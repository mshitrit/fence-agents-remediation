package v1alpha1

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testNs = "test-namespace"

// getFuncNodeSecretIpConflict returns the default Get function behavior for secrets
func getFuncNodeSecretIpConflict() func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		// Default behavior - Return a pre-built secret for testing duplicate parameters
		if key.Name == "test-node-secret-ip-conflict" && key.Namespace == testNs {
			if secret, ok := obj.(*corev1.Secret); ok {
				secret.ObjectMeta = metav1.ObjectMeta{
					Name:      "test-node-secret-ip-conflict",
					Namespace: testNs,
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
}

var _ = Describe("FenceAgentsRemediationTemplate Validation", func() {

	var (
		mockValidatorClient = &mockClient{}

		validator = &customValidator{
			Client: mockValidatorClient,
		}
		ctx = context.Background()
	)

	Context("creating FenceAgentsRemediationTemplate", func() {

		When("agent name match format and binary", func() {
			It("should be accepted", func() {
				farTemplate := getFARTemplate(validAgentName, ResourceDeletionRemediationStrategy)
				_, err := validator.ValidateCreate(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		When("farTemplate has only shared parameters without NodeTemplate and no node parameters", func() {
			It("should be rejected", func() {
				farTemplate := getFARTemplate(validAgentName, ResourceDeletionRemediationStrategy)
				farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
					"ip":       "192.168.1.100",
					"username": "admin",
					"password": "secret",
				}
				// Explicitly ensure no node parameters
				farTemplate.Spec.Template.Spec.NodeParameters = nil

				warnings, err := validator.ValidateCreate(ctx, farTemplate)
				Expect(warnings).To(BeEmpty())
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(ContainSubstring("invalid spec: mandatory parameters are missing")))
			})
		})

		When("farTemplate has only shared parameters with NodeTemplate and no node parameters", func() {
			It("should be accepted", func() {
				farTemplate := getFARTemplate(validAgentName, ResourceDeletionRemediationStrategy)
				farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
					"ip":       "192.168.1.100",
					"username": "admin",
					"password": "secret-{{.NodeName}}", // This contains a NodeTemplate
				}
				// Explicitly ensure no node parameters
				farTemplate.Spec.Template.Spec.NodeParameters = nil

				warnings, err := validator.ValidateCreate(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				Expect(warnings).To(BeEmpty())
			})
		})

		When("farTemplate has only secret parameters with NodeTemplate and no node parameters", func() {
			It("should be accepted", func() {
				// Setup mock to return secret with NodeTemplate
				originalGetFunc := mockValidatorClient.GetFunc
				DeferCleanup(func() {
					mockValidatorClient.GetFunc = originalGetFunc
				})

				mockValidatorClient.GetFunc = func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == "test-shared-secret-with-template" && key.Namespace == testNs {
						if secret, ok := obj.(*corev1.Secret); ok {
							secret.ObjectMeta = metav1.ObjectMeta{
								Name:      "test-shared-secret-with-template",
								Namespace: testNs,
							}
							secret.Data = map[string][]byte{
								"--ip":       []byte("192.168.1.{{.NodeName}}"), // This contains a NodeTemplate
								"--username": []byte("admin"),
								"--password": []byte("secret"),
							}
							return nil
						}
					}
					return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
				}

				farTemplate := &FenceAgentsRemediationTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret-template",
						Namespace: testNs,
					},
					Spec: FenceAgentsRemediationTemplateSpec{
						Template: FenceAgentsRemediationTemplateResource{
							Spec: FenceAgentsRemediationSpec{
								Agent:               validAgentName,
								RemediationStrategy: ResourceDeletionRemediationStrategy,
								SharedSecretName:    ptr.To("test-shared-secret-with-template"),
								// Explicitly ensure no node parameters or shared parameters
								NodeParameters:   nil,
								SharedParameters: nil,
							},
						},
					},
				}

				warnings, err := validator.ValidateCreate(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
				Expect(warnings).To(BeEmpty())
			})
		})

		When("farTemplate has no shared parameters and no node parameters", func() {
			It("should be rejected", func() {
				farTemplate := &FenceAgentsRemediationTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-" + validAgentName + "-template",
					},
					Spec: FenceAgentsRemediationTemplateSpec{
						Template: FenceAgentsRemediationTemplateResource{
							Spec: FenceAgentsRemediationSpec{
								Agent:               validAgentName,
								RemediationStrategy: ResourceDeletionRemediationStrategy,
								// Explicitly no SharedParameters or NodeParameters
							},
						},
					},
				}
				warnings, err := validator.ValidateCreate(ctx, farTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("invalid spec: mandatory parameters are missing")))
			})
		})

		When("agent name was not found ", func() {
			It("should be rejected", func() {
				farTemplate := getFARTemplate(invalidAgentName, ResourceDeletionRemediationStrategy)
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
					_, err := validator.ValidateCreate(ctx, outOfServiceStrategy)
					Expect(err).NotTo(HaveOccurred())
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
				oldFARTemplate = getFARTemplate(invalidAgentName, ResourceDeletionRemediationStrategy)
			})
			It("should be accepted", func() {
				farTemplate := getFARTemplate(validAgentName, ResourceDeletionRemediationStrategy)
				_, err := validator.ValidateUpdate(ctx, oldFARTemplate, farTemplate)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		When("agent name was not found ", func() {
			BeforeEach(func() {
				oldFARTemplate = getFARTemplate(invalidAgentName, ResourceDeletionRemediationStrategy)
			})
			It("should be rejected", func() {
				farTemplate := getFARTemplate(invalidAgentName, ResourceDeletionRemediationStrategy)
				warnings, err := validator.ValidateUpdate(ctx, oldFARTemplate, farTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("unsupported fence agent: %s", invalidAgentName)))
			})
		})

		When("action parameter is invalid", func() {
			BeforeEach(func() {
				oldFARTemplate = getFARTemplate(validAgentName, ResourceDeletionRemediationStrategy)
			})
			It("should be rejected", func() {
				farTemplate := getFARTemplate(validAgentName, ResourceDeletionRemediationStrategy)
				farTemplate.Spec.Template.Spec.SharedParameters = map[ParameterName]string{
					"action": "shutdown", // Invalid action
				}
				warnings, err := validator.ValidateUpdate(ctx, oldFARTemplate, farTemplate)
				ExpectWithOffset(1, warnings).To(BeEmpty())
				Expect(err).To(MatchError(ContainSubstring("FAR doesn't support any other action than `reboot` or `off`")))
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
					_, err := validator.ValidateUpdate(ctx, resourceDeletionStrategy, outOfServiceStrategy)
					Expect(err).NotTo(HaveOccurred())
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
					Namespace: testNs,
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
					Namespace: testNs,
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
		BeforeEach(func() {
			// Set up default secret behavior for tests that need it
			mockValidatorClient.GetFunc = getFuncNodeSecretIpConflict()
		})

		It("should fail when template has invalid action parameter", func() {
			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-action-template",
					Namespace: testNs,
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: validAgentName,
							SharedParameters: map[ParameterName]string{
								"--ip":     "192.168.1.100",
								"--action": "shutdown", // Invalid action - only "reboot" or "off" are supported
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, farTemplate)
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("FAR doesn't support any other action than `reboot`"))
		})

		It("should fail when templates reference missing node secrets", func() {
			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "missing-secrets-template",
					Namespace: testNs,
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
					Namespace: testNs,
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
								"worker-1": "test-node-secret-ip-conflict", // This secret contains "--ip" parameter
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, farTemplate)
			// Should fail because "--ip" is defined in both NodeParameters and the secret
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid multiple definition of FAR parameter"))
		})

	})
})

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
					// Add basic shared parameters with a template to satisfy new validation
					SharedParameters: map[ParameterName]string{
						"ip":       "192.168.1.100",
						"username": "admin-{{.NodeName}}", // Contains NodeTemplate to satisfy validation
					},
				},
			},
		},
	}
}
