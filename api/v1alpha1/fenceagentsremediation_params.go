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
	"maps"
	"strings"
	"time"

	commonAnnotations "github.com/medik8s/common/pkg/annotations"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilErrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/medik8s/fence-agents-remediation/pkg/executor"
	"github.com/medik8s/fence-agents-remediation/pkg/template"
)

const (
	parameterActionRebootValue     = "reboot"
	actionName                     = "action"
	parameterActionName            = "--" + actionName
	parameterActionStatusValue     = "status"
	errorParamDefinedMultipleTimes = "invalid multiple definition of FAR parameter, parameter name: %s"
	errorMissingParams             = "nodeParameters or sharedParameters or both are missing, and they cannot be empty"
	// statusValidationTimeout is the maximum time allowed for a single status validation before it would time out.
	// Overall time for all the validations shouldn't exceed the 13 seconds ocp cap (https://docs.redhat.com/en/documentation/openshift_container_platform/4.19/html/architecture/admission-plug-ins)
	statusValidationTimeout = 3 * time.Second
)

var (
	// paramsLog is for logging in this package.
	paramsLog = logf.Log.WithName("fenceagentsremediation-params")
)

// Extending the default 10 sec timeout to 13 per ocp cap because we are running multiple status validation and want to take advantage of the maximum possible time (https://docs.redhat.com/en/documentation/openshift_container_platform/4.19/html/architecture/admission-plug-ins)
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
	return v.validate(ctx, obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateUpdate(ctx context.Context, old runtime.Object, new runtime.Object) (admission.Warnings, error) {
	return v.validate(ctx, new)
}

func (v *customValidator) validate(ctx context.Context, new runtime.Object) (admission.Warnings, error) {
	r := new.(*FenceAgentsRemediationTemplate)
	paramsLog.Info("validate update", "name", r.Name)

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
	warnings, err := v.validateFenceAgentTemplate(ctx, r)
	if err != nil {
		allErrors = append(allErrors, err)
	}
	// Add parameter validation warnings
	allWarnings = append(allWarnings, warnings...)

	return allWarnings, utilErrors.NewAggregate(allErrors)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	r := obj.(*FenceAgentsRemediationTemplate)
	paramsLog.Info("validate delete", "name", r.Name)
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

	// If template has no parameters or secrets, template is considered invalid
	if !hasSharedParams && !hasNodeParams && !hasSecrets {
		err := errors.New(errorMissingParams)
		paramsLog.Error(err, "Missing parameters")
		return nil, err
	}

	// Collect all unique node names from NodeParameters and NodeSecretNames
	nodeNames := getNodeNamesFromSpec(spec)

	skipStatusValidation := false
	// If no node-specific parameters, validate with shared parameters only, use a dummy placeholder for node name
	if len(nodeNames) == 0 {
		paramsLog.Info("validateFenceAgentTemplate no nodes found")
		nodeNames = append(nodeNames, "temp-validation")
		// Status validation will NOT occur for shared params with a node template (because we want to avoid getting all the nodes from the API server)
		skipStatusValidation = true
	}
	// Validate parameters for each node mentioned in NodeParameters
	for _, nodeName := range nodeNames {
		// Generate a temporary FAR CR from the template for this specific node
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
			result := validateParametersWithStatus(ctx, spec.Agent, completeParams, v.commandExecutor)
			if !result.IsSuccessful {
				return warnings, fmt.Errorf("fence agent parameter validation failed: %s", result.Message)
			}
			// Check if successful but has a warning message
			if result.IsSuccessful && result.Message != "" {
				warning := fmt.Sprintf("fence agent parameter validation succeeded with warning for node %s: %s", nodeName, result.Message)
				warnings = append(warnings, warning)
				paramsLog.Info("validateFenceAgentTemplate warning", "node", nodeName, "warning", warning)
			}
		}
	}
	return warnings, nil
}

func getNodeNamesFromSpec(spec *FenceAgentsRemediationSpec) []string {
	nodeNamesMap := make(map[string]bool)
	for _, nodeMap := range spec.NodeParameters {
		for nodeName := range nodeMap {
			nodeNamesMap[string(nodeName)] = true
		}
	}
	for nodeName := range spec.NodeSecretNames {
		nodeNamesMap[string(nodeName)] = true
	}

	var nodeNames []string
	for nodeName := range nodeNamesMap {
		nodeNames = append(nodeNames, nodeName)
	}

	return nodeNames
}

// validateFenceAgentParams builds the fence agent parameters map with validation
func validateFenceAgentParams(far *FenceAgentsRemediation, isNodeTemplateExistInSecretParams bool, secretParams map[string]string) (map[ParameterName]string, error) {
	nodeName := GetNodeName(far)
	fenceAgentParams := make(map[ParameterName]string)

	// Track parameter names for uniqueness validation
	existingParams := make(map[ParameterName]bool)

	isNodeTemplateExistInSharedParams := false
	// Validate and add shared parameters
	for paramName, paramVal := range far.Spec.SharedParameters {
		// Verify action must be reboot
		if err := validateActionParameter(string(paramName), paramVal); err != nil {
			return nil, err
		}
		// Verify param isn't already defined
		if existingParams[paramName] {
			err := fmt.Errorf(errorParamDefinedMultipleTimes, paramName)
			paramsLog.Error(err, "can't build fence agents parameters a parameter is defined multiple times", "parameter name", paramName)
			return nil, err
		}
		existingParams[paramName] = true

		processedParamVal, err := template.RenderParameterTemplate(paramVal, nodeName)
		if err != nil {
			paramsLog.Error(err, "Failed to process template in shared parameter", "parameter", paramName, "value", paramVal, "node", nodeName)
			return fenceAgentParams, err
		}
		isNodeTemplateExistInSharedParams = isNodeTemplateExistInSharedParams || paramVal != processedParamVal
		fenceAgentParams[paramName] = processedParamVal
	}

	// Validate and add node parameters (these can override shared parameters)
	for paramName, nodeMap := range far.Spec.NodeParameters {
		if nodeVal, isFound := nodeMap[NodeName(nodeName)]; isFound {
			// Verify action must be reboot
			if err := validateActionParameter(string(paramName), nodeVal); err != nil {
				return nil, err
			}
			// For node params we don't enforce uniqueness as node param value will override shared param
			existingParams[paramName] = true

			if _, exist := fenceAgentParams[paramName]; exist {
				paramsLog.Info("Shared parameter is overridden by node parameter", "parameter", paramName)
			}
			fenceAgentParams[paramName] = nodeVal
		} else {
			paramsLog.Info("Node parameter is missing for this node", "parameter name", paramName, "node name", nodeName)
		}
	}

	// Validate and add secret parameters
	for secretKey, secretVal := range secretParams {
		secretParam := ParameterName(secretKey)
		// Verify action must be reboot
		if err := validateActionParameter(string(secretParam), secretVal); err != nil {
			return nil, err
		}
		if existingParams[secretParam] {
			err := fmt.Errorf(errorParamDefinedMultipleTimes, secretParam)
			paramsLog.Error(err, "can't build fence agents parameters a parameter is defined multiple times", "parameter name", secretParam)
			return nil, err
		}
		existingParams[secretParam] = true
		fenceAgentParams[secretParam] = secretVal
	}

	onlySharedParamsWithoutTemplate := len(far.Spec.NodeParameters) == 0 && !isNodeTemplateExistInSharedParams && !isNodeTemplateExistInSecretParams
	if len(fenceAgentParams) == 0 || onlySharedParamsWithoutTemplate {
		err := errors.New(errorMissingParams)
		paramsLog.Error(err, "Missing parameters")
		return nil, err
	}

	return fenceAgentParams, nil
}

// validateParametersWithStatus validates fence agent parameters by running a status command
func validateParametersWithStatus(ctx context.Context, agent string, parameters map[ParameterName]string, exec executor.CommandExecutor) *ParameterValidationResult {
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
	ctxWithTimeout, cancel := context.WithTimeout(ctx, statusValidationTimeout)
	defer cancel()

	paramsLog.Info("Testing fence agent status command", "agent", agent, "command", command)

	stdout, stderr, err := exec.RunCommand(ctxWithTimeout, command[0], command[1:]...)

	if err != nil {
		result.IsSuccessful = false
		if errors.Is(ctxWithTimeout.Err(), context.DeadlineExceeded) {
			result.Message = fmt.Sprintf("status command timed out after %v", statusValidationTimeout)
			paramsLog.Info("validateParametersWithStatus status command timed out", "result", result)
			return result
		}

		result.Message = fmt.Sprintf("fence agent command failed: %v (stderr: %s, stdout: %s)", err, stderr, stdout)
		paramsLog.Info("validateParametersWithStatus status command failed", "result", result)
		return result
	}

	// Command completed successfully, now check if stdout contains "Status: ON"
	if strings.Contains(stdout, "Status: ON") {
		paramsLog.Info("Fence agent status command succeeded with Status: ON", "agent", agent, "stdout", stdout)
	} else {
		result.Message = fmt.Sprintf("fence agent command completed but status is not ON (stdout: %s, stderr: %s)", stdout, stderr)
		paramsLog.Info("Fence agent status command completed but status not ON", "agent", agent, "stdout", stdout, "stderr", stderr)
	}

	return result
}

// validateActionParameter validates that action parameters are set correctly
func validateActionParameter(paramName, paramVal string) error {
	if (paramName == actionName || paramName == parameterActionName) && paramVal != "" && paramVal != parameterActionRebootValue {
		// --action parameter with a different value from reboot is not supported
		err := fmt.Errorf("FAR doesn't support any other action than reboot")
		paramsLog.Error(err, "can't build CR with this action attribute", "action", paramVal)
		return err
	}
	return nil
}

// BuildFenceAgentParams collects the FAR's parameters for the node based on FAR CR, and if the CR is missing parameters
// or the CR's name don't match nodeParameter name, or it has an action which is different from reboot, then return an error
func BuildFenceAgentParams(ctx context.Context, k8sClient client.Client, far *FenceAgentsRemediation) (map[ParameterName]string, bool, error) {
	paramsLog.Info("BuildFenceAgentParams starting", "Node Name", far.Name)

	nodeName := GetNodeName(far)
	secretParams, isNodeTemplateExistInSecretParams, err := collectRemediationSecretParams(ctx, k8sClient, far, nodeName)
	if err != nil {
		paramsLog.Error(err, "Failed collecting secrets data", "Node Name", nodeName, "CR Name", far.Name)
		return nil, true, err
	}

	// Build the parameters map with validation included
	fenceAgentParams, err := validateFenceAgentParams(far, isNodeTemplateExistInSecretParams, secretParams)
	if err != nil {
		return nil, false, err
	}

	// Add the reboot action with its default value - https://github.com/ClusterLabs/fence-agents/blob/main/lib/fencing.py.py#L103
	if _, exist := fenceAgentParams[parameterActionName]; !exist {
		paramsLog.Info("`action` parameter is missing, so we add it with the default value of `reboot`")
		fenceAgentParams[parameterActionName] = parameterActionRebootValue
	}

	paramsLog.Info("BuildFenceAgentParams finished successfully ", "Node Name", far.Name)
	return fenceAgentParams, false, nil
}

// GetNodeName checks for the node name in far's commonAnnotations.NodeNameAnnotation if it does not exist it assumes the node name equals to far CR's name and return it.
func GetNodeName(far *FenceAgentsRemediation) string {
	ann := far.GetAnnotations()
	if ann == nil {
		return far.GetName()
	}
	if nodeName, isNodeNameAnnotationExist := ann[commonAnnotations.NodeNameAnnotation]; isNodeNameAnnotationExist {
		return nodeName
	}
	return far.GetName()
}

// collectRemediationSecretParams collects the parameters from the shared secret and the node secret
func collectRemediationSecretParams(ctx context.Context, k8sClient client.Client, far *FenceAgentsRemediation, nodeName string) (map[string]string, bool, error) {
	paramsLog.Info("collectRemediationSecretParams start for node", "node", nodeName)
	secretParams := map[string]string{}
	var sharedSecretParams map[string]string
	var err error

	// Extract secret names and namespace from FAR
	sharedSecretName := far.Spec.SharedSecretName
	nodeSecretNames := far.Spec.NodeSecretNames
	namespace := far.Namespace

	// collect secret params from shared secret
	if sharedSecretName != nil {
		sharedSecretParams, err = collectSecretParams(ctx, k8sClient, *sharedSecretName, namespace, true) // true = isSharedSecret
		if err != nil {
			return nil, false, err
		}
	}
	isNodeTemplateExist := false
	// Templating secret shared parameters
	for paramName, paramVal := range sharedSecretParams {
		processedParamVal, err := template.RenderParameterTemplate(paramVal, nodeName)

		if err != nil {
			paramsLog.Error(err, "Failed to process template in shared secret parameter", "parameter", paramName)
			return secretParams, isNodeTemplateExist, err
		}
		isNodeTemplateExist = isNodeTemplateExist || processedParamVal != paramVal
		secretParams[paramName] = processedParamVal
	}

	// collect secret params from the node's secret
	nodeSecretName, isFound := nodeSecretNames[NodeName(nodeName)]
	var nodeSecretParams map[string]string
	if isFound {
		nodeSecretParams, err = collectSecretParams(ctx, k8sClient, nodeSecretName, namespace, false) // false = isSharedSecret
		if err != nil {
			return nil, isNodeTemplateExist, err
		}
		// Apply node secret params, in case param exist both in shared and node, node param will override the shared.
		maps.Copy(secretParams, nodeSecretParams)
	}
	paramsLog.Info("collectRemediationSecretParams finish successfully for node", "node", nodeName)
	return secretParams, isNodeTemplateExist, nil
}

// collectSecretParams reads and adds the secret params if they are available
// For shared secrets, IsNotFound errors are ignored (returns empty map)
// For node secrets, IsNotFound errors are returned as errors
func collectSecretParams(
	ctx context.Context,
	k8sClient client.Client,
	secretName, namespace string,
	isSharedSecret bool,
) (map[string]string, error) {
	secretParams := make(map[string]string)

	// Get the secret directly (inlined from getSecret)
	secret := &corev1.Secret{}
	secretKeyObj := client.ObjectKey{Name: secretName, Namespace: namespace}

	if err := k8sClient.Get(ctx, secretKeyObj, secret); err != nil {
		if apiErrors.IsNotFound(err) {
			if isSharedSecret {
				// For shared secrets, IsNotFound is OK - return empty params
				paramsLog.Info("shared secret not found, continuing with empty params", "secret name", secretName, "namespace", namespace)
				return secretParams, nil
			}
			// For node secrets, IsNotFound is an error
			paramsLog.Error(err, "node secret not found", "secret name", secretName, "namespace", namespace)
			return nil, fmt.Errorf("node secret '%s' not found in namespace '%s': %w", secretName, namespace, err)

		}
		// For any other error, always return it
		paramsLog.Error(err, "failed to get secret", "secret name", secretName, "namespace", namespace)
		return nil, fmt.Errorf("failed to get secret '%s' in namespace '%s': %w", secretName, namespace, err)
	}

	// fill secret params from secret
	for secretKey, secretVal := range secret.Data {
		secretParams[secretKey] = string(secretVal)
		paramsLog.Info("found a value from secret", "secret name", secretName, "parameter name", secretKey)
	}

	return secretParams, nil
}
