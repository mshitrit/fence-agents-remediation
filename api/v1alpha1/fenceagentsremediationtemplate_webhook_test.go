package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/medik8s/fence-agents-remediation/pkg/validation"
)

var _ = Describe("FenceAgentsRemediationTemplate Validation", func() {

	Context("creating FenceAgentsRemediationTemplate", func() {

		When("agent name match format and binary", func() {
			It("should be accepted", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				warnings, err := farTemplate.ValidateCreate()
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
				warnings, err := farTemplate.ValidateCreate()
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
					warnings, err := outOfServiceStrategy.ValidateCreate()
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
					warnings, err := outOfServiceStrategy.ValidateCreate()
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
				warnings, err := farTemplate.ValidateUpdate(oldFARTemplate)
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
				warnings, err := farTemplate.ValidateUpdate(oldFARTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("unsupported fence agent: %s", invalidAgentName)))
			})
		})

		When("updating with invalid parameters", func() {
			BeforeEach(func() {
				oldFARTemplate = getTestFARTemplate(validAgentName)
			})
			It("should be rejected", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				farTemplate.Spec.Template.Spec.SharedParameters = map[validation.ParameterName]string{
					"action": "off", // Invalid action
				}
				warnings, err := farTemplate.ValidateUpdate(oldFARTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("FAR doesn't support any other action than reboot")))
			})
		})

		Context("with OutOfServiceTaint strategy", func() {
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
					warnings, err := outOfServiceStrategy.ValidateUpdate(resourceDeletionStrategy)
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
					warnings, err := outOfServiceStrategy.ValidateUpdate(resourceDeletionStrategy)
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
