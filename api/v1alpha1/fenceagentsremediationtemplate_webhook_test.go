package v1alpha1

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// mockClient for testing
type mockClient struct {
	client.Client
}

// Implement Get method to handle secret retrieval in tests
func (m *mockClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	// Return a pre-built secret for testing duplicate parameters
	if key.Name == "test-node-secret" && key.Namespace == "test-namespace" {
		if secret, ok := obj.(*corev1.Secret); ok {
			secret.ObjectMeta = metav1.ObjectMeta{
				Name:      "test-node-secret",
				Namespace: "test-namespace",
			}
			secret.Data = map[string][]byte{
				"--ip":       []byte("192.168.1.100"), // This will conflict with NodeParameters
				"--username": []byte("admin"),
			}
			return nil
		}
	}
	// Return NotFound error for any other secret to simulate missing secrets
	return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
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

		When("template has only shared parameters and no node parameters", func() {
			It("should be accepted", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
					"ip":       "192.168.1.100",
					"username": "admin",
					"password": "secret",
				}
				// Explicitly ensure no node parameters
				farTemplate.Spec.Template.Spec.NodeParameters = nil

				warnings, err := validator.ValidateCreate(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				// No warnings expected about node-specific parameters since there are none
				Expect(warnings).To(BeEmpty())
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
				farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
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

	Context("validating template syntax", func() {
		It("should aggregate multiple template validation errors", func() {
			// Create a template with multiple invalid template strings
			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-template",
					Namespace: "test-namespace",
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[ParameterName]string{
								"--systems-uri": "/redfish/v1/Systems/{{.NodeName", // Missing closing brace
								"--hostname":    "{{.InvalidField}}",               // Unsupported name, only NodeName is supported
								"--port":        "{{.NodeName}}.com",               // Valid template
								"--invalid":     "/path/{{.NodeName",               // Another missing closing brace
							},
						},
					},
				},
			}

			// Validate and expect aggregated errors
			warnings, err := validator.ValidateCreate(ctx, farTemplate)
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())

			// Check that the error message contains information about multiple failures
			errorMsg := err.Error()
			Expect(errorMsg).To(ContainSubstring("--systems-uri"))
			Expect(errorMsg).To(ContainSubstring("--hostname"))
			Expect(errorMsg).To(ContainSubstring("--invalid"))
			// The valid parameter should not appear in error message
			Expect(errorMsg).ToNot(ContainSubstring("--port"))
		})

		It("should succeed when all templates are valid", func() {
			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valid-template",
					Namespace: "test-namespace",
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[ParameterName]string{
								"--systems-uri": "/redfish/v1/Systems/{{.NodeName}}",
								"--hostname":    "{{.NodeName}}.example.com",
								"--port":        "623", // No template, should be fine
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, farTemplate)
			Expect(err).ToNot(HaveOccurred())
			Expect(warnings).To(BeEmpty())
		})
	})

	Context("validating parameter validation functionality", func() {
		It("should fail when template has invalid action parameter", func() {
			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-action-template",
					Namespace: "test-namespace",
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[ParameterName]string{
								"--ip":     "192.168.1.100",
								"--action": "off", // Invalid action - only "reboot" is supported
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, farTemplate)
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("FAR doesn't support any other action than reboot"))
		})

		It("should fail when templates reference missing node secrets", func() {
			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "missing-secrets-template",
					Namespace: "test-namespace",
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[ParameterName]string{
								"--ip": "192.168.1.100",
							},
							NodeSecretNames: map[NodeName]string{
								"worker-1": "non-existent-node-secret",
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, farTemplate)
			// Should fail because node secrets are expected to exist when referenced
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("node secret 'non-existent-node-secret' not found in namespace 'test-namespace'"))
		})

		It("should fail when NodeSecretParam duplicates a NodeParam", func() {
			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "duplicate-params-template",
					Namespace: "test-namespace",
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: validAgentName,
							NodeParameters: map[ParameterName]map[NodeName]string{
								"--ip": {
									"worker-1": "192.168.1.101", // This will conflict with secret
								},
								"--port": {
									"worker-1": "623",
								},
							},
							NodeSecretNames: map[NodeName]string{
								"worker-1": "test-node-secret", // This secret contains "--ip" parameter
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, farTemplate)
			// Should fail because "--ip" is defined in both NodeParameters and the secret
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid multiple definition of FAR param"))
		})

	})
})

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}

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
