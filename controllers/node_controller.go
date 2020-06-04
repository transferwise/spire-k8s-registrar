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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"strings"
	"time"
)

// NodeReconciler reconciles a Node object
type NodeReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	TrustDomain string
	MyId        string
	SpireClient registration.RegistrationClient
}

type NodeSelectorSubType string

const (
	NodeNameSelector NodeSelectorSubType = "agent_node_name"
)

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

func (r *NodeReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("node", req.NamespacedName)

	var node corev1.Node
	err := r.Get(ctx, req.NamespacedName, &node)
	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Unable to fetch node")
		return ctrl.Result{}, err
	}

	nodeDeleted := errors.IsNotFound(err) || !node.ObjectMeta.DeletionTimestamp.IsZero()

	nodeSpireEntries, err := r.getEntriesMatchingNode(ctx, log, req.NamespacedName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if nodeDeleted {
		err := r.deleteAllEntries(ctx, log, nodeSpireEntries)
		return ctrl.Result{}, err
	}

	spiffeId := r.makeSpiffeIdForNode(&node)

	createEntryIfNotExistsResponse, err := r.SpireClient.CreateEntryIfNotExists(ctx, &common.RegistrationEntry{
		Selectors: []*common.Selector{
			r.k8sNodeSelector(NodeNameSelector, node.Name),
		},
		ParentId: r.MyId,
		SpiffeId: spiffeId,
	})
	if err != nil {
		log.Error(err, "Failed to create or update spire entry")
		return ctrl.Result{}, err
	}
	if !createEntryIfNotExistsResponse.Preexisting {
		log.Info("Created new spire entry", "entry", createEntryIfNotExistsResponse.Entry)
	}

	err = r.deleteAllEntriesExcept(ctx, log, nodeSpireEntries, createEntryIfNotExistsResponse.Entry.EntryId)

	return ctrl.Result{}, err
}

func (r *NodeReconciler) deleteAllEntries(ctx context.Context, reqLogger logr.Logger, entryIds []string) error {
	for _, entry := range entryIds {
		err := r.ensureDeleted(ctx, reqLogger, entry)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *NodeReconciler) deleteAllEntriesExcept(ctx context.Context, reqLogger logr.Logger, entryIds []string, skip string) error {
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

func (r *NodeReconciler) getEntriesMatchingNode(ctx context.Context, reqLogger logr.Logger, nodeNamespacedName types.NamespacedName) ([]string, error) {
	entries, err := r.SpireClient.ListBySelectors(ctx, &common.Selectors{
		Entries: []*common.Selector{
			r.k8sNodeSelector(NodeNameSelector, nodeNamespacedName.Name),
		},
	})
	if err != nil {
		reqLogger.Error(err, "Failed to load entries matching node")
		return nil, err
	}
	var result []string
	for _, entry := range entries.Entries {
		if entry.ParentId == r.MyId {
			result = append(result, entry.EntryId)
		}
	}
	return result, nil
}

func (r *NodeReconciler) makeSpiffeIdForNode(node *corev1.Node) string {
	return makeSpiffeIdForNodeName(r.MyId, node.Name)
}

func (r *NodeReconciler) k8sNodeSelector(selector NodeSelectorSubType, value string) *common.Selector {
	return &common.Selector{
		Type:  "k8s_psat",
		Value: fmt.Sprintf("%s:%s", selector, value),
	}
}

func (r *NodeReconciler) ensureDeleted(ctx context.Context, reqLogger logr.Logger, entryId string) error {
	return ensureSpireEntryDeleted(r.SpireClient, ctx, reqLogger, entryId)
}

func (r *NodeReconciler) selectorsToNamespacedName(selectors []*common.Selector) *types.NamespacedName {
	nodeName := ""
	for _, selector := range selectors {
		if selector.Type == "k8s_psat" {
			splitted := strings.SplitN(selector.Value, ":", 1)
			if len(splitted) > 1 {
				switch NodeSelectorSubType(splitted[0]) {
				case NodeNameSelector:
					nodeName = splitted[1]
					break
				}
			}
		}
	}
	if nodeName != "" {
		return &types.NamespacedName{
			Namespace: "",
			Name:      nodeName,
		}
	}
	return nil
}

func (r *NodeReconciler) pollSpire(out chan event.GenericEvent, s <-chan struct{}) error {

	ctx := context.Background()
	log := r.Log
	for {
		select {
		case <-s:
			return nil
		case <-time.After(10 * time.Second):
			log.Info("Syncing spire server node entries")
			entries, err := r.SpireClient.ListByParentID(ctx, &registration.ParentID{Id: r.MyId})
			if err != nil {
				log.Error(err, "Unable to list spire entries")
				continue
			}
			seen := make(map[string]bool)
			for _, entry := range entries.Entries {
				if namespacedName := r.selectorsToNamespacedName(entry.Selectors); namespacedName != nil {
					reconcile := false
					if seen[namespacedName.String()] {
						// More than one entry found
						reconcile = true
					} else {
						var node corev1.Node
						err := r.Get(ctx, *namespacedName, &node)
						if err != nil {
							if errors.IsNotFound(err) {
								// Node has been deleted
								reconcile = true
							} else {
								log.Error(err, "Unable to fetch Node", "node", namespacedName)
							}
						}
					}
					seen[namespacedName.String()] = true
					if reconcile {
						log.Info("Triggering reconciliation for node", "node", namespacedName)
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

type SpireNodePoller struct {
	r   *NodeReconciler
	out chan event.GenericEvent
}

// Start implements Runnable
func (p *SpireNodePoller) Start(s <-chan struct{}) error {
	return p.r.pollSpire(p.out, s)
}

func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {

	events := make(chan event.GenericEvent)

	err := mgr.Add(&SpireNodePoller{
		r:   r,
		out: events,
	})
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Watches(&source.Channel{Source: events}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}
