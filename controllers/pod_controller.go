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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"net/url"
	"path"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

type PodReconcilerMode int32

const (
	PodReconcilerModeServiceAccount PodReconcilerMode = iota
	PodReconcilerModeLabel
	PodReconcilerModeAnnotation
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
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

func (r *PodReconciler) makeID(pathFmt string, pathArgs ...interface{}) string {
	id := url.URL{
		Scheme: "spiffe",
		Host:   r.TrustDomain,
		Path:   path.Clean(fmt.Sprintf(pathFmt, pathArgs...)),
	}
	return id.String()
}

func (r *PodReconciler) makeSpiffeId(obj ObjectWithMetadata) string {
	return r.makeSpiffeIdForPod(obj.(*corev1.Pod))
}

func (r *PodReconciler) makeParentIdForPod(pod *corev1.Pod) string {
	nodeName := pod.Spec.NodeName
	if nodeName == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s", r.MyId, nodeName)
}

func (r *PodReconciler) makeParentId(obj ObjectWithMetadata) string {
	return r.makeParentIdForPod(obj.(*corev1.Pod))
}

func (r *PodReconciler) getSelectors(namespacedName types.NamespacedName) []*common.Selector {
	return []*common.Selector{
		r.k8sWorkloadSelector(PodNamespaceSelector, namespacedName.Namespace),
		r.k8sWorkloadSelector(PodNameSelector, namespacedName.Name),
	}
}

func (r *PodReconciler) getAllEntries(ctx context.Context) ([]*common.RegistrationEntry, error) {
	nodeEntries, err := r.SpireClient.ListByParentID(ctx, &registration.ParentID{Id: r.MyId})
	if err != nil {
		return nil, err
	}
	var allEntries []*common.RegistrationEntry
	for _, nodeEntry := range nodeEntries.Entries {
		entries, err := r.SpireClient.ListByParentID(ctx, &registration.ParentID{Id: nodeEntry.SpiffeId})
		if err != nil {
			return nil, err
		}
		allEntries = append(allEntries, entries.Entries...)
	}
	return allEntries, nil
}

func (r *PodReconciler) getObject() ObjectWithMetadata {
	return &corev1.Pod{}
}

func NewPodReconciler(client client.Client, log logr.Logger, scheme *runtime.Scheme, trustDomain string, myId string, spireClient registration.RegistrationClient, mode PodReconcilerMode, value string) *BaseReconciler {
	return &BaseReconciler{
		Client:      client,
		Scheme:      scheme,
		TrustDomain: trustDomain,
		MyId:        myId,
		SpireClient: spireClient,
		Log:         log,
		ObjectReconcilier: &PodReconciler{
			MyId:        myId,
			SpireClient: spireClient,
			TrustDomain: trustDomain,
			Mode:        mode,
			Value:       value,
		},
	}
}
