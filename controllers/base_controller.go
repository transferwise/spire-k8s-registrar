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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

type ObjectReconcilier interface {
	makeSpiffeId(ObjectWithMetadata) string
	makeParentId(ObjectWithMetadata) string
	getSelectors(types.NamespacedName) []*common.Selector
	getAllEntries(context.Context) ([]*common.RegistrationEntry, error)
	selectorsToNamespacedName([]*common.Selector) *types.NamespacedName
	getObject() ObjectWithMetadata
}

// BaseReconciler reconciles... something
type BaseReconciler struct {
	client.Client
	ObjectReconcilier
	Scheme      *runtime.Scheme
	TrustDomain string
	MyId        string
	SpireClient registration.RegistrationClient
	Log         logr.Logger
}

type RuntimeObject = runtime.Object
type V1Object = v1.Object

type ObjectWithMetadata interface {
	RuntimeObject
	V1Object
}

func (r *BaseReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	reqLogger := r.Log.WithValues("request", req.NamespacedName)

	obj := r.getObject()
	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil && !errors.IsNotFound(err) {
		reqLogger.Error(err, "Unable to fetch resource")
		return ctrl.Result{}, err
	}

	isDeleted := errors.IsNotFound(err) || !obj.GetDeletionTimestamp().IsZero()

	matchedEntries, err := r.getMatchingEntries(ctx, reqLogger, req.NamespacedName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if isDeleted {
		err := r.deleteAllEntries(ctx, reqLogger, matchedEntries)
		return ctrl.Result{}, err
	}

	spiffeId := r.makeSpiffeId(obj)
	if spiffeId == "" {
		// Object does not need an entry. This might be a change, so we need to delete any hanging entries.
		err := r.deleteAllEntries(ctx, reqLogger, matchedEntries)
		return ctrl.Result{}, err
	}

	createEntryIfNotExistsResponse, err := r.SpireClient.CreateEntryIfNotExists(ctx, &common.RegistrationEntry{
		Selectors: r.getSelectors(req.NamespacedName),
		ParentId:  r.makeParentId(obj),
		SpiffeId:  spiffeId,
	})

	if err != nil {
		reqLogger.Error(err, "Failed to create or update spire entry")
		return ctrl.Result{}, err
	}
	if !createEntryIfNotExistsResponse.Preexisting {
		reqLogger.Info("Created new spire entry", "entry", createEntryIfNotExistsResponse.Entry)
	}

	err = r.deleteAllEntriesExcept(ctx, reqLogger, matchedEntries, createEntryIfNotExistsResponse.Entry.EntryId)

	return ctrl.Result{}, err
}

func (r *BaseReconciler) deleteAllEntries(ctx context.Context, reqLogger logr.Logger, entryIds []string) error {
	for _, entry := range entryIds {
		err := r.ensureDeleted(ctx, reqLogger, entry)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *BaseReconciler) deleteAllEntriesExcept(ctx context.Context, reqLogger logr.Logger, entryIds []string, skip string) error {
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

func (r *BaseReconciler) getMatchingEntries(ctx context.Context, reqLogger logr.Logger, namespacedName types.NamespacedName) ([]string, error) {
	entries, err := r.SpireClient.ListBySelectors(ctx, &common.Selectors{
		Entries: r.getSelectors(namespacedName),
	})
	if err != nil {
		reqLogger.Error(err, "Failed to load entries")
		return nil, err
	}
	var result []string
	for _, entry := range entries.Entries {
		if strings.HasPrefix(entry.ParentId, r.MyId) {
			result = append(result, entry.EntryId)
		}
	}
	return result, nil
}

func (r *NodeReconciler) k8sNodeSelector(selector NodeSelectorSubType, value string) *common.Selector {
	return &common.Selector{
		Type:  "k8s_psat",
		Value: fmt.Sprintf("%s:%s", selector, value),
	}
}

func (r *BaseReconciler) ensureDeleted(ctx context.Context, reqLogger logr.Logger, entryId string) error {
	if _, err := r.SpireClient.DeleteEntry(ctx, &registration.RegistrationEntryID{Id: entryId}); err != nil {
		if status.Code(err) != codes.NotFound {
			if status.Code(err) == codes.Internal {
				// Spire server currently returns a 500 if delete fails due to the entry not existing. This is probably a bug.
				// We work around it by attempting to fetch the entry, and if it's not found then all is good.
				if _, err := r.SpireClient.FetchEntry(ctx, &registration.RegistrationEntryID{Id: entryId}); err != nil {
					if status.Code(err) == codes.NotFound {
						reqLogger.V(1).Info("Entry already deleted", "entry", entryId)
						return nil
					}
				}
			}
			return err
		}
	}
	reqLogger.Info("deleted entry", "entry", entryId)
	return nil
}

func (r *BaseReconciler) pollSpire(out chan event.GenericEvent, s <-chan struct{}) error {
	ctx := context.Background()
	log := r.Log
	for {
		select {
		case <-s:
			return nil
		case <-time.After(10 * time.Second):
			log.Info("Syncing spire entries")
			start := time.Now()
			seen := make(map[string]bool)
			entries, err := r.getAllEntries(ctx)
			if err != nil {
				log.Error(err, "Unable to fetch entries")
				break
			}
			reconciled := 0
			for _, entry := range entries {
				if namespacedName := r.selectorsToNamespacedName(entry.Selectors); namespacedName != nil {
					reconcile := false
					if seen[namespacedName.String()] {
						// More than one entry found
						reconcile = true
					} else {
						obj := r.getObject()
						err := r.Get(ctx, *namespacedName, obj)
						if err != nil {
							if errors.IsNotFound(err) {
								// resource has been deleted
								reconcile = true
							} else {
								log.Error(err, "Unable to fetch resource", "name", namespacedName)
							}
						} else {
							if r.makeSpiffeId(obj) == "" {
								// No longer needs an entry
								reconcile = true
							}
						}
					}
					seen[namespacedName.String()] = true
					if reconcile {
						reconciled++
						log.V(1).Info("Triggering reconciliation for resource", "name", namespacedName)
						out <- event.GenericEvent{Meta: &v1.ObjectMeta{
							Name:      namespacedName.Name,
							Namespace: namespacedName.Namespace,
						}}
					}
				}
			}
			log.Info("Synced spire entries", "took", time.Since(start), "found", len(entries), "queued", reconciled)
		}
	}
}

type SpirePoller struct {
	r   *BaseReconciler
	out chan event.GenericEvent
}

// Start implements Runnable
func (p *SpirePoller) Start(s <-chan struct{}) error {
	return p.r.pollSpire(p.out, s)
}

func (r *BaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	events := make(chan event.GenericEvent)

	err := mgr.Add(&SpirePoller{
		r:   r,
		out: events,
	})
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(r.getObject()).
		Watches(&source.Channel{Source: events}, &handler.EnqueueRequestForObject{}).
		Complete(r)
}
