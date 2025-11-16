/*
Copyright 2025.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type WatchObjectSpec struct {
	// Image specifies the container image to use for the WebApp.
	// This will appear as spec.source.image

	// +required
	ApiVersion string `json:"apiVersion"`

	// +required
	Kind string `json:"kind"`

	// +required
	Name string `json:"nameRegex"`

	// +optional
	Namespace string `json:"namespaceRegex"`
}

// KrocSpec defines the desired state of Kroc
type KrocSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	WatchObject      WatchObjectSpec `json:"watchObject"`
	ResourceToCreate *string         `json:"resourceToCreate,omitempty"`
}

// KrocStatus defines the observed state of Kroc.
type KrocStatus struct {
	// +optional
	Phase string `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Kroc is the Schema for the krocs API
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.phase`
type Kroc struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Kroc
	// +required
	Spec KrocSpec `json:"spec"`

	// status defines the observed state of Kroc
	// +optional
	Status KrocStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// KrocList contains a list of Kroc
type KrocList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Kroc `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Kroc{}, &KrocList{})
}
