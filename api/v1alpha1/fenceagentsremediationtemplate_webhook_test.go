package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

		Context("with parameter validation", func() {

			When("template has valid shared parameters", func() {
				It("should be accepted", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
						"ip":       "192.168.1.100",
						"username": "admin",
					}
					warnings, err := farTemplate.ValidateCreate()
					Expect(err).NotTo(HaveOccurred())
					// Should have warnings about status test failure due to missing real credentials
					Expect(len(warnings)).To(BeNumerically(">", 0))
				})
			})

			When("template has empty shared parameters", func() {
				It("should be accepted", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
						"ip":       "", // Empty values are allowed (might come from secrets)
						"username": "admin",
					}
					warnings, err := farTemplate.ValidateCreate()
					Expect(err).NotTo(HaveOccurred())
					// Should have warnings about status test failure
					Expect(len(warnings)).To(BeNumerically(">", 0))
				})
			})

			When("template has invalid action parameter in shared parameters", func() {
				It("should be rejected", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
						"action": "status", // Should be reboot or empty
					}
					warnings, err := farTemplate.ValidateCreate()
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring("FAR doesn't support any other action than reboot")))
				})
			})

			When("template has valid action parameter in shared parameters", func() {
				It("should be accepted", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
						"action": "reboot", // Valid action
						"ip":     "192.168.1.100",
					}
					warnings, err := farTemplate.ValidateCreate()
					Expect(err).NotTo(HaveOccurred())
					Expect(len(warnings)).To(BeNumerically(">", 0))
				})
			})

			When("template has empty action parameter in shared parameters", func() {
				It("should be accepted", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
						"action": "", // Empty is allowed
						"ip":     "192.168.1.100",
					}
					warnings, err := farTemplate.ValidateCreate()
					Expect(err).NotTo(HaveOccurred())
					Expect(len(warnings)).To(BeNumerically(">", 0))
				})
			})

			When("template has valid node parameters", func() {
				It("should be accepted", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.NodeParameters = map[ParameterName]map[NodeName]string{
						"slot": {
							"worker-1": "1",
							"worker-2": "2",
						},
					}
					warnings, err := farTemplate.ValidateCreate()
					Expect(err).NotTo(HaveOccurred())
					// Should have warning about node-specific parameters
					Expect(warnings).To(ContainElement(ContainSubstring("Template contains node-specific parameters")))
				})
			})

			When("template has node parameter with empty node mappings", func() {
				It("should be rejected", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.NodeParameters = map[ParameterName]map[NodeName]string{
						"slot": {}, // Empty node map
					}
					warnings, err := farTemplate.ValidateCreate()
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring("node parameter 'slot' is defined but has no node mappings")))
				})
			})

			When("template has node parameter with empty node name", func() {
				It("should be rejected", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.NodeParameters = map[ParameterName]map[NodeName]string{
						"slot": {
							"":         "1", // Empty node name
							"worker-1": "2",
						},
					}
					warnings, err := farTemplate.ValidateCreate()
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring("empty node name found in parameter 'slot'")))
				})
			})

			When("template has node parameter with empty parameter value", func() {
				It("should be rejected", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.NodeParameters = map[ParameterName]map[NodeName]string{
						"slot": {
							"worker-1": "", // Empty parameter value
							"worker-2": "2",
						},
					}
					warnings, err := farTemplate.ValidateCreate()
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring("empty parameter value for node 'worker-1' in parameter 'slot'")))
				})
			})

			When("template has invalid action parameter in node parameters", func() {
				It("should be rejected", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.NodeParameters = map[ParameterName]map[NodeName]string{
						"action": {
							"worker-1": "off", // Invalid action
						},
					}
					warnings, err := farTemplate.ValidateCreate()
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring("FAR doesn't support any other action than reboot")))
				})
			})

			When("template has valid action parameter in node parameters", func() {
				It("should be accepted", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.NodeParameters = map[ParameterName]map[NodeName]string{
						"action": {
							"worker-1": "reboot", // Valid action
						},
						"slot": {
							"worker-1": "1",
						},
					}
					warnings, err := farTemplate.ValidateCreate()
					Expect(err).NotTo(HaveOccurred())
					Expect(warnings).To(ContainElement(ContainSubstring("Template contains node-specific parameters")))
				})
			})

			When("template has alt-format action parameter", func() {
				It("should validate correctly", func() {
					farTemplate := getTestFARTemplate(validAgentName)
					farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
						"--action": "status", // Alt format, should be rejected
					}
					warnings, err := farTemplate.ValidateCreate()
					ExpectWithOffset(1, warnings).To(BeEmpty())
					Expect(err).To(MatchError(ContainSubstring("FAR doesn't support any other action than reboot")))
				})
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
				farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
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
