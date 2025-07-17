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
	"context"
	"fmt"

	commonAnnotations "github.com/medik8s/common/pkg/annotations"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/medik8s/fence-agents-remediation/pkg/validation"
)

var (
	// webhookTemplateLog is for logging in this package.
	webhookFARTemplateLog = logf.Log.WithName("fenceagentsremediationtemplate-resource")
	// parameterValidator for validating fence agent parameters
	parameterValidator = validation.NewFenceAgentParameterValidator()
	// webhookClient for accessing Kubernetes resources during validation
	webhookClient client.Client
)

func (r *FenceAgentsRemediationTemplate) SetupWebhookWithManager(mgr ctrl.Manager) error {
	// Store the client for use in validation
	webhookClient = mgr.GetClient()

	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediationtemplate,mutating=true,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates,verbs=create;update,versions=v1alpha1,name=mfenceagentsremediationtemplate.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &FenceAgentsRemediationTemplate{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *FenceAgentsRemediationTemplate) Default() {
	webhookFARTemplateLog.Info("default", "name", r.Name)
	if r.GetAnnotations() == nil {
		r.Annotations = make(map[string]string)
	}
	if _, isSameKindAnnotationSet := r.GetAnnotations()[commonAnnotations.MultipleTemplatesSupportedAnnotation]; !isSameKindAnnotationSet {
		r.Annotations[commonAnnotations.MultipleTemplatesSupportedAnnotation] = "true"
	}
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// +kubebuilder:webhook:path=/validate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediationtemplate,mutating=false,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates,verbs=create;update,versions=v1alpha1,name=vfenceagentsremediationtemplate.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &FenceAgentsRemediationTemplate{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *FenceAgentsRemediationTemplate) ValidateCreate() (admission.Warnings, error) {
	webhookFARTemplateLog.Info("validate create", "name", r.Name)
	return r.validateFARTemplate()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *FenceAgentsRemediationTemplate) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	webhookFARTemplateLog.Info("validate update", "name", r.Name)
	return r.validateFARTemplate()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *FenceAgentsRemediationTemplate) ValidateDelete() (admission.Warnings, error) {
	webhookFARTemplateLog.Info("validate delete", "name", r.Name)
	return nil, nil
}

// validateFARTemplate performs comprehensive validation of the FenceAgentsRemediationTemplate
func (r *FenceAgentsRemediationTemplate) validateFARTemplate() (admission.Warnings, error) {
	spec := &r.Spec.Template.Spec
	var warnings []string

	// First, run the existing FAR validation logic
	basicWarnings, basicErr := validateFAR(spec)

	// Convert basic warnings to string slice if any
	if len(basicWarnings) > 0 {
		for _, w := range basicWarnings {
			warnings = append(warnings, string(w))
		}
	}
	//TODO mshitrit simplify the validateFARTemplate > validateFenceAgentParameters > ValidateFenceAgentParams chain

	// Perform enhanced parameter validation
	paramValidationErrors := r.validateFenceAgentParameters()

	// Combine validation errors
	var allErrors []error
	if basicErr != nil {
		allErrors = append(allErrors, basicErr)
	}
	allErrors = append(allErrors, paramValidationErrors)
	aggregated := errors.NewAggregate(allErrors)

	return warnings, aggregated
}

// validateFenceAgentParameters validates the fence agent parameters according to custom rules
// and optionally tests them with an actual status command
func (r *FenceAgentsRemediationTemplate) validateFenceAgentParameters() error {
	spec := &r.Spec.Template.Spec
	ctx := context.TODO()

	// Collect shared secret parameters
	var sharedSecretParams map[string]string
	var err error

	if spec.SharedSecretName != nil && webhookClient != nil {
		sharedSecretParams, err = validation.CollectRemediationSecretParams(
			ctx,
			webhookClient,
			spec.SharedSecretName,
			spec.NodeSecretNames,
			"", // empty node name for shared secrets only
			r.Namespace,
		)
		if err != nil {
			webhookFARTemplateLog.Info("Failed to collect shared secret params, using empty params", "error", err)
			sharedSecretParams = make(map[string]string)
		}
	} else {
		sharedSecretParams = make(map[string]string)
	}

	// Collect all unique node names from NodeParameters
	nodeNames := make(map[string]bool)
	for _, nodeMap := range spec.NodeParameters {
		for nodeName := range nodeMap {
			nodeNames[string(nodeName)] = true
		}
	}

	// If no node-specific parameters, validate with empty node name (for shared parameters only)
	if len(nodeNames) == 0 {
		if err := validation.ValidateFenceAgentParams(spec.SharedParameters, spec.NodeParameters, sharedSecretParams, ""); err != nil {
			return err
		}
	} else {
		// Validate parameters for each node mentioned in NodeParameters
		for nodeName := range nodeNames {
			// Collect node-specific secret parameters
			var nodeSecretParams map[string]string

			if spec.NodeSecretNames != nil && webhookClient != nil {
				nodeSecretParams, err = validation.CollectRemediationSecretParams(
					ctx,
					webhookClient,
					spec.SharedSecretName,
					spec.NodeSecretNames,
					nodeName,
					r.Namespace,
				)
				if err != nil {
					webhookFARTemplateLog.Info("Failed to collect secret params for node, using empty params", "node", nodeName, "error", err)
					nodeSecretParams = make(map[string]string)
				}
			} else {
				nodeSecretParams = make(map[string]string)
			}

			if err := validation.ValidateFenceAgentParams(spec.SharedParameters, spec.NodeParameters, nodeSecretParams, nodeName); err != nil {
				return err
			}
		}
	}

	_, err = parameterValidator.ValidateParametersWithStatus(spec.Agent, spec.SharedParameters)
	return err
}

// convertValidationErrors converts string errors to proper error types
func convertValidationErrors(errorStrings []string) []error {
	var errors []error
	for _, errStr := range errorStrings {
		errors = append(errors, fmt.Errorf("%s", errStr))
	}
	return errors
}
