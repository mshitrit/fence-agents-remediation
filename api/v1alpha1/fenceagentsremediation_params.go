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

	commonAnnotations "github.com/medik8s/common/pkg/annotations"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilErrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/medik8s/fence-agents-remediation/pkg/template"
)

const (
	parameterRebootActionValue     = "reboot"
	parameterOffActionValue        = "off"
	actionName                     = "action"
	parameterActionName            = "--" + actionName
	errorParamDefinedMultipleTimes = "invalid multiple definition of FAR parameter, parameter name: %s"
	errorMissingParams             = "invalid template: mandatory parameters are missing"
	ErrorUnsupportedAction         = "FAR doesn't support any other action than reboot or off"
)

var (
	// paramsLog is for logging in this package.
	paramsLog = logf.Log.WithName("fenceagentsremediation-params")
)

type SecretParams struct {
	params          map[string]string
	hasNodeTemplate bool
}

// +kubebuilder:webhook:path=/validate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediationtemplate,mutating=false,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates,verbs=create;update,versions=v1alpha1,name=vfenceagentsremediationtemplate.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediation,mutating=false,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediations,verbs=create;update,versions=v1alpha1,name=vfenceagentsremediation.kb.io,admissionReviewVersions=v1

type customValidator struct {
	client.Client
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validate(ctx, obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateUpdate(ctx context.Context, old runtime.Object, new runtime.Object) (admission.Warnings, error) {
	return v.validate(ctx, new)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	metaObj, _ := obj.(metav1.Object)
	paramsLog.Info("validate delete", "name", metaObj.GetName())
	return nil, nil
}

func (v *customValidator) validate(ctx context.Context, new runtime.Object) (admission.Warnings, error) {
	spec := v.getSpec(new)

	// Skipping validation because must be either a FenceAgentsRemediationTemplate or a FenceAgentsRemediation
	metaObj, _ := new.(metav1.Object)

	aggregated := utilErrors.NewAggregate([]error{
		validateAgentName(spec.Agent),
		validateStrategy(spec.RemediationStrategy),
		validateTemplateParameters(spec),
		validateFenceAgentForNodes(ctx, v.Client, metaObj.GetNamespace(), spec),
	})

	return admission.Warnings{}, aggregated
}

func (v *customValidator) getSpec(new runtime.Object) *FenceAgentsRemediationSpec {
	var spec FenceAgentsRemediationSpec
	if fart, isFart := new.(*FenceAgentsRemediationTemplate); isFart {
		spec = fart.Spec.Template.Spec
	} else {
		far, _ := new.(*FenceAgentsRemediation)
		spec = far.Spec
	}
	return &spec
}

// validateFenceAgentForNodes validates fence agent parameters for all the nodes defined in the spec
// by creating temporary FAR CRs and using BuildFenceAgentParams
func validateFenceAgentForNodes(ctx context.Context, k8sClient client.Client, namespace string, spec *FenceAgentsRemediationSpec) error {

	// Check if template has any parameters at all
	hasSharedParams := len(spec.SharedParameters) > 0
	hasNodeParams := len(spec.NodeParameters) > 0
	hasSecrets := spec.SharedSecretName != nil || spec.NodeSecretNames != nil

	// If template has no parameters or secrets, template is considered invalid
	if !hasSharedParams && !hasNodeParams && !hasSecrets {
		err := errors.New(errorMissingParams)
		paramsLog.Error(err, "Missing parameters")
		return err
	}

	// Collect all unique node names from NodeParameters and NodeSecretNames
	nodeNames := getNodeNamesFromSpec(spec)

	// If no node-specific parameters, validate with shared parameters only, use a dummy placeholder for node name
	if len(nodeNames) == 0 {
		paramsLog.Info("validateFenceAgentForNodes no nodes found")
		nodeNames = append(nodeNames, "temp-validation")
	}
	// Validate parameters for each node mentioned in NodeParameters
	for _, nodeName := range nodeNames {
		// Generate a temporary FAR CR from the template for this specific node
		tempFAR := &FenceAgentsRemediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodeName,
				Namespace: namespace,
			},
			Spec: *spec,
		}

		// BuildFenceAgentParams handles secret collection and validation internally
		_, _, err := BuildFenceAgentParams(ctx, k8sClient, tempFAR)
		if err != nil {
			// If BuildFenceAgentParams fails, return the validation error
			return err
		}
	}
	return nil
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
func validateFenceAgentParams(far *FenceAgentsRemediation, secretParams SecretParams) (map[ParameterName]string, error) {
	nodeName := GetNodeName(far)
	fenceAgentParams := make(map[ParameterName]string)

	// Track parameter names for uniqueness validation
	existingParams := make(map[ParameterName]bool)

	isNodeTemplateExistInSharedParams := false
	// Validate and add shared parameters
	for paramName, paramVal := range far.Spec.SharedParameters {
		// Verify action must be reboot
		if err := validateFenceAction(string(paramName), paramVal); err != nil {
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
			if err := validateFenceAction(string(paramName), nodeVal); err != nil {
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
	for secretKey, secretVal := range secretParams.params {
		secretParam := ParameterName(secretKey)
		// Verify action must be reboot
		if err := validateFenceAction(string(secretParam), secretVal); err != nil {
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

	onlySharedParamsWithoutTemplate := len(far.Spec.NodeParameters) == 0 && !isNodeTemplateExistInSharedParams && !secretParams.hasNodeTemplate
	if len(fenceAgentParams) == 0 || onlySharedParamsWithoutTemplate {
		err := errors.New(errorMissingParams)
		paramsLog.Error(err, "Missing parameters")
		return nil, err
	}

	return fenceAgentParams, nil
}

// validateFenceAction validates that action parameters are set correctly
func validateFenceAction(paramName, paramVal string) error {
	if (paramName == actionName || paramName == parameterActionName) &&
		(paramVal != "" && paramVal != parameterRebootActionValue && paramVal != parameterOffActionValue) {
		// --action parameter with a different value from reboot is not supported
		err := errors.New(ErrorUnsupportedAction)
		paramsLog.Error(err, "can't build CR with this action attribute", "action", paramVal)
		return err
	}
	return nil
}

// BuildFenceAgentParams collects the FAR's parameters for the node based on FAR CR, and if the CR is missing parameters
// or the CR's name don't match nodeParameter name, or it has an action which is different from reboot and off, then return an error
func BuildFenceAgentParams(ctx context.Context, k8sClient client.Client, far *FenceAgentsRemediation) (map[ParameterName]string, bool, error) {
	paramsLog.Info("BuildFenceAgentParams starting", "Node Name", far.Name)

	nodeName := GetNodeName(far)
	secretParams, err := collectRemediationSecretParams(ctx, k8sClient, far, nodeName)
	if err != nil {
		paramsLog.Error(err, "Failed collecting secrets data", "Node Name", nodeName, "CR Name", far.Name)
		return nil, true, err
	}

	// Build the parameters map with validation included
	fenceAgentParams, err := validateFenceAgentParams(far, secretParams)
	if err != nil {
		return nil, false, err
	}

	// Add the reboot action with its default value - https://github.com/ClusterLabs/fence-agents/blob/main/lib/fencing.py.py#L103
	if _, exist := fenceAgentParams[parameterActionName]; !exist {
		paramsLog.Info("`action` parameter is missing, so we add it with the default value of `reboot`")
		fenceAgentParams[parameterActionName] = parameterRebootActionValue
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
func collectRemediationSecretParams(ctx context.Context, k8sClient client.Client, far *FenceAgentsRemediation, nodeName string) (SecretParams, error) {
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
			return SecretParams{}, err
		}
	}
	isNodeTemplateExist := false
	// Templating secret shared parameters
	for paramName, paramVal := range sharedSecretParams {
		processedParamVal, err := template.RenderParameterTemplate(paramVal, nodeName)

		if err != nil {
			paramsLog.Error(err, "Failed to process template in shared secret parameter", "parameter", paramName)
			return SecretParams{secretParams, isNodeTemplateExist}, err
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
			return SecretParams{nil, isNodeTemplateExist}, err
		}
		// Apply node secret params, in case param exist both in shared and node, node param will override the shared.
		maps.Copy(secretParams, nodeSecretParams)
	}
	paramsLog.Info("collectRemediationSecretParams finish successfully for node", "node", nodeName)
	return SecretParams{secretParams, isNodeTemplateExist}, nil
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
