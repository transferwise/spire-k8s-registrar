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
	"github.com/go-logr/logr"
	"github.com/spiffe/spire/proto/spire/api/registration"
	"github.com/spiffe/spire/proto/spire/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

// NodeReconciler reconciles a Node object
type NodeReconciler struct {
	MyId        string
	SpireClient registration.RegistrationClient
}

type NodeSelectorSubType string

const (
	NodeNameSelector NodeSelectorSubType = "agent_node_name"
)

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

func (r *NodeReconciler) makeSpiffeId(obj ObjectWithMetadata) string {
	return makeSpiffeIdForNodeName(r.MyId, obj.GetName())
}

func (r *NodeReconciler) makeParentId(_ ObjectWithMetadata) string {
	return r.MyId
}

func (r *NodeReconciler) getSelectors(namespacedName types.NamespacedName) []*common.Selector {
	return []*common.Selector{
		r.k8sNodeSelector(NodeNameSelector, namespacedName.Name),
	}
}

func (r *NodeReconciler) getAllEntries(ctx context.Context) ([]*common.RegistrationEntry, error) {
	entries, err := r.SpireClient.ListByParentID(ctx, &registration.ParentID{Id: r.MyId})
	if err != nil {
		return nil, err
	}
	return entries.Entries, nil
}

func (r *NodeReconciler) getObject() ObjectWithMetadata {
	return &corev1.Node{}
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

func NewNodeReconciler(client client.Client, log logr.Logger, scheme *runtime.Scheme, trustDomain string, myId string, spireClient registration.RegistrationClient) *BaseReconciler {
	return &BaseReconciler{
		Client:      client,
		Scheme:      scheme,
		TrustDomain: trustDomain,
		MyId:        myId,
		SpireClient: spireClient,
		Log:         log,
		ObjectReconcilier: &NodeReconciler{
			MyId:        myId,
			SpireClient: spireClient,
		},
	}
}
