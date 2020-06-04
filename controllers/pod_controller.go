/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/spiffe/spire/proto/spire/api/registration"
	"github.com/spiffe/spire/proto/spire/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"net/url"
	"path"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"strings"
	"time"
)

type PodReconcilerMode int32

const (
	PodReconcilerModeServiceAccount PodReconcilerMode = iota
	PodReconcilerModeLabel
	PodReconcilerModeAnnotation
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	TrustDomain string
	Mode        PodReconcilerMode
	Value       string
	MyId        string
	SpireClient registration.RegistrationClient
}

type WorkloadSelectorSubType string

const (
	PodNamespaceSelector WorkloadSelectorSubType = "ns"
	PodNameSelector                              = "pod-name"
)

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

func (r *PodReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("pod", req.NamespacedName)

	var pod corev1.Pod
	err := r.Get(ctx, req.NamespacedName, &pod)
	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Unable to fetch Pod")
		return ctrl.Result{}, err
	}

	podDeleted := errors.IsNotFound(err) || !pod.ObjectMeta.DeletionTimestamp.IsZero()

	podSpireEntries, err := r.getEntriesMatchingPod(ctx, log, req.NamespacedName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if podDeleted {
		err := r.deleteAllEntries(ctx, log, podSpireEntries)
		return ctrl.Result{}, err
	}

	spiffeId := r.makeSpiffeIdForPod(&pod)
	if spiffeId == "" {
		// Pod does not need an entry. This might be a change, so we need to delete any hanging entries.
		err := r.deleteAllEntries(ctx, log, podSpireEntries)
		return ctrl.Result{}, err
	}

	nodeName := pod.Spec.NodeName
	if nodeName == "" {
		log.Info("Pod not scheduled on a node yet, ignoring")
		return ctrl.Result{}, nil
	}
	parentId := makeSpiffeIdForNodeName(r.MyId, nodeName)

	createEntryIfNotExistsResponse, err := r.SpireClient.CreateEntryIfNotExists(ctx, &common.RegistrationEntry{
		Selectors: []*common.Selector{
			r.k8sWorkloadSelector(PodNamespaceSelector, pod.Namespace),
			r.k8sWorkloadSelector(PodNameSelector, pod.Name),
		},
		ParentId: parentId,
		SpiffeId: spiffeId,
	})
	if err != nil {
		log.Error(err, "Failed to create or update spire entry")
		return ctrl.Result{}, err
	}
	if !createEntryIfNotExistsResponse.Preexisting {
		log.Info("Created new spire entry", "entry", createEntryIfNotExistsResponse.Entry)
	}

	err = r.deleteAllEntriesExcept(ctx, log, podSpireEntries, createEntryIfNotExistsResponse.Entry.EntryId)

	return ctrl.Result{}, err
}

func (r *PodReconciler) deleteAllEntries(ctx context.Context, reqLogger logr.Logger, entryIds []string) error {
	for _, entry := range entryIds {
		err := r.ensureDeleted(ctx, reqLogger, entry)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *PodReconciler) deleteAllEntriesExcept(ctx context.Context, reqLogger logr.Logger, entryIds []string, skip string) error {
	for _, entry := range entryIds {
		if entry != skip {
			err := r.ensureDeleted(ctx, reqLogger, entry)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *PodReconciler) getEntriesMatchingPod(ctx context.Context, reqLogger logr.Logger, podNamespacedName types.NamespacedName) ([]string, error) {
	entries, err := r.SpireClient.ListBySelectors(ctx, &common.Selectors{
		Entries: []*common.Selector{
			r.k8sWorkloadSelector(PodNamespaceSelector, podNamespacedName.Namespace),
			r.k8sWorkloadSelector(PodNameSelector, podNamespacedName.Name),
		},
	})
	if err != nil {
		reqLogger.Error(err, "Failed to load entries matching pod")
		return nil, err
	}
	var result []string

	nodePrefix := makeSpiffeIdForNodeName(r.MyId, "")
	for _, entry := range entries.Entries {
		if strings.HasPrefix(entry.ParentId, nodePrefix) {
			result = append(result, entry.EntryId)
		}
	}
	return result, nil
}

func (r *PodReconciler) makeSpiffeIdForPod(pod *corev1.Pod) string {
	spiffeId := ""
	switch r.Mode {
	case PodReconcilerModeServiceAccount:
		spiffeId = r.makeID(r.TrustDomain, "ns/%s/sa/%s", pod.Namespace, pod.Spec.ServiceAccountName)
	case PodReconcilerModeLabel:
		if val, ok := pod.GetLabels()[r.Value]; ok {
			spiffeId = r.makeID("%s", val)
		}
	case PodReconcilerModeAnnotation:
		if val, ok := pod.GetAnnotations()[r.Value]; ok {
			spiffeId = r.makeID("%s", val)
		}
	}
	return spiffeId
}

func (r *PodReconciler) k8sWorkloadSelector(selector WorkloadSelectorSubType, value string) *common.Selector {
	return &common.Selector{
		Type:  "k8s",
		Value: fmt.Sprintf("%s:%s", selector, value),
	}
}

func (r *PodReconciler) ensureDeleted(ctx context.Context, reqLogger logr.Logger, entryId string) error {
	return ensureSpireEntryDeleted(r.SpireClient, ctx, reqLogger, entryId)
}

func (r *PodReconciler) selectorsToNamespacedName(selectors []*common.Selector) *types.NamespacedName {
	podNamespace := ""
	podName := ""
	for _, selector := range selectors {
		if selector.Type == "k8s" {
			splitted := strings.SplitN(selector.Value, ":", 1)
			if len(splitted) > 1 {
				switch WorkloadSelectorSubType(splitted[0]) {
				case PodNamespaceSelector:
					podNamespace = splitted[1]
					break
				case PodNameSelector:
					podName = splitted[1]
					break
				}
			}
		}
	}
	if podNamespace != "" && podName != "" {
		return &types.NamespacedName{
			Namespace: podNamespace,
			Name:      podName,
		}
	}
	return nil
}

func (r *PodReconciler) pollSpire(out chan event.GenericEvent, s <-chan struct{}) error {

	ctx := context.Background()
	log := r.Log
	for {
		select {
		case <-s:
			return nil
		case <-time.After(10 * time.Second):
			log.Info("Syncing spire server pod entries")

			nodeEntries, err := r.SpireClient.ListByParentID(ctx, &registration.ParentID{Id: r.MyId})
			if err != nil {
				log.Error(err, "Unable to list spire node entries")
				continue
			}
			seen := make(map[string]bool)
			for _, nodeEntry := range nodeEntries.Entries {
				entries, err := r.SpireClient.ListByParentID(ctx, &registration.ParentID{Id: nodeEntry.SpiffeId})
				if err != nil {
					log.Error(err, "Unable to list spire pod entries")
					continue
				}
				for _, entry := range entries.Entries {
					if namespacedName := r.selectorsToNamespacedName(entry.Selectors); namespacedName != nil {
						reconcile := false
						if seen[namespacedName.String()] {
							// More than one entry found
							reconcile = true
						} else {
							var pod corev1.Pod
							err := r.Get(ctx, *namespacedName, &pod)
							if err != nil {
								if errors.IsNotFound(err) {
									// Pod has been deleted
									reconcile = true
								} else {
									log.Error(err, "Unable to fetch Pod", "pod", namespacedName)
								}
							} else {
								if r.makeSpiffeIdForPod(&pod) == "" {
									// Pod no longer needs a spiffe ID
									reconcile = true
								}
							}
						}
						seen[namespacedName.String()] = true
						if reconcile {
							log.Info("Triggering reconciliation for pod", "pod", namespacedName)
							out <- event.GenericEvent{Meta: &v1.ObjectMeta{
								Name:      namespacedName.Name,
								Namespace: namespacedName.Namespace,
							}}
						}
					}
				}
			}
		}
	}
}

type SpirePodEntryPoller struct {
	r   *PodReconciler
	out chan event.GenericEvent
}

// Start implements Runnable
func (p *SpirePodEntryPoller) Start(s <-chan struct{}) error {
	return p.r.pollSpire(p.out, s)
}

func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {

	events := make(chan event.GenericEvent)

	err := mgr.Add(&SpirePodEntryPoller{
		r:   r,
		out: events,
	})
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Watches(&source.Channel{Source: events}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}

func (r *PodReconciler) makeID(pathFmt string, pathArgs ...interface{}) string {
	id := url.URL{
		Scheme: "spiffe",
		Host:   r.TrustDomain,
		Path:   path.Clean(fmt.Sprintf(pathFmt, pathArgs...)),
	}
	return id.String()
}
