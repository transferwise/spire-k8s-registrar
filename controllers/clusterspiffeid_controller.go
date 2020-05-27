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
	spiffeidv1beta1 "github.com/transferwise/spire-k8s-registrar/api/v1beta1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"net/url"
	"path"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterSpiffeIDReconciler reconciles a ClusterSpiffeID object
type ClusterSpiffeIDReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	myId        *string
	SpireClient registration.RegistrationClient
	TrustDomain string
	Cluster     string
}

var (
	spireEntryIdKey = ".status.entryId"
)

// +kubebuilder:rbac:groups=spiffeid.spiffe.io,resources=clusterspiffeids,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=spiffeid.spiffe.io,resources=spireentries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=spiffeid.spiffe.io,resources=clusterspiffeids/status,verbs=get;update;patch

func (r *ClusterSpiffeIDReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("clusterspiffeid", req.NamespacedName)

	if r.myId == nil {
		if err := r.makeMyId(ctx, log); err != nil {
			log.Error(err, "unable to create parent ID")
			return ctrl.Result{}, err
		}
	}

	var clusterSpiffeId spiffeidv1beta1.ClusterSpiffeID
	if err := r.Get(ctx, req.NamespacedName, &clusterSpiffeId); err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Error(err, "unable to fetch ClusterSpiffeID")
		}
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	myFinalizerName := "spire.finalizers.clusterspiffeid.spiffeid.spiffe.io"
	if clusterSpiffeId.ObjectMeta.DeletionTimestamp.IsZero() {
		if !containsString(clusterSpiffeId.GetFinalizers(), myFinalizerName) {
			clusterSpiffeId.SetFinalizers(append(clusterSpiffeId.GetFinalizers(), myFinalizerName))
			if err := r.Update(ctx, &clusterSpiffeId); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if containsString(clusterSpiffeId.GetFinalizers(), myFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.safeDelete(ctx, log, *clusterSpiffeId.Status.EntryId, clusterSpiffeId.GetUID()); err != nil {
				log.Error(err, "unable to delete spire entry", "entryid", *clusterSpiffeId.Status.EntryId)
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			clusterSpiffeId.SetFinalizers(removeString(clusterSpiffeId.GetFinalizers(), myFinalizerName))
			if err := r.Update(ctx, &clusterSpiffeId); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Finalized entry", "entry", clusterSpiffeId.Name)
		}
		return ctrl.Result{}, nil
	}

	entryId, err := r.getOrCreateSpireEntry(ctx, log, &clusterSpiffeId)
	if err != nil {
		log.Error(err, "unable to create spire entry", "request", req)
		return ctrl.Result{}, err
	}
	oldEntryId := clusterSpiffeId.Status.EntryId
	if oldEntryId == nil || *oldEntryId != entryId {
		// We need to update the Status field
		clusterSpiffeId.Status.EntryId = &entryId
		if oldEntryId != nil {
			// entry resource must have been modified, delete the hanging one
			if err := r.safeDelete(ctx, log, *clusterSpiffeId.Status.EntryId, clusterSpiffeId.GetUID()); err != nil {
				log.Error(err, "unable to delete old spire entry", "entryid", *clusterSpiffeId.Status.EntryId)
				return ctrl.Result{}, err
			}
		}
		if err := r.Status().Update(ctx, &clusterSpiffeId); err != nil {
			log.Error(err, "unable to update ClusterSpiffeID status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ClusterSpiffeIDReconciler) safeDelete(ctx context.Context, reqLogger logr.Logger, entryId string, myUid types.UID) error {
	var siblings spiffeidv1beta1.ClusterSpiffeIDList
	if err := r.List(ctx, &siblings, client.MatchingFields{spireEntryIdKey: entryId}); err != nil {
		reqLogger.Error(err, "unable to list IDs that potentially share our entry")
		return err
	}
	numSiblings := len(siblings.Items)
	if numSiblings > 1 || (numSiblings == 1 && siblings.Items[0].GetUID() != myUid) {
		reqLogger.Info("Spire entry still in use by other resources.", "entry", entryId, "references", numSiblings)
		return nil
	}
	if err := r.ensureDeleted(ctx, reqLogger, entryId); err != nil {
		reqLogger.Error(err, "unable to delete unused spire entry", "entry", entryId)
		return err
	}
	return nil
}

func (r *ClusterSpiffeIDReconciler) ensureDeleted(ctx context.Context, reqLogger logr.Logger, entryId string) error {
	if _, err := r.SpireClient.DeleteEntry(ctx, &registration.RegistrationEntryID{Id: entryId}); err != nil {
		if status.Code(err) != codes.NotFound {
			if status.Code(err) == codes.Internal {
				// Spire server currently returns a 500 if delete fails due to the entry not existing. This is probably a bug.
				// We work around it by attempting to fetch the entry, and if it's not found then all is good.
				if _, err := r.SpireClient.FetchEntry(ctx, &registration.RegistrationEntryID{Id: entryId}); err != nil {
					if status.Code(err) == codes.NotFound {
						reqLogger.Info("Entry already deleted", "entry", entryId)
						return nil
					}
				}
			}
			return err
		}
	}
	reqLogger.Info("Deleted entry", "entry", entryId)
	return nil
}

// ServerURI creates a server SPIFFE URI given a trustDomain.
func ServerURI(trustDomain string) *url.URL {
	return &url.URL{
		Scheme: "spiffe",
		Host:   trustDomain,
		Path:   path.Join("spire", "server"),
	}
}

// ServerID creates a server SPIFFE ID string given a trustDomain.
func ServerID(trustDomain string) string {
	return ServerURI(trustDomain).String()
}

func (r *ClusterSpiffeIDReconciler) makeID(pathFmt string, pathArgs ...interface{}) string {
	id := url.URL{
		Scheme: "spiffe",
		Host:   r.TrustDomain,
		Path:   path.Clean(fmt.Sprintf(pathFmt, pathArgs...)),
	}
	return id.String()
}

func (r *ClusterSpiffeIDReconciler) nodeID() string {
	return r.makeID("spire-k8s-operator/%s/node", r.Cluster)
}

func (r *ClusterSpiffeIDReconciler) makeMyId(ctx context.Context, reqLogger logr.Logger) error {
	myId := r.nodeID()
	reqLogger.Info("Initializing operator parent ID.")
	_, err := r.SpireClient.CreateEntry(ctx, &common.RegistrationEntry{
		Selectors: []*common.Selector{
			{Type: "k8s_psat", Value: fmt.Sprintf("cluster:%s", r.Cluster)},
		},
		ParentId: ServerID(r.TrustDomain),
		SpiffeId: myId,
	})
	if err != nil {
		if status.Code(err) != codes.AlreadyExists {
			reqLogger.Info("Failed to create operator parent ID", "spiffeID", myId)
			return err
		}
	}
	reqLogger.Info("Initialized operator parent ID", "spiffeID", myId)
	r.myId = &myId
	return nil
}

func (r *ClusterSpiffeIDReconciler) getOrCreateSpireEntry(ctx context.Context, reqLogger logr.Logger, instance *spiffeidv1beta1.ClusterSpiffeID) (string, error) {

	// TODO: sanitize!
	selectors := make([]*common.Selector, 0, len(instance.Spec.Selector.PodLabel))
	for k, v := range instance.Spec.Selector.PodLabel {
		selectors = append(selectors, &common.Selector{
			Type:  "k8s",
			Value: fmt.Sprintf("pod-label:%s:%s", k, v),
		})
	}
	if len(instance.Spec.Selector.PodName) > 0 {
		selectors = append(selectors, &common.Selector{
			Type:  "k8s",
			Value: fmt.Sprintf("pod-name:%s", instance.Spec.Selector.PodName),
		})
	}
	if len(instance.Spec.Selector.PodUid) > 0 {
		selectors = append(selectors, &common.Selector{
			Type:  "k8s",
			Value: fmt.Sprintf("pod-uid:%s", instance.Spec.Selector.PodUid),
		})
	}
	if len(instance.Spec.Selector.Namespace) > 0 {
		selectors = append(selectors, &common.Selector{
			Type:  "k8s",
			Value: fmt.Sprintf("ns:%s", instance.Spec.Selector.Namespace),
		})
	}
	if len(instance.Spec.Selector.ServiceAccount) > 0 {
		selectors = append(selectors, &common.Selector{
			Type:  "k8s",
			Value: fmt.Sprintf("sa:%s", instance.Spec.Selector.ServiceAccount),
		})
	}
	if len(instance.Spec.Selector.ContainerName) > 0 {
		selectors = append(selectors, &common.Selector{
			Type:  "k8s",
			Value: fmt.Sprintf("container-name:%s", instance.Spec.Selector.ContainerName),
		})
	}
	if len(instance.Spec.Selector.ContainerImage) > 0 {
		selectors = append(selectors, &common.Selector{
			Type:  "k8s",
			Value: fmt.Sprintf("container-image:%s", instance.Spec.Selector.ContainerImage),
		})
	}
	for _, v := range instance.Spec.Selector.Arbitrary {
		selectors = append(selectors, &common.Selector{Type: "k8s", Value: v})
	}

	spiffeId := instance.Spec.SpiffeId

	createEntryIfNotExistsResponse, err := r.SpireClient.CreateEntryIfNotExists(ctx, &common.RegistrationEntry{
		Selectors: selectors,
		ParentId:  *r.myId,
		SpiffeId:  spiffeId,
	})
	if err != nil {
		reqLogger.Error(err, "Failed to create spire entry")
		return "", err
	}
	entryId := createEntryIfNotExistsResponse.Entry.EntryId
	if createEntryIfNotExistsResponse.Preexisting {
		reqLogger.Info("Found existing entry", "entryID", entryId, "spiffeID", spiffeId)
	} else {
		reqLogger.Info("Created entry", "entryID", entryId, "spiffeID", spiffeId)
	}

	return entryId, nil

}

// Helper functions to check and remove string from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

func (r *ClusterSpiffeIDReconciler) SetupWithManager(mgr ctrl.Manager) error {

	if err := mgr.GetFieldIndexer().IndexField(&spiffeidv1beta1.ClusterSpiffeID{}, spireEntryIdKey, func(rawObj runtime.Object) []string {
		// grab the job object, extract the owner...
		clusterSpiffeId := rawObj.(*spiffeidv1beta1.ClusterSpiffeID)
		if clusterSpiffeId.Status.EntryId == nil {
			return nil
		}
		return []string{*clusterSpiffeId.Status.EntryId}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&spiffeidv1beta1.ClusterSpiffeID{}).
		Complete(r)
}
