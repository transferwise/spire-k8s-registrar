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
	spiffeidv1beta1 "github.com/transferwise/spire-k8s-registrar/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"net/url"
	"path"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch

func (r *PodReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("pod", req.NamespacedName)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "unable to fetch Pod")
		}
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	spiffeidname := fmt.Sprintf("spire-operator-%s", pod.GetName())

	spiffeId := ""
	switch r.Mode {
	case PodReconcilerModeServiceAccount:
		spiffeId = r.makeID("ns/%s/sa/%s", req.Namespace, pod.Spec.ServiceAccountName)
	case PodReconcilerModeLabel:
		if val, ok := pod.GetLabels()[r.Value]; ok {
			spiffeId = r.makeID("%s", val)
		} else {
			// No relevant label
			return ctrl.Result{}, nil
		}
	case PodReconcilerModeAnnotation:
		if val, ok := pod.GetAnnotations()[r.Value]; ok {
			spiffeId = r.makeID("%s", val)
		} else {
			// No relevant annotation
			return ctrl.Result{}, nil
		}
	}

	existing := &spiffeidv1beta1.ClusterSpiffeID{}
	if err := r.Get(ctx, types.NamespacedName{Name: spiffeidname}, existing); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Failed to get ClusterSpiffeID", "name", spiffeidname)
			return ctrl.Result{}, err
		}
		clusterSpiffeId := &spiffeidv1beta1.ClusterSpiffeID{
			ObjectMeta: v1.ObjectMeta{
				Name: spiffeidname,
			},
			Spec: spiffeidv1beta1.ClusterSpiffeIDSpec{
				SpiffeId: spiffeId,
				Selector: spiffeidv1beta1.Selector{
					PodName:   pod.Name,
					Namespace: pod.Namespace,
				},
			},
		}
		// Namespace scoped resource cannot own a cluster scoped resource
		// Work out how to deal with GC later...
		//err = controllerutil.SetControllerReference(&pod, clusterSpiffeId, r.Scheme)
		//if err != nil {
		//	log.Error(err, "Failed to create new SpiffeID", "SpiffeID.Name", clusterSpiffeId.Name)
		//	return ctrl.Result{}, err
		//}
		log.Info("Creating a new SpiffeID", "SpiffeID.Name", clusterSpiffeId.Name)
		err = r.Create(ctx, clusterSpiffeId)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				log.Error(err, "Failed to create new SpiffeID", "SpiffeID.Name", clusterSpiffeId.Name)
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
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
