/*
Copyright 2023.

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

// spec:
//	size: 1
//  containerPort: 80
//   nginx:
//     image: nginx:latest
//     command: nginx
//   git:
//     image: git-alpine:latest
//     branch: master

// StaticSpec defines the desired state of Static
type StaticSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// !!! Important: Run "make" to regenerate code after modifying this file !!!
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	Repository string `json:"repository,omitempty"`
	Branch     string `json:"branch,omitempty"`
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=300
	// +kubebuilder:validation:ExclusiveMaximum=false

	// Size define the number of Webpage instances
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	Wait int `json:"wait,omitempty"`
	// Port defines the port that will be used to init the container with the image
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	//ContainerPort   int32 `json:"containerPort,omitempty"`
	Ingress StaticSpecIngress `json:"ingress,omitempty"`
}
type StaticSpecIngress struct {
	Annotations map[string]string `json:"annotation,omitempty"`
	Host        []string          `json:"host,omitempty"`
	Tls         bool              `json:"tls,omitempty"`
}

// StaticStatus defines the observed state of Static
type StaticStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Represents the observations of a Webpage's current state.
	// Webpage.status.conditions.type are: "Available", "Progressing", and "Degraded"
	// Webpage.status.conditions.status are one of True, False, Unknown.
	// Webpage.status.conditions.reason the value should be a CamelCase string and producers of specific
	// condition types may define expected values and meanings for this field, and whether the values
	// are considered a guaranteed API.
	// Webpage.status.conditions.Message is a human-readable message indicating details about the transition.
	// For further information see: https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// Conditions store the status conditions of the Webpage instances
	// +operator-sdk:csv:customresourcedefinitions:type=status
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Static is the Schema for the statics API
type Static struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StaticSpec   `json:"spec,omitempty"`
	Status StaticStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// StaticList contains a list of Static
type StaticList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Static `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Static{}, &StaticList{})
}
