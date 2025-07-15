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
	"fmt"

	commonAnnotations "github.com/medik8s/common/pkg/annotations"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
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
)

func (r *FenceAgentsRemediationTemplate) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediationtemplate,mutating=true,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates,verbs=create;update,versions=v1alpha1,name=mfenceagentsremediationtemplate.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &FenceAgentsRemediationTemplate{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (farTemplate *FenceAgentsRemediationTemplate) Default() {
	webhookFARTemplateLog.Info("default", "name", farTemplate.Name)
	if farTemplate.GetAnnotations() == nil {
		farTemplate.Annotations = make(map[string]string)
	}
	if _, isSameKindAnnotationSet := farTemplate.GetAnnotations()[commonAnnotations.MultipleTemplatesSupportedAnnotation]; !isSameKindAnnotationSet {
		farTemplate.Annotations[commonAnnotations.MultipleTemplatesSupportedAnnotation] = "true"
	}
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// +kubebuilder:webhook:path=/validate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediationtemplate,mutating=false,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates,verbs=create;update,versions=v1alpha1,name=vfenceagentsremediationtemplate.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &FenceAgentsRemediationTemplate{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (farTemplate *FenceAgentsRemediationTemplate) ValidateCreate() (admission.Warnings, error) {
	webhookFARTemplateLog.Info("validate create", "name", farTemplate.Name)
	return validateFARTemplate(farTemplate)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (farTemplate *FenceAgentsRemediationTemplate) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	webhookFARTemplateLog.Info("validate update", "name", farTemplate.Name)
	return validateFARTemplate(farTemplate)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (farTemplate *FenceAgentsRemediationTemplate) ValidateDelete() (admission.Warnings, error) {
	webhookFARTemplateLog.Info("validate delete", "name", farTemplate.Name)
	return nil, nil
}

// validateFARTemplate performs comprehensive validation of the FenceAgentsRemediationTemplate
func validateFARTemplate(farTemplate *FenceAgentsRemediationTemplate) (admission.Warnings, error) {
	spec := &farTemplate.Spec.Template.Spec
	var warnings []string

	// First, run the existing FAR validation logic
	basicWarnings, basicErr := validateFAR(spec)

	// Convert basic warnings to string slice if any
	if len(basicWarnings) > 0 {
		for _, w := range basicWarnings {
			warnings = append(warnings, string(w))
		}
	}

	// Perform enhanced parameter validation
	paramValidationErrors := validateFenceAgentParameters(spec, &warnings)

	// Combine validation errors
	var allErrors []error
	if basicErr != nil {
		allErrors = append(allErrors, basicErr)
	}
	allErrors = append(allErrors, paramValidationErrors...)
	aggregated := errors.NewAggregate(allErrors)

	return admission.Warnings(warnings), aggregated
}

// validateFenceAgentParameters validates the fence agent parameters according to custom rules
// and optionally tests them with an actual status command
func validateFenceAgentParameters(spec *FenceAgentsRemediationSpec, warnings *[]string) []error {
	var validationErrors []error

	// Convert types for validation package
	sharedParams := make(map[string]string)
	for k, v := range spec.SharedParameters {
		sharedParams[string(k)] = v
	}

	nodeParams := make(map[string]map[string]string)
	for paramName, nodeMap := range spec.NodeParameters {
		nodeParams[string(paramName)] = make(map[string]string)
		for nodeName, paramValue := range nodeMap {
			nodeParams[string(paramName)][string(nodeName)] = paramValue
		}
	}

	// Validate parameter consistency using validation package
	consistencyErrors := validation.ValidateParameterConsistency(sharedParams, nodeParams)
	validationErrors = append(validationErrors, consistencyErrors...)

	// Validate action parameters using validation package
	for paramName, paramValue := range sharedParams {
		if err := validation.ValidateActionParameter(paramName, paramValue); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	for paramName, nodeMap := range nodeParams {
		for nodeName, paramValue := range nodeMap {
			if err := validation.ValidateActionParameter(paramName, paramValue); err != nil {
				// Create node-specific error message
				validationErrors = append(validationErrors, fmt.Errorf("action parameter '%s' for node '%s' must be 'reboot' or empty, got '%s'", paramName, nodeName, paramValue))
			}
		}
	}

	// Only test parameters with fence agent status command if there are no structural errors
	if len(validationErrors) == 0 && (len(sharedParams) > 0 || len(nodeParams) > 0) {
		result, err := parameterValidator.ValidateParametersWithStatus(spec.Agent, sharedParams)
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("Parameter validation error: %v", err))
		} else {
			// Add parameter validation errors as hard failures
			validationErrors = append(validationErrors, convertValidationErrors(result.Errors)...)
			// Add connectivity warnings only if no validation errors
			if len(result.Errors) == 0 {
				for _, warning := range result.Warnings {
					*warnings = append(*warnings, warning)
				}
			}
		}
	}

	// Add warning about node-specific parameters only if no validation errors
	if len(validationErrors) == 0 && len(nodeParams) > 0 {
		*warnings = append(*warnings, "Template contains node-specific parameters. "+
			"These will be validated when FenceAgentsRemediation instances are created for specific nodes.")
	}

	return validationErrors
}

// convertValidationErrors converts string errors to proper error types
func convertValidationErrors(errorStrings []string) []error {
	var errors []error
	for _, errStr := range errorStrings {
		errors = append(errors, fmt.Errorf("%s", errStr))
	}
	return errors
}
