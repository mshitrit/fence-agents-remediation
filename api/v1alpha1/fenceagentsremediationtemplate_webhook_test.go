package v1alpha1

import (
	"context"
	"errors"
	"strings"

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

// MockCommandExecutor for testing - implements executor.CommandExecutor
type MockCommandExecutor struct {
	Commands  [][]string              // Track called commands
	Responses map[string]MockResponse // Predefined responses
}

type MockResponse struct {
	Stdout string
	Stderr string
	Err    error
}

func (m *MockCommandExecutor) RunCommand(ctx context.Context, name string, args ...string) (string, string, error) {
	fullCmd := append([]string{name}, args...)
	m.Commands = append(m.Commands, fullCmd)

	// Match command pattern and return predefined response
	cmdStr := strings.Join(fullCmd, " ")
	if response, exists := m.Responses[cmdStr]; exists {
		return response.Stdout, response.Stderr, response.Err
	}

	// Default behavior - return error as fence agent is not available in test environment
	return "", "executable file not found in $PATH", errors.New("executable file not found in $PATH")
}

var _ = Describe("FenceAgentsRemediationTemplate Validation", func() {

	var (
		mockValidatorClient = &mockClient{}
		mockCommandExecutor = &MockCommandExecutor{
			Commands:  [][]string{},
			Responses: make(map[string]MockResponse),
		}

		validator = &customValidator{
			Client:          mockValidatorClient,
			commandExecutor: mockCommandExecutor,
		}
		ctx = context.Background()
	)

	Context("creating FenceAgentsRemediationTemplate", func() {

		When("agent name match format and binary", func() {
			It("should be accepted", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				_, err := validator.ValidateCreate(ctx, farTemplate)
				Expect(err).NotTo(HaveOccurred())
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
				oldFARTemplate = getTestFARTemplate(invalidAgentName)
			})
			It("should be accepted", func() {
				farTemplate := getTestFARTemplate(validAgentName)
				_, err := validator.ValidateUpdate(ctx, oldFARTemplate, farTemplate)
				Expect(err).NotTo(HaveOccurred())
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
		BeforeEach(func() {
			// Reset mock state before each test
			mockCommandExecutor.Commands = [][]string{}
			mockCommandExecutor.Responses = make(map[string]MockResponse)
		})

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

		It("should test validateParametersWithStatus success scenario", func() {
			// Setup mock to return "Status: ON" for successful validation
			// Note: We need to match the actual command string format
			mockCommandExecutor.Responses["fence_ipmilan --action status --ip 192.168.1.100 --port 623"] = MockResponse{
				Stdout: "Status: ON\n",
				Stderr: "",
				Err:    nil,
			}

			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "status-success-template",
					Namespace: "test-namespace",
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: "fence_ipmilan",
							NodeParameters: map[ParameterName]map[NodeName]string{
								"--ip": {
									"worker-1": "192.168.1.100",
								},
								"--port": {
									"worker-1": "623",
								},
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, farTemplate)
			// Should succeed because mock returns "Status: ON"
			Expect(warnings).To(BeEmpty())
			Expect(err).ToNot(HaveOccurred())

			// Verify the command was called correctly
			Expect(mockCommandExecutor.Commands).To(HaveLen(1))
			// Verify it contains the expected components (order may vary)
			actualCmd := mockCommandExecutor.Commands[0]
			Expect(actualCmd).To(ContainElement("fence_ipmilan"))
			Expect(actualCmd).To(ContainElement("--action"))
			Expect(actualCmd).To(ContainElement("status"))
			Expect(actualCmd).To(ContainElement("--ip"))
			Expect(actualCmd).To(ContainElement("192.168.1.100"))
			Expect(actualCmd).To(ContainElement("--port"))
			Expect(actualCmd).To(ContainElement("623"))
		})

		It("should test validateParametersWithStatus failure scenario", func() {
			// Setup mock to return non-ON status
			mockCommandExecutor.Responses["fence_ipmilan --action status --ip 192.168.1.101"] = MockResponse{
				Stdout: "Status: OFF\n",
				Stderr: "",
				Err:    nil,
			}

			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "status-failure-template",
					Namespace: "test-namespace",
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: "fence_ipmilan",
							NodeParameters: map[ParameterName]map[NodeName]string{
								"--ip": {
									"worker-1": "192.168.1.101",
								},
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, farTemplate)
			// Should succeed but with a warning because status is not ON
			Expect(err).ToNot(HaveOccurred())
			Expect(warnings).To(HaveLen(1))
			Expect(warnings[0]).To(ContainSubstring("fence agent parameter validation succeeded with warning"))
			Expect(warnings[0]).To(ContainSubstring("status is not ON"))

			// Verify the command was called
			Expect(mockCommandExecutor.Commands).To(HaveLen(1))
			actualCmd := mockCommandExecutor.Commands[0]
			Expect(actualCmd).To(ContainElement("fence_ipmilan"))
			Expect(actualCmd).To(ContainElement("--action"))
			Expect(actualCmd).To(ContainElement("status"))
			Expect(actualCmd).To(ContainElement("--ip"))
			Expect(actualCmd).To(ContainElement("192.168.1.101"))
		})

		It("should test validateParametersWithStatus command execution error", func() {
			// Setup mock to return execution error
			mockCommandExecutor.Responses["fence_ipmilan --action status --ip 192.168.1.102"] = MockResponse{
				Stdout: "",
				Stderr: "Connection failed",
				Err:    errors.New("exit status 1"),
			}

			farTemplate := &FenceAgentsRemediationTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "status-error-template",
					Namespace: "test-namespace",
				},
				Spec: FenceAgentsRemediationTemplateSpec{
					Template: FenceAgentsRemediationTemplateResource{
						Spec: FenceAgentsRemediationSpec{
							Agent: "fence_ipmilan",
							NodeParameters: map[ParameterName]map[NodeName]string{
								"--ip": {
									"worker-1": "192.168.1.102",
								},
							},
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(ctx, farTemplate)
			// Should fail because command execution failed
			Expect(warnings).To(BeEmpty())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("fence agent parameter validation failed"))
			Expect(err.Error()).To(ContainSubstring("fence agent command failed"))
			Expect(err.Error()).To(ContainSubstring("Connection failed"))

			// Verify the command was called
			Expect(mockCommandExecutor.Commands).To(HaveLen(1))
			actualCmd := mockCommandExecutor.Commands[0]
			Expect(actualCmd).To(ContainElement("fence_ipmilan"))
			Expect(actualCmd).To(ContainElement("--action"))
			Expect(actualCmd).To(ContainElement("status"))
			Expect(actualCmd).To(ContainElement("--ip"))
			Expect(actualCmd).To(ContainElement("192.168.1.102"))
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
