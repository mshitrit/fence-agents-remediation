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
	"maps"

	commonAnnotations "github.com/medik8s/common/pkg/annotations"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/medik8s/fence-agents-remediation/pkg/template"
)

const (
	parameterActionRebootValue = "reboot"
	actionName                 = "action"
	parameterActionName        = "--" + actionName
	parameterActionStatusValue = "status"
)

var (
	// paramsLog is for logging in this package.
	paramsLog = logf.Log.WithName("fenceagentsremediation-params")
)

// BuildFenceAgentParams collects the FAR's parameters for the node based on FAR CR, and if the CR is missing parameters
// or the CR's name don't match nodeParameter name, or it has an action which is different from reboot, then return an error
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
func collectRemediationSecretParams(
	ctx context.Context,
	k8sClient client.Client,
	far *FenceAgentsRemediation,
	nodeName string,
) (map[string]string, error) {
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
			return nil, err
		}
	}

	// Templating secret shared parameters
	for paramName, paramVal := range sharedSecretParams {
		processedParamVal, err := template.RenderParameterTemplate(paramVal, nodeName)
		if err != nil {
			paramsLog.Error(err, "Failed to process template in shared secret parameter", "parameter", paramName)
			return secretParams, err
		}
		secretParams[paramName] = processedParamVal
	}

	// collect secret params from the node's secret
	nodeSecretName, isFound := nodeSecretNames[NodeName(nodeName)]
	var nodeSecretParams map[string]string
	if isFound {
		nodeSecretParams, err = collectSecretParams(ctx, k8sClient, nodeSecretName, namespace, false) // false = isSharedSecret
		if err != nil {
			return nil, err
		}
		// Apply node secret params, in case param exist both in shared and node, node param will override the shared.
		maps.Copy(secretParams, nodeSecretParams)
	}
	paramsLog.Info("collectRemediationSecretParams finish successfully for node", "node", nodeName)
	return secretParams, nil
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
