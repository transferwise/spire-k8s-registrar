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
	"github.com/davecgh/go-spew/spew"
	"github.com/go-logr/logr"
	"hash"
	"hash/fnv"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/rand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	spiffeidv1beta1 "github.com/transferwise/spire-k8s-registrar/api/v1beta1"
)

// ClusterSpiffeIDReconciler reconciles a ClusterSpiffeID object
type ClusterSpiffeIDReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=spiffeid.spiffe.io,resources=clusterspiffeids,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=spiffeid.spiffe.io,resources=spireentries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=spiffeid.spiffe.io,resources=clusterspiffeids/status,verbs=get;update;patch

func (r *ClusterSpiffeIDReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("clusterspiffeid", req.NamespacedName)

	var clusterSpiffeID spiffeidv1beta1.ClusterSpiffeID
	if err := r.Get(ctx, req.NamespacedName, &clusterSpiffeID); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "unable to fetch ClusterSpiffeID")
		}
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	entrySpec := spiffeidv1beta1.SpireEntrySpec{
		SpiffeId: clusterSpiffeID.Spec.SpiffeId,
		Selector: clusterSpiffeID.Spec.Selector,
	}

	// TODO: This wont cope with hash collisions at all...
	spireEntrySpecHasher := fnv.New64a()
	deepHashObject(spireEntrySpecHasher, entrySpec)
	entryName := rand.SafeEncodeString(fmt.Sprint(spireEntrySpecHasher.Sum64()))

	entry := spiffeidv1beta1.SpireEntry{
		ObjectMeta: v1.ObjectMeta{
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
			Name:        entryName,
			Namespace:   req.Namespace,
		},
		Spec: entrySpec,
	}
	if err := controllerutil.SetControllerReference(&clusterSpiffeID, &entry, r.Scheme); err != nil {
		log.Error(err, "Failed to set controller reference on new entry")
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, &entry); err != nil {
		if !errors.IsAlreadyExists(err) {
			log.Error(err, "unable to create SpireEntry")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: entryName}, &entry); err != nil {
			log.Error(err, "unable to fetch existing entry")
			return ctrl.Result{}, err
		}
		if v1.GetControllerOf(&entry) != nil {
			if err := addOwnerReference(&clusterSpiffeID, &entry, r.Scheme); err != nil {
				log.Error(err, "Failed to set owner reference on existing entry")
				return ctrl.Result{}, err
			}
			log.Info("adding owner reference on existing entry")
		} else {
			if err := controllerutil.SetControllerReference(&clusterSpiffeID, &entry, r.Scheme); err != nil {
				log.Error(err, "Failed to set controller reference on existing entry")
				return ctrl.Result{}, err
			}
			log.Info("updating controller ref on existing entry to take ownership")
		}
		if err := r.Update(ctx, &entry); err != nil {
			log.Error(err, "Failed to update existing entry")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func addOwnerReference(owner, object v1.Object, scheme *runtime.Scheme) error {
	ro, ok := owner.(runtime.Object)
	if !ok {
		return fmt.Errorf("%T is not a runtime.Object, cannot call addOwnerReference", owner)
	}

	gvk, err := apiutil.GVKForObject(ro, scheme)
	if err != nil {
		return err
	}

	ref := v1.OwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Name:       owner.GetName(),
		UID:        owner.GetUID(),
	}

	existingRefs := object.GetOwnerReferences()
	for _, r := range existingRefs {
		if referSameObject(ref, r) {
			return nil
		}
	}
	existingRefs = append(existingRefs, ref)

	// Update owner references
	object.SetOwnerReferences(existingRefs)
	return nil
}

// Returns true if a and b point to the same object
func referSameObject(a, b v1.OwnerReference) bool {
	aGV, err := schema.ParseGroupVersion(a.APIVersion)
	if err != nil {
		return false
	}

	bGV, err := schema.ParseGroupVersion(b.APIVersion)
	if err != nil {
		return false
	}

	return aGV == bGV && a.Kind == b.Kind && a.Name == b.Name
}

func deepHashObject(hasher hash.Hash, objectToWrite interface{}) {
	hasher.Reset()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	printer.Fprintf(hasher, "%#v", objectToWrite)
}

func (r *ClusterSpiffeIDReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&spiffeidv1beta1.ClusterSpiffeID{}).
		Watches(&source.Kind{Type: &spiffeidv1beta1.SpireEntry{}}, &handler.EnqueueRequestForOwner{OwnerType: &spiffeidv1beta1.ClusterSpiffeID{}}).
		Complete(r)
}
