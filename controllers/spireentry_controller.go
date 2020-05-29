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
	"github.com/spiffe/spire/proto/spire/api/registration"
	spiffeidv1beta1 "github.com/transferwise/spire-k8s-registrar/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SpireEntryReconciler reconciles a SpireEntry object
type SpireEntryReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	SpireClient registration.RegistrationClient
	MyId        string
}

// +kubebuilder:rbac:groups=spiffeid.io.spiffe,resources=spireentries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=spiffeid.io.spiffe,resources=spireentries/status,verbs=get;update;patch

func (r *SpireEntryReconciler) SetupWithManager(mgr ctrl.Manager) error {

	err := mgr.Add(manager.RunnableFunc(func(s <-chan struct{}) error {
		ctx := context.Background()
		log := r.Log
		for {
			select {
			case <-s:
				return nil
			case <-time.After(10 * time.Second):
				log.Info("Syncing spire server entries")
				entries, err := r.SpireClient.ListByParentID(ctx, &registration.ParentID{Id: r.MyId})
				if err != nil {
					log.Error(err, "Unable to list spire entries")
					continue
				}

				knownEntries := make(map[string]bool)

				for _, entry := range entries.Entries {
					knownEntries[entry.EntryId] = true
					err := r.Create(ctx, &spiffeidv1beta1.SpireEntry{
						ObjectMeta: v1.ObjectMeta{
							Labels:      make(map[string]string),
							Annotations: make(map[string]string),
							Name:        entry.EntryId,
						},
						Spec: spiffeidv1beta1.SpireEntrySpec{},
					})
					if err != nil {
						if !errors.IsAlreadyExists(err) {
							log.Error(err, "unable to sync new spire entry", "entryId", entry.EntryId, "spiffeId", entry.SpiffeId)
							continue
						}
					} else {
						log.Info("synced new spire entry", "entryId", entry.EntryId, "spiffeId", entry.SpiffeId)
					}
				}

				var resources spiffeidv1beta1.SpireEntryList
				if err := r.List(ctx, &resources); err != nil {
					log.Error(err, "Unable to list spire entry resources")
					continue
				}
				for _, resource := range resources.Items {
					if _, known := knownEntries[resource.Name]; known == false {
						// Entry must have been deleted from the server, remove the resource
						log.Info("Deleting removed entry", "name", resource.Name)
						if err := r.Delete(ctx, &resource); err != nil {
							log.Error(err, "Unable to delete removed entry", "name", resource.Name)
							continue
						}
					}
				}
			}
		}
	}))

	if err != nil {
		return err
	}

	return nil
}
