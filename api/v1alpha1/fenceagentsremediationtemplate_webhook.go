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

	"k8s.io/apimachinery/pkg/runtime"
	utilErrors "k8s.io/apimachinery/pkg/util/errors"
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
)

type customValidator struct {
	client.Client
}

func (r *FenceAgentsRemediationTemplate) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(&customValidator{mgr.GetClient()}).
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

// +kubebuilder:webhook:path=/validate-fence-agents-remediation-medik8s-io-v1alpha1-fenceagentsremediationtemplate,mutating=false,failurePolicy=fail,sideEffects=None,groups=fence-agents-remediation.medik8s.io,resources=fenceagentsremediationtemplates,verbs=create;update,versions=v1alpha1,name=vfenceagentsremediationtemplate.kb.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	r := obj.(*FenceAgentsRemediationTemplate)
	webhookFARTemplateLog.Info("validate create", "name", r.Name)

	var allErrors []error
	// First, run the existing FAR validation logic
	validateWarnings, validateFarErr := validateFAR(&r.Spec.Template.Spec)
	if validateFarErr != nil {
		allErrors = append(allErrors, validateFarErr)
	}

	// Perform enhanced parameter validation with secret collection
	validateParamErr := v.validateFenceAgentParameters(ctx, r)
	if validateParamErr != nil {
		allErrors = append(allErrors, validateParamErr)
	}

	return validateWarnings, utilErrors.NewAggregate(allErrors)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateUpdate(ctx context.Context, old runtime.Object, new runtime.Object) (admission.Warnings, error) {
	r := new.(*FenceAgentsRemediationTemplate)
	webhookFARTemplateLog.Info("validate update", "name", r.Name)

	var allErrors []error
	// First, run the existing FAR validation logic
	validateWarnings, validateFarErr := validateFAR(&r.Spec.Template.Spec)
	if validateFarErr != nil {
		allErrors = append(allErrors, validateFarErr)
	}

	// Perform enhanced parameter validation with secret collection
	validateParamErr := v.validateFenceAgentParameters(ctx, r)
	if validateParamErr != nil {
		allErrors = append(allErrors, validateParamErr)
	}

	return validateWarnings, utilErrors.NewAggregate(allErrors)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (v *customValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	r := obj.(*FenceAgentsRemediationTemplate)
	webhookFARTemplateLog.Info("validate delete", "name", r.Name)
	return nil, nil
}

// validateFenceAgentParameters validates the fence agent parameters according to custom rules
// and optionally tests them with an actual status command
func (v *customValidator) validateFenceAgentParameters(ctx context.Context, r *FenceAgentsRemediationTemplate) error {
	spec := &r.Spec.Template.Spec

	// Collect shared secret parameters
	var sharedSecretParams map[string]string
	var err error

	if spec.SharedSecretName != nil {
		sharedSecretParams, err = validation.CollectRemediationSecretParams(
			ctx,
			v.Client,
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

			if spec.NodeSecretNames != nil {
				nodeSecretParams, err = validation.CollectRemediationSecretParams(
					ctx,
					v.Client,
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
	//TODO mshitrit shared params isn't enough
	_, err = parameterValidator.ValidateParametersWithStatus(spec.Agent, spec.SharedParameters)
	return err
}

const (
	errorMissingParams = "nodeParameters or sharedParameters or both are missing, and they cannot be empty"
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
func buildFenceAgentParamsMap(k8sClient client.Client, far *FenceAgentsRemediation, secretParams map[string]string) (map[validation.ParameterName]string, error) {
	nodeName := GetNodeName(far)
	fenceAgentParams := make(map[validation.ParameterName]string)

	// Add shared parameters
	for paramName, paramVal := range far.Spec.SharedParameters {
		fenceAgentParams[paramName] = paramVal
	}

	// Add node parameters (these can override shared parameters)
	for paramName, nodeMap := range far.Spec.NodeParameters {
		if nodeVal, isFound := nodeMap[validation.NodeName(nodeName)]; isFound {
			if _, exist := fenceAgentParams[paramName]; exist {
				webhookFARTemplateLog.Info("Shared parameter is overridden by node parameter", "parameter", paramName)
			}
			fenceAgentParams[paramName] = nodeVal
		} else {
			webhookFARTemplateLog.Info("Node parameter is missing for this node", "parameter name", paramName, "node name", nodeName)
		}
	}

	// Add secret parameters
	for secretKey, secretVal := range secretParams {
		secretParam := validation.ParameterName(secretKey)
		fenceAgentParams[secretParam] = secretVal
	}

	if len(fenceAgentParams) == 0 {
		err := errors.New(errorMissingParams)
		webhookFARTemplateLog.Error(err, "Missing parameters")
		return nil, err
	}

	return fenceAgentParams, nil
}

// BuildFenceAgentParams collects the FAR's parameters for the node based on FAR CR, and if the CR is missing parameters
// or the CR's name don't match nodeParameter name, or it has an action which is different from reboot, then return an error
func BuildFenceAgentParams(ctx context.Context, k8sClient client.Client, far *FenceAgentsRemediation) (map[validation.ParameterName]string, bool, error) {
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
		webhookFARTemplateLog.Error(err, "Failed collecting secrets data", "Node Name", nodeName, "CR Name", far.Name)
		return nil, true, err
	}

	// First validate all parameters
	if err := validation.ValidateFenceAgentParams(far.Spec.SharedParameters, far.Spec.NodeParameters, secretParams, nodeName); err != nil {
		return nil, false, err
	}

	// If validation passes, build the parameters map
	fenceAgentParams, err := buildFenceAgentParamsMap(k8sClient, far, secretParams)
	if err != nil {
		return nil, true, err
	}

	// Add the reboot action with its default value - https://github.com/ClusterLabs/fence-agents/blob/main/lib/fencing.py.py#L103
	if _, exist := fenceAgentParams[validation.ParameterActionName]; !exist {
		webhookFARTemplateLog.Info("`action` parameter is missing, so we add it with the default value of `reboot`")
		fenceAgentParams[validation.ParameterActionName] = validation.ParameterActionRebootValue
	}

	return fenceAgentParams, false, nil
}
