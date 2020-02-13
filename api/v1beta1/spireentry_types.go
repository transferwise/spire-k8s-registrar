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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type Selector struct {
	// Pod label names/values to match for this spiffe ID
	PodLabel map[string]string `json:"podLabel,omitempty"`
	// Pod names to match for this spiffe ID
	PodName string `json:"podName,omitempty"`
	// Pod UIDs to match for this spiffe ID
	PodUid types.UID `json:"podUid,omitempty"`
	// Namespace to match for this spiffe ID
	Namespace string `json:"namespace,omitempty"`
	// ServiceAccount to match for this spiffe ID
	ServiceAccount string `json:"serviceAccount,omitempty"`
	// Arbitrary selectors
	Arbitrary []string `json:"arbitrary,omitempty"`
}

// SpireEntrySpec defines the desired state of SpireEntry
type SpireEntrySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	SpiffeId string   `json:"spiffeId"`
	Selector Selector `json:"selector"`
}

// SpireEntryStatus defines the observed state of SpireEntry
type SpireEntryStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	EntryId *string `json:"entryId,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status

// SpireEntry is the Schema for the spireentries API
type SpireEntry struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SpireEntrySpec   `json:"spec,omitempty"`
	Status SpireEntryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SpireEntryList contains a list of SpireEntry
type SpireEntryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SpireEntry `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SpireEntry{}, &SpireEntryList{})
}
