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

const (
	// FARFinalizer is a finalizer for a FenceAgentsRemediation CR deletion
	FARFinalizer string = "fence-agents-remediation.medik8s.io/far-finalizer"
	// Taints
	FARNoExecuteTaintKey = "medik8s.io/fence-agents-remediation"
)

// ConditionsChangeReason represents the reason of updating the some or all the conditions
type ConditionsChangeReason string

const (
	// RemediationFinishedNodeNotFound - CR was found but its name doesn't matche a node
	RemediationFinishedNodeNotFound ConditionsChangeReason = "RemediationFinishedNodeNotFound"
	// RemediationInterruptedByNHC - Remediation was interrupted by NHC timeout annotation
	RemediationInterruptedByNHC ConditionsChangeReason = "RemediationInterruptedByNHC"
	// RemediationStarted - CR was found, its name matches a node, and a finalizer was set
	RemediationStarted ConditionsChangeReason = "RemediationStarted"
	// FenceAgentSucceeded - FAR taint was added, fence agent command has been created and executed successfully
	FenceAgentSucceeded ConditionsChangeReason = "FenceAgentSucceeded"
	// RemediationFinishedSuccessfully - The unhealthy node was fully remediated/fenced (it was tainted, fenced by FA and all of its resources have been deleted)
	RemediationFinishedSuccessfully ConditionsChangeReason = "RemediationFinishedSuccessfully"

	ResourceDeletionRemediationStrategy  = RemediationStrategyType("ResourceDeletion")
	OutOfServiceTaintRemediationStrategy = RemediationStrategyType("OutOfServiceTaint")
)

type ParameterName string
type NodeName string
type RemediationStrategyType string

// FenceAgentsRemediationSpec defines the desired state of FenceAgentsRemediation
type FenceAgentsRemediationSpec struct {
	// Agent is the name of fence agent that will be used.
	// It should have a fence_ prefix.
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern=fence_.+
	Agent string `json:"agent"`

	// RetryCount is the number of times the fencing agent will be executed
	// +kubebuilder:default:=5
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	RetryCount int `json:"retrycount,omitempty"`

	// RetryInterval is the interval between each fencing agent execution
	// +kubebuilder:default:="5s"
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	// +kubebuilder:validation:Type=string
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	RetryInterval metav1.Duration `json:"retryinterval,omitempty"`

	// Timeout is the timeout for each fencing agent execution
	// +kubebuilder:default:="60s"
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	// +kubebuilder:validation:Type=string
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	Timeout metav1.Duration `json:"timeout,omitempty"`

	// SharedParameters are parameters common to all nodes
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	SharedParameters map[ParameterName]string `json:"sharedparameters,omitempty"`

	// NodeParameters are passed to the fencing agent according to the node that is fenced, since they are node specific
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	NodeParameters map[ParameterName]map[NodeName]string `json:"nodeparameters,omitempty"`

	// RemediationStrategy is the remediation method for unhealthy nodes.
	// Currently, it could be either "OutOfServiceTaint" or "ResourceDeletion".
	// ResourceDeletion will iterate over all pods related to the unhealthy node and delete them.
	// OutOfServiceTaint will add the out-of-service taint which is a new well-known taint "node.kubernetes.io/out-of-service"
	// that enables automatic deletion of pv-attached pods on failed nodes, "out-of-service" taint is only supported on clusters with k8s version 1.26+ or OCP/OKD version 4.13+.
	// +kubebuilder:default:="ResourceDeletion"
	// +kubebuilder:validation:Enum=ResourceDeletion;OutOfServiceTaint
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	RemediationStrategy RemediationStrategyType `json:"remediationStrategy,omitempty"`

	// NodeSecretNames maps the node name to the Secret name which contains params relevant for that node.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	NodeSecretNames map[NodeName]string `json:"nodeSecrets,omitempty"`

	// SharedSecretName is the name of the Secret which will contain params needed for FAR in order to remediate any node.
	// Using this Secret is optional.
	// +optional
	// +kubebuilder:default:="fence-agents-credentials-shared"
	// +kubebuilder:validation:Type=string
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	SharedSecretName *string `json:"sharedSecretName,omitempty"`
}

// FenceAgentsRemediationStatus defines the observed state of FenceAgentsRemediation
type FenceAgentsRemediationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Represents the observations of a FenceAgentsRemediation's current state.
	// Known .status.conditions.type are: "Processing", "FenceAgentActionSucceeded", and "Succeeded".
	// +listType=map
	// +listMapKey=type
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,displayName="conditions",xDescriptors="urn:alm:descriptor:io.kubernetes.conditions"
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastUpdateTime is the last time the status was updated.
	//
	// +optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	// +operator-sdk:csv:customresourcedefinitions:type=status
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=far

// +operator-sdk:csv:customresourcedefinitions:resources={{"FenceAgentsRemediation","v1alpha1","fenceagentsremediations"}}
// FenceAgentsRemediation is the Schema for the fenceagentsremediations API
type FenceAgentsRemediation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FenceAgentsRemediationSpec   `json:"spec,omitempty"`
	Status FenceAgentsRemediationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// FenceAgentsRemediationList contains a list of FenceAgentsRemediation
type FenceAgentsRemediationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FenceAgentsRemediation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FenceAgentsRemediation{}, &FenceAgentsRemediationList{})
}
