/*
Copyright 2022.

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

type FenceAgentsRemediationTemplateResource struct {
	Spec FenceAgentsRemediationSpec `json:"spec"`
}

// FenceAgentsRemediationTemplateSpec defines the desired state of FenceAgentsRemediationTemplate
type FenceAgentsRemediationTemplateSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Template defines the desired state of FenceAgentsRemediationTemplate
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	Template FenceAgentsRemediationTemplateResource `json:"template"`
}

// FenceAgentsRemediationTemplateStatus defines the observed state of FenceAgentsRemediationTemplate
type FenceAgentsRemediationTemplateStatus struct {
	// Represents the observations of a FenceAgentsRemediationTemplate's current state.
	// Known .status.conditions.type: "ParametersValidation".
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ValidationFailures maps node name to the validation failure message.
	// +optional
	ValidationFailures map[string]string `json:"validationFailures,omitempty"`

	// ValidationPassed marks nodes that have passed validation in the current round.
	// +optional
	ValidationPassed map[string]string `json:"validationPassed,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
//+kubebuilder:resource:shortName=fartemplate

// FenceAgentsRemediationTemplate is the Schema for the fenceagentsremediationtemplates API
// +operator-sdk:csv:customresourcedefinitions:resources={{"FenceAgentsRemediationTemplate","v1alpha1","fenceagentsremediationtemplates"}}
type FenceAgentsRemediationTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FenceAgentsRemediationTemplateSpec   `json:"spec,omitempty"`
	Status FenceAgentsRemediationTemplateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FenceAgentsRemediationTemplateList contains a list of FenceAgentsRemediationTemplate
type FenceAgentsRemediationTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FenceAgentsRemediationTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FenceAgentsRemediationTemplate{}, &FenceAgentsRemediationTemplateList{})
}
