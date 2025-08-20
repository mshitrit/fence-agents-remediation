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
	commonAnnotations "github.com/medik8s/common/pkg/annotations"

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var (
	// webhookFARTemplateLog is for logging in this package.
	webhookFARTemplateLog = logf.Log.WithName("fenceagentsremediationtemplate-resource")
)

func (farTemplate *FenceAgentsRemediationTemplate) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(farTemplate).
		WithValidator(&customValidator{
			Client: mgr.GetClient(),
		}).
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
