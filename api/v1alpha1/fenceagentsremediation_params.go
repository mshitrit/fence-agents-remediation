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

	commonAnnotations "github.com/medik8s/common/pkg/annotations"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/medik8s/fence-agents-remediation/pkg/template"
	"github.com/medik8s/fence-agents-remediation/pkg/validation"
)

const (
	errorMissingParams = "nodeParameters or sharedParameters or both are missing, and they cannot be empty"
)

var (
	// paramsLog is for logging in this package.
	paramsLog = logf.Log.WithName("fenceagentsremediation-params")
)

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

// buildFenceAgentParamsMap builds the fence agent parameters map after validation has passed
func buildFenceAgentParamsMap(far *FenceAgentsRemediation, secretParams map[string]string) (map[validation.ParameterName]string, error) {
	nodeName := GetNodeName(far)
	fenceAgentParams := make(map[validation.ParameterName]string)

	// Add shared parameters
	for paramName, paramVal := range far.Spec.SharedParameters {
		processedParamVal, err := template.RenderParameterTemplate(paramVal, nodeName)
		if err != nil {
			paramsLog.Error(err, "Failed to process template in shared parameter", "parameter", paramName, "value", paramVal, "node", nodeName)
			return fenceAgentParams, err
		}
		fenceAgentParams[paramName] = processedParamVal
	}

	// Add node parameters (these can override shared parameters)
	for paramName, nodeMap := range far.Spec.NodeParameters {
		if nodeVal, isFound := nodeMap[validation.NodeName(nodeName)]; isFound {
			if _, exist := fenceAgentParams[paramName]; exist {
				paramsLog.Info("Shared parameter is overridden by node parameter", "parameter", paramName)
			}
			fenceAgentParams[paramName] = nodeVal
		} else {
			paramsLog.Info("Node parameter is missing for this node", "parameter name", paramName, "node name", nodeName)
		}
	}

	// Add secret parameters
	for secretKey, secretVal := range secretParams {
		secretParam := validation.ParameterName(secretKey)
		fenceAgentParams[secretParam] = secretVal
	}

	if len(fenceAgentParams) == 0 {
		err := errors.New(errorMissingParams)
		paramsLog.Error(err, "Missing parameters")
		return nil, err
	}

	return fenceAgentParams, nil
}

// BuildFenceAgentParams collects the FAR's parameters for the node based on FAR CR, and if the CR is missing parameters
// or the CR's name don't match nodeParameter name, or it has an action which is different from reboot, then return an error
func BuildFenceAgentParams(ctx context.Context, k8sClient client.Client, far *FenceAgentsRemediation) (map[validation.ParameterName]string, bool, error) {
	paramsLog.Info("BuildFenceAgentParams starting", "Node Name", far.Name)

	nodeName := GetNodeName(far)
	secretParams, err := validation.CollectRemediationSecretParams(
		ctx,
		k8sClient,
		far.Spec.SharedSecretName,
		far.Spec.NodeSecretNames,
		nodeName,
		far.Namespace,
	)
	if err != nil {
		paramsLog.Error(err, "Failed collecting secrets data", "Node Name", nodeName, "CR Name", far.Name)
		return nil, true, err
	}

	// First validate all parameters
	if err := validation.ValidateFenceAgentParams(far.Spec.SharedParameters, far.Spec.NodeParameters, secretParams, nodeName); err != nil {
		return nil, false, err
	}

	// If validation passes, build the parameters map
	fenceAgentParams, err := buildFenceAgentParamsMap(far, secretParams)
	if err != nil {
		return nil, true, err
	}

	// Add the reboot action with its default value - https://github.com/ClusterLabs/fence-agents/blob/main/lib/fencing.py.py#L103
	if _, exist := fenceAgentParams[validation.ParameterActionName]; !exist {
		paramsLog.Info("`action` parameter is missing, so we add it with the default value of `reboot`")
		fenceAgentParams[validation.ParameterActionName] = validation.ParameterActionRebootValue
	}

	paramsLog.Info("BuildFenceAgentParams finished successfully ", "Node Name", far.Name)
	return fenceAgentParams, false, nil
}
