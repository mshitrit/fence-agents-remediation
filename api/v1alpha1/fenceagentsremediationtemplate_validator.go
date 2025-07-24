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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilErrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/medik8s/fence-agents-remediation/pkg/validation"
)

var (
	// webhookTemplateValidatorLog is for logging in this package.
	webhookTemplateValidatorLog = logf.Log.WithName("fenceagentsremediationtemplate-validator")
	// parameterValidator for validating fence agent parameters
	parameterValidator = validation.NewFenceAgentParameterValidator()
)

type customValidator struct {
	client.Client
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	r := obj.(*FenceAgentsRemediationTemplate)
	webhookTemplateValidatorLog.Info("validate create", "name", r.Name)

	var allErrors []error
	var allWarnings []string

	// First, run the existing FAR validation logic
	validateWarnings, validateFarErr := validateFAR(&r.Spec.Template.Spec)
	if validateFarErr != nil {
		allErrors = append(allErrors, validateFarErr)
	}
	// Add validateFAR warnings
	allWarnings = append(allWarnings, validateWarnings...)

	// Perform enhanced parameter validation with secret collection
	paramWarnings, validateParamErr := v.validateFenceAgentParameters(ctx, r)
	if validateParamErr != nil {
		allErrors = append(allErrors, validateParamErr)
	}
	// Add parameter validation warnings
	allWarnings = append(allWarnings, paramWarnings...)

	return allWarnings, utilErrors.NewAggregate(allErrors)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateUpdate(ctx context.Context, old runtime.Object, new runtime.Object) (admission.Warnings, error) {
	r := new.(*FenceAgentsRemediationTemplate)
	webhookTemplateValidatorLog.Info("validate update", "name", r.Name)

	var allErrors []error
	var allWarnings []string

	// First, run the existing FAR validation logic
	validateWarnings, validateFarErr := validateFAR(&r.Spec.Template.Spec)
	if validateFarErr != nil {
		allErrors = append(allErrors, validateFarErr)
	}
	// Add validateFAR warnings
	allWarnings = append(allWarnings, validateWarnings...)

	// Perform enhanced parameter validation with secret collection
	paramWarnings, validateParamErr := v.validateFenceAgentParameters(ctx, r)
	if validateParamErr != nil {
		allErrors = append(allErrors, validateParamErr)
	}
	// Add parameter validation warnings
	allWarnings = append(allWarnings, paramWarnings...)

	return allWarnings, utilErrors.NewAggregate(allErrors)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	r := obj.(*FenceAgentsRemediationTemplate)
	webhookTemplateValidatorLog.Info("validate delete", "name", r.Name)
	return nil, nil
}

// validateFenceAgentParameters validates fence agent parameters for templates
// by creating temporary FAR CRs and using BuildFenceAgentParams + ValidateParametersWithStatus
func (v *customValidator) validateFenceAgentParameters(ctx context.Context, r *FenceAgentsRemediationTemplate) ([]string, error) {
	var warnings []string
	spec := &r.Spec.Template.Spec

	// Check if template has any parameters at all
	hasSharedParams := len(spec.SharedParameters) > 0
	hasNodeParams := len(spec.NodeParameters) > 0
	hasSecrets := spec.SharedSecretName != nil || spec.NodeSecretNames != nil

	// If template has no parameters or secrets, skip parameter validation
	// Templates are allowed to be empty - parameters can be added later
	//TODO mshitrit should we allow this ?
	if !hasSharedParams && !hasNodeParams && !hasSecrets {
		webhookTemplateValidatorLog.Info("validateFenceAgentParameters return no params")
		return warnings, nil
	}

	// Collect all unique node names from NodeParameters and NodeSecretNames
	nodeNames := getNodeNamesFromSpec(spec)

	skipStatusValidation := false
	// If no node-specific parameters, validate with shared parameters only, use a dummy placeholder for node name
	if len(nodeNames) == 0 {
		webhookTemplateValidatorLog.Info("validateFenceAgentParameters no nodes found")
		nodeNames["temp-validation"] = true
		// Status validation will NOT occur for shared params with a node template (because we want to avoid getting all the nodes from the API server)
		skipStatusValidation = true
	}
	// Validate parameters for each node mentioned in NodeParameters
	for nodeName := range nodeNames {
		// Create a temporary FAR CR from the template for this specific node
		tempFAR := &FenceAgentsRemediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodeName,
				Namespace: r.Namespace,
			},
			Spec: *spec,
		}

		// BuildFenceAgentParams handles secret collection and validation internally
		completeParams, _, err := BuildFenceAgentParams(ctx, v.Client, tempFAR)
		if err != nil {
			// If BuildFenceAgentParams fails, return the validation error
			return warnings, err
		}

		if !skipStatusValidation {
			// Validate the complete parameter set with status command
			result := parameterValidator.ValidateParametersWithStatus(spec.Agent, completeParams)
			if !result.IsSuccessful {
				return warnings, fmt.Errorf("fence agent parameter validation failed: %s", result.Message)
			}
			// Check if successful but has a warning message
			if result.IsSuccessful && result.Message != "" {
				warning := fmt.Sprintf("fence agent parameter validation succeeded with warning for node %s: %s", nodeName, result.Message)
				warnings = append(warnings, warning)
				webhookTemplateValidatorLog.Info("validateFenceAgentParameters warning", "node", nodeName, "warning", warning)
			}
		}
	}
	return warnings, nil
}

func getNodeNamesFromSpec(spec *FenceAgentsRemediationSpec) map[string]bool {
	nodeNames := make(map[string]bool)
	for _, nodeMap := range spec.NodeParameters {
		for nodeName := range nodeMap {
			nodeNames[string(nodeName)] = true
		}
	}
	for nodeName, _ := range spec.NodeSecretNames {
		nodeNames[string(nodeName)] = true
	}
	return nodeNames
}
