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

	"k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/medik8s/fence-agents-remediation/pkg/template"
	"github.com/medik8s/fence-agents-remediation/pkg/validation"
)

var (
	// webhookFARLog is for logging in this package.
	webhookFARLog = logf.Log.WithName("fenceagentsremediation-resource")
	// verify agent existence with os.Stat function
	agentValidator = validation.NewAgentValidator()
	// isOutOfServiceTaintSupported will be set to true in case out-of-service taint is supported (k8s 1.26 or higher)
	isOutOfServiceTaintSupported bool
)

func (r *FenceAgentsRemediation) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(&customValidator{
			Client: mgr.GetClient(),
		}).
		Complete()
}

func InitOutOfServiceTaintSupportedFlag(outOfServiceTaintSupported bool) {
	isOutOfServiceTaintSupported = outOfServiceTaintSupported
}

func validateAgentName(agent string) error {
	exists, err := agentValidator.ValidateAgentName(agent)
	if err != nil {
		return errors.NewAggregate([]error{
			fmt.Errorf("Failed to validate fence agent: %s. You might want to try again.", agent),
			err,
		})
	}
	if !exists {
		return fmt.Errorf("unsupported fence agent: %s", agent)
	}
	return nil
}

func validateStrategy(farRemStrategy RemediationStrategyType) error {
	if farRemStrategy == OutOfServiceTaintRemediationStrategy && !isOutOfServiceTaintSupported {
		return fmt.Errorf("%s remediation strategy is not supported at kubernetes version lower than 1.26, please use a different remediation strategy", OutOfServiceTaintRemediationStrategy)
	}
	return nil
}

// validateTemplateParameters validates template syntax in shared parameters and collects all errors
func validateTemplateParameters(spec *FenceAgentsRemediationSpec) error {
	var validationErrors []error

	// Validate template syntax in shared parameters
	for paramName, paramValue := range spec.SharedParameters {
		if _, err := template.RenderParameterTemplate(paramValue, "dummy-node-name"); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("invalid template syntax in shared parameter %s: %w", paramName, err))
		}
	}

	return errors.NewAggregate(validationErrors)
}
