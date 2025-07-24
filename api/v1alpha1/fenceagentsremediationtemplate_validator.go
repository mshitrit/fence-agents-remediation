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
	"errors"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilErrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/medik8s/fence-agents-remediation/pkg/executor"
)

const (
	errorParamDefinedMultipleTimes = "invalid multiple definition of FAR param"

	// Parameter validation constants shouldn't exceed 13 seconds ocp cap (https://docs.redhat.com/en/documentation/openshift_container_platform/4.19/html/architecture/admission-plug-ins)
	parameterValidationTimeout = 3 * time.Second
)

var (
	// webhookTemplateValidatorLog is for logging in this package.
	webhookTemplateValidatorLog = logf.Log.WithName("fenceagentsremediationtemplate-validator")
)

// +kubebuilder:webhook:path=/validate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediationtemplate,mutating=false,failurePolicy=fail,sideEffects=None,timeoutSeconds=13,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates,verbs=create;update,versions=v1alpha1,name=vfenceagentsremediationtemplate.kb.io,admissionReviewVersions=v1

type customValidator struct {
	client.Client
	commandExecutor executor.CommandExecutor
}

// ParameterValidationResult contains the results of parameter validation
type ParameterValidationResult struct {
	IsSuccessful bool
	Message      string
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
	paramWarnings, validateParamErr := v.validateFenceAgentTemplate(ctx, r)
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
	paramWarnings, validateParamErr := v.validateFenceAgentTemplate(ctx, r)
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

// validateFenceAgentTemplate validates fence agent parameters for templates
// by creating temporary FAR CRs and using BuildFenceAgentParams + validateParametersWithStatus
func (v *customValidator) validateFenceAgentTemplate(ctx context.Context, r *FenceAgentsRemediationTemplate) ([]string, error) {
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
		webhookTemplateValidatorLog.Info("validateFenceAgentTemplate return no params")
		return warnings, nil
	}

	// Collect all unique node names from NodeParameters and NodeSecretNames
	nodeNames := getNodeNamesFromSpec(spec)

	skipStatusValidation := false
	// If no node-specific parameters, validate with shared parameters only, use a dummy placeholder for node name
	if len(nodeNames) == 0 {
		webhookTemplateValidatorLog.Info("validateFenceAgentTemplate no nodes found")
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
			result := validateParametersWithStatus(spec.Agent, completeParams, v.commandExecutor)
			if !result.IsSuccessful {
				return warnings, fmt.Errorf("fence agent parameter validation failed: %s", result.Message)
			}
			// Check if successful but has a warning message
			if result.IsSuccessful && result.Message != "" {
				warning := fmt.Sprintf("fence agent parameter validation succeeded with warning for node %s: %s", nodeName, result.Message)
				warnings = append(warnings, warning)
				webhookTemplateValidatorLog.Info("validateFenceAgentTemplate warning", "node", nodeName, "warning", warning)
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

// validateFenceAgentParams validates all fence agent parameters without building the map
func validateFenceAgentParams(
	far *FenceAgentsRemediation,
	secretParams map[string]string,
	nodeName string,
) error {
	// Track parameter names for uniqueness validation
	existingParams := make(map[ParameterName]bool)

	// Extract parameters from FAR
	sharedParameters := far.Spec.SharedParameters
	nodeParameters := far.Spec.NodeParameters

	// Validate shared parameters
	for paramName, paramVal := range sharedParameters {
		// Verify action must be reboot
		if err := validateActionParameter(string(paramName), paramVal); err != nil {
			return err
		}
		// Verify param isn't already defined
		if existingParams[paramName] {
			err := errors.New(errorParamDefinedMultipleTimes)
			webhookTemplateValidatorLog.Error(err, "can't build fence agents params a param is defined multiple times", "param name", paramName)
			return err
		}
		existingParams[paramName] = true
	}

	// Validate node parameters
	for paramName, nodeMap := range nodeParameters {
		if nodeVal, isFound := nodeMap[NodeName(nodeName)]; isFound {
			// Verify action must be reboot
			if err := validateActionParameter(string(paramName), nodeVal); err != nil {
				return err
			}
			// For node params we don't enforce uniqueness as node param value will override shared param
			existingParams[paramName] = true
		}
	}

	// Validate secret parameters
	for secretKey, secretVal := range secretParams {
		secretParam := ParameterName(secretKey)
		// Verify action must be reboot
		if err := validateActionParameter(string(secretParam), secretVal); err != nil {
			return err
		}
		if existingParams[secretParam] {
			err := errors.New(errorParamDefinedMultipleTimes)
			webhookTemplateValidatorLog.Error(err, "can't build fence agents params a param is defined multiple times", "param name", secretParam)
			return err
		}
		existingParams[secretParam] = true
	}

	return nil
}

// validateParametersWithStatus validates fence agent parameters by running a status command
func validateParametersWithStatus(agent string, parameters map[ParameterName]string, exec executor.CommandExecutor) *ParameterValidationResult {
	result := &ParameterValidationResult{
		IsSuccessful: true,
		Message:      "",
	}

	// Build command with status action
	command := []string{agent, parameterActionName, parameterActionStatusValue}

	// Add parameters (excluding action parameters to avoid conflicts)
	for paramName, paramValue := range parameters {
		if string(paramName) != actionName && string(paramName) != parameterActionName {
			command = append(command, string(paramName), paramValue)
		}
	}

	// Run the status command with timeout
	ctx, cancel := context.WithTimeout(context.Background(), parameterValidationTimeout)
	defer cancel()

	webhookTemplateValidatorLog.Info("Testing fence agent status command", "agent", agent, "command", command)

	stdout, stderr, err := exec.RunCommand(ctx, command[0], command[1:]...)

	if err != nil {
		result.IsSuccessful = false
		if ctx.Err() == context.DeadlineExceeded {
			result.Message = fmt.Sprintf("status command timed out after %v", parameterValidationTimeout)
			webhookTemplateValidatorLog.Info("validateParametersWithStatus status command timed out", "result", result)
			return result
		}

		result.Message = fmt.Sprintf("fence agent command failed: %v (stderr: %s, stdout: %s)", err, stderr, stdout)
		webhookTemplateValidatorLog.Info("validateParametersWithStatus status command failed", "result", result)
		return result
	}

	// Command completed successfully, now check if stdout contains "Status: ON"
	if strings.Contains(stdout, "Status: ON") {
		webhookTemplateValidatorLog.Info("Fence agent status command succeeded with Status: ON", "agent", agent, "stdout", stdout)
	} else {
		result.Message = fmt.Sprintf("fence agent command completed but status is not ON (stdout: %s, stderr: %s)", stdout, stderr)
		webhookTemplateValidatorLog.Info("Fence agent status command completed but status not ON", "agent", agent, "stdout", stdout, "stderr", stderr)
	}

	return result
}

// validateActionParameter validates that action parameters are set correctly
func validateActionParameter(paramName, paramVal string) error {
	if (paramName == actionName || paramName == parameterActionName) && paramVal != "" && paramVal != parameterActionRebootValue {
		// --action parameter with a different value from reboot is not supported
		err := fmt.Errorf("FAR doesn't support any other action than reboot")
		webhookTemplateValidatorLog.Error(err, "can't build CR with this action attribute", "action", paramVal)
		return err
	}
	return nil
}
