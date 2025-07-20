package v1alpha1

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/fence-agents-remediation/pkg/validation"
)

// mockClient for testing
type mockClient struct {
	client.Client
}

var _ = Describe("FenceAgentsRemediationTemplate validation", func() {
	var mockValidatorClient = &mockClient{}
	var validator = &customValidator{mockValidatorClient}
	var ctx = context.Background()

	Context("Validating FAR Template creation", func() {

		When("agent name match format and binary", func() {
			It("should be accepted", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				warnings, err := validator.ValidateCreate(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				// May have warnings about status command testing
				if len(warnings) > 0 {
					Expect(warnings[0]).To(ContainSubstring("Fence agent status test failed"))
				}
			})
		})

		When("agent name was not found ", func() {
			It("should be rejected", func() {
				farTemplate := getTestFARTemplate(invalidAgentName)
				warnings, err := validator.ValidateCreate(ctx, farTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("unsupported fence agent: %s", invalidAgentName)))
			})
		})

		Context("with OutOfServiceTaint strategy", func() {
			var outOfServiceStrategy *FenceAgentsRemediationTemplate

			BeforeEach(func() {
				orgValue := isOutOfServiceTaintSupported
				DeferCleanup(func() { isOutOfServiceTaintSupported = orgValue })

				outOfServiceStrategy = getFARTemplate(validAgentName, OutOfServiceTaintRemediationStrategy)
			})

			When("out of service taint is supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = true
				})
				It("should be allowed", func() {
					warnings, err := validator.ValidateCreate(ctx, outOfServiceStrategy)
					Expect(err).NotTo(HaveOccurred())
					// May have warnings about status command testing
					if len(warnings) > 0 {
						Expect(warnings[0]).To(ContainSubstring("Fence agent status test failed"))
					}
				})
			})

			When("out of service taint is not supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = false
				})
				It("should be denied", func() {
					warnings, err := validator.ValidateCreate(ctx, outOfServiceStrategy)
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring(outOfServiceTaintUnsupportedMsg)))
				})
			})
		})
	})

	Context("updating FenceAgentsRemediationTemplate", func() {
		var oldFARTemplate *FenceAgentsRemediationTemplate
		When("agent name match format and binary", func() {
			BeforeEach(func() {
				oldFARTemplate = getTestFARTemplate(invalidAgentName)
			})
			It("should be accepted", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				warnings, err := validator.ValidateUpdate(ctx, oldFARTemplate, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				// May have warnings about status command testing
				if len(warnings) > 0 {
					Expect(warnings[0]).To(ContainSubstring("Fence agent status test failed"))
				}
			})
		})

		When("agent name was not found ", func() {
			BeforeEach(func() {
				oldFARTemplate = getTestFARTemplate(invalidAgentName)
			})
			It("should be rejected", func() {
				farTemplate := getTestFARTemplate(invalidAgentName)
				warnings, err := validator.ValidateUpdate(ctx, oldFARTemplate, farTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("unsupported fence agent: %s", invalidAgentName)))
			})
		})

		When("action parameter is invalid", func() {
			BeforeEach(func() {
				oldFARTemplate = getTestFARTemplate(validAgentName)
			})
			It("should be rejected", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				farTemplate.Spec.Template.Spec.SharedParameters = map[validation.ParameterName]string{
					"action": "off", // Invalid action
				}
				warnings, err := validator.ValidateUpdate(ctx, oldFARTemplate, farTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("FAR doesn't support any other action than reboot")))
			})
		})

		When("remediationStrategy is OutOfServiceTaint", func() {
			var outOfServiceStrategy *FenceAgentsRemediationTemplate
			var resourceDeletionStrategy *FenceAgentsRemediationTemplate

			BeforeEach(func() {
				orgValue := isOutOfServiceTaintSupported
				DeferCleanup(func() { isOutOfServiceTaintSupported = orgValue })

				outOfServiceStrategy = getFARTemplate(validAgentName, OutOfServiceTaintRemediationStrategy)
				resourceDeletionStrategy = getFARTemplate(validAgentName, ResourceDeletionRemediationStrategy)
			})

			When("out of service taint is supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = true
				})
				It("should be allowed", func() {
					warnings, err := validator.ValidateUpdate(ctx, resourceDeletionStrategy, outOfServiceStrategy)
					Expect(err).NotTo(HaveOccurred())
					// May have warnings about status command testing
					if len(warnings) > 0 {
						Expect(warnings[0]).To(ContainSubstring("Fence agent status test failed"))
					}
				})
			})

			When("out of service taint is not supported", func() {
				BeforeEach(func() {
					isOutOfServiceTaintSupported = false
				})
				It("should be denied", func() {
					warnings, err := validator.ValidateUpdate(ctx, resourceDeletionStrategy, outOfServiceStrategy)
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring(outOfServiceTaintUnsupportedMsg)))
				})
			})
		})
	})
})

func getTestFARTemplate(agentName string) *FenceAgentsRemediationTemplate {
	return getFARTemplate(agentName, ResourceDeletionRemediationStrategy)
}

func getFARTemplate(agentName string, strategy RemediationStrategyType) *FenceAgentsRemediationTemplate {
	return &FenceAgentsRemediationTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-" + agentName + "-template",
		},
		Spec: FenceAgentsRemediationTemplateSpec{
			Template: FenceAgentsRemediationTemplateResource{
				Spec: FenceAgentsRemediationSpec{
					Agent:               agentName,
					RemediationStrategy: strategy,
				},
			},
		},
	}
}
