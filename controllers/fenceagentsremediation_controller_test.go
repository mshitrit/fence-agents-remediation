/*
Copyright 2023.
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
package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	commonConditions "github.com/medik8s/common/pkg/conditions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
)

const (
	dummyNode      = "dummy-node"
	workerNode     = "worker-0"
	fenceAgentIPMI = "fence_ipmilan"
	farPodName     = "far-pod"
	testPodName    = "far-pod-test-1"

	// intervals
	timeoutPreRemediation  = "1s" // this timeout is used for the other steps that occur before remediation is completed
	timeoutPostRemediation = "2s" // this timeout is used for the other steps that occur after remediation is completed
	pollInterval           = "200ms"

	// eventSteps
	eventExist    = "Verifying that event %s was created from"
	eventNotExist = "Verifying that event %s was not created from"
)

var (
	faPodLabels = map[string]string{"app.kubernetes.io/name": "fence-agents-remediation-operator"}
	log         = ctrl.Log.WithName("controllers-unit-test")
)

var _ = Describe("FAR Controller", func() {
	var (
		node                     *corev1.Node
		underTestFAR             = &v1alpha1.FenceAgentsRemediation{}
		nodeSecret, sharedSecret = &corev1.Secret{}, &corev1.Secret{}
	)

	noActionShareParam := map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--password": "password",
		"--ip":       "192.168.111.1",
		"--lanplus":  "",
	}
	testShareParam := map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--password": "password",
		"--action":   "reboot",
		"--ip":       "192.168.111.1",
		"--lanplus":  "",
	}
	testShareParamTwice := map[v1alpha1.ParameterName]string{
		"--username": "admin",
		"--ipport":   "600",
		"--password": "password",
		"--action":   "reboot",
		"--ip":       "192.168.111.1",
		"--lanplus":  "",
	}
	testNodeParam := map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{
		"--ipport": {
			"master-0": "6230",
			"master-1": "6231",
			"master-2": "6232",
			"worker-0": "6233",
			"worker-1": "6234",
			"worker-2": "6235",
		},
	}
	BeforeEach(func() {
		storedCommand = storedCommand[:0]
	})

	Context("Reconcile with ResourceDeletion strategy", func() {
		nodeSecretName := fmt.Sprintf("fence-agents-credentials-node-%s", workerNode)
		sharedSecretName := "fence-agents-credentials-shared"
		farRemediationTaint := utils.CreateRemediationTaint()
		conditionStatusPointer := func(status metav1.ConditionStatus) *metav1.ConditionStatus { return &status }
		underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
		BeforeEach(func() {
			// Create two pods and at the end clean them up with DeferCleanup
			testPod := createRunningPod("far-test-1", testPodName, workerNode)
			DeferCleanup(cleanupTestedResources, testPod)

			farPod := createRunningPod("far-manager-test", farPodName, "")
			DeferCleanup(k8sClient.Delete, context.Background(), farPod)

			nodeSecret = generateSecret(nodeSecretName, map[string][]byte{
				"--pass":  []byte("abc"),
				"--pass2": []byte("abc2"),
			})
			sharedSecret = generateSecret(sharedSecretName, map[string][]byte{})
		})

		JustBeforeEach(func() {
			// Create node, and FAR CR, and at the end clean them up with DeferCleanup
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), node)

			Expect(k8sClient.Create(context.Background(), nodeSecret)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), nodeSecret)

			Expect(k8sClient.Create(context.Background(), sharedSecret)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), sharedSecret)

			Expect(k8sClient.Create(context.Background(), underTestFAR)).To(Succeed())
			DeferCleanup(func() {
				Expect(cleanupFar(context.Background(), underTestFAR)).To(Succeed())
				deleteErr := k8sClient.Get(ctx, client.ObjectKeyFromObject(underTestFAR), &v1alpha1.FenceAgentsRemediation{})
				if !apierrors.IsNotFound(deleteErr) {
					verifyEvent(corev1.EventTypeNormal, utils.EventReasonRemoveFinalizer, utils.EventMessageRemoveFinalizer)
				}
				clearEvents()
			})

			// Sleep for a second to ensure dummy reconciliation has begun running before the unit tests
			time.Sleep(1 * time.Second)
		})
		Context("Verify correct params", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
			})
			When("A param is defined both in shared Secret and in node Secret", func() {
				BeforeEach(func() {
					sharedSecret = generateSecret(sharedSecretName, map[string][]byte{
						"--mock-secure-param-a": []byte("mock-top-secret-shared-value"),
						"--mock-secure-param-b": []byte("mock-top-secret-value-b"),
					})
					nodeSecret = generateSecret(nodeSecretName, map[string][]byte{
						"--mock-secure-param-a": []byte("mock-top-secret-node-value"),
						"--mock-secure-param-c": []byte("mock-top-secret-value-c"),
					})
					underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)

				})
				It("Node Secret param should override shared Secret param", func() {
					Eventually(func(g Gomega) {
						g.Expect(storedCommand).To(ConsistOf([]string{
							"fence_ipmilan",
							"--lanplus",
							"--password=password",
							"--username=admin",
							"--action=reboot",
							"--ip=192.168.111.1",
							"--mock-secure-param-a=mock-top-secret-node-value",
							"--mock-secure-param-b=mock-top-secret-value-b",
							"--mock-secure-param-c=mock-top-secret-value-c",
							"--ipport=6233"}))
					}, timeoutPreRemediation, pollInterval).Should(Succeed())
				})
			})
			When("A param is defined both as secure (Secret) and non-secure", func() {
				When("A param is defined both in sharedSecret and in shared params", func() {
					BeforeEach(func() {
						dupParamKey := "--mockparam"
						testShareParam[v1alpha1.ParameterName(dupParamKey)] = "mockValue"
						DeferCleanup(func() { delete(testShareParam, v1alpha1.ParameterName(dupParamKey)) })

						sharedSecret = generateSecret(sharedSecretName, map[string][]byte{
							dupParamKey: []byte("mockValue"),
						})

						underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
					})
					It("A validation error would prevent execution of fence agent command", func() {
						// No remediation should be executed due to an error
						Consistently(func(g Gomega) {
							g.Expect(storedCommand).To(BeEmpty())
						}, timeoutPreRemediation, pollInterval).Should(Succeed())
						// Taint is added before execution of fence agent takes place
						verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)
						// Actual execution does not happen because of the validation error
						verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentExecuted, utils.EventMessageFenceAgentExecuted)

					})
				})
				When("A param is defined both in sharedSecret and in node params", func() {
					BeforeEach(func() {
						dupParamKey := "--mockparam"
						testNodeParam[v1alpha1.ParameterName(dupParamKey)] = map[v1alpha1.NodeName]string{workerNode: "mockValue"}
						DeferCleanup(func() { delete(testNodeParam, v1alpha1.ParameterName(dupParamKey)) })

						sharedSecret = generateSecret(sharedSecretName, map[string][]byte{
							dupParamKey: []byte("mockValue"),
						})
						underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
					})
					It("A validation error would prevent execution of fence agent command", func() {
						// No remediation should be executed due to an error
						Consistently(func(g Gomega) {
							g.Expect(storedCommand).To(BeEmpty())
						}, timeoutPreRemediation, pollInterval).Should(Succeed())
						// Taint is added before execution of fence agent takes place
						verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)
						// Actual execution does not happen because of the validation error
						verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentExecuted, utils.EventMessageFenceAgentExecuted)

					})
				})
				When("A param is defined both in node Secret and in shared params", func() {
					BeforeEach(func() {
						dupParamKey := "--mockparam"
						testShareParam[v1alpha1.ParameterName(dupParamKey)] = "mockValue"
						DeferCleanup(func() { delete(testShareParam, v1alpha1.ParameterName(dupParamKey)) })

						nodeSecret = generateSecret(nodeSecretName, map[string][]byte{
							dupParamKey: []byte("mockValue"),
						})

						underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
					})
					It("A validation error would prevent execution of fence agent command", func() {
						// No remediation should be executed due to an error
						Consistently(func(g Gomega) {
							g.Expect(storedCommand).To(BeEmpty())
						}, timeoutPreRemediation, pollInterval).Should(Succeed())
						// Taint is added before execution of fence agent takes place
						verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)
						// Actual execution does not happen because of the validation error
						verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentExecuted, utils.EventMessageFenceAgentExecuted)

					})
				})
				When("A param is defined both in node Secret and in node params", func() {
					BeforeEach(func() {
						dupParamKey := "--mockparam"
						testNodeParam[v1alpha1.ParameterName(dupParamKey)] = map[v1alpha1.NodeName]string{workerNode: "mockValue"}
						DeferCleanup(func() { delete(testNodeParam, v1alpha1.ParameterName(dupParamKey)) })

						nodeSecret = generateSecret(nodeSecretName, map[string][]byte{
							dupParamKey: []byte("mockValue"),
						})

						underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
					})
					It("A validation error would prevent execution of fence agent command", func() {
						// No remediation should be executed due to an error
						Consistently(func(g Gomega) {
							g.Expect(storedCommand).To(BeEmpty())
						}, timeoutPreRemediation, pollInterval).Should(Succeed())
						// Taint is added before execution of fence agent takes place
						verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)
						// Actual execution does not happen because of the validation error
						verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentExecuted, utils.EventMessageFenceAgentExecuted)

					})
				})
			})
			When("FAR CR misses the action parameter", func() {
				BeforeEach(func() {
					underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, noActionShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
				})
				It("should succeed and add the action parameter with value reboot", func() {
					Eventually(func(g Gomega) {
						g.Expect(storedCommand).To(ConsistOf([]string{
							"fence_ipmilan",
							"--lanplus",
							"--password=password",
							"--username=admin",
							"--action=reboot",
							"--ip=192.168.111.1",
							"--pass2=abc2",
							"--pass=abc",
							"--ipport=6233"}))
					}, timeoutPreRemediation, pollInterval).Should(Succeed())
				})
			})
			When("Param defined both in Node and shared params", func() {
				BeforeEach(func() {
					underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParamTwice, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)

				})
				It("Node param should be used with ipport `6233`", func() {
					Eventually(func(g Gomega) {
						g.Expect(storedCommand).To(ConsistOf([]string{
							"fence_ipmilan",
							"--lanplus",
							"--password=password",
							"--username=admin",
							"--action=reboot",
							"--ip=192.168.111.1",
							"--pass2=abc2",
							"--pass=abc",
							"--ipport=6233"}))
					}, timeoutPreRemediation, pollInterval).Should(Succeed())
				})

			})
		})
		When("creating valid FAR CR", func() {

			testSuccessfulRemediation := func() {
				Eventually(func(g Gomega) {
					g.Expect(storedCommand).To(ConsistOf([]string{
						"fence_ipmilan",
						"--pass=abc",
						"--pass2=abc2",
						"--lanplus",
						"--password=password",
						"--username=admin",
						"--action=reboot",
						"--ip=192.168.111.1",
						"--ipport=6233"}))
				}, timeoutPreRemediation, pollInterval).Should(Succeed())

				underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

				By("Not having any test pod")
				verifyPodDeleted(testPodName)

				By("Verifying correct conditions for successful remediation")
				verifyRemediationConditions(
					underTestFAR,
					conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
					conditionStatusPointer(metav1.ConditionTrue),  // FenceAgentActionSucceededTypeStatus
					conditionStatusPointer(metav1.ConditionTrue))  // SucceededTypeStatus
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonNodeRemediationCompleted, utils.EventMessageNodeRemediationCompleted)
			}
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
			})
			When("node name is stored in remediation name", func() {
				It("should have finalizer and taint, while the tested pod will be deleted", testSuccessfulRemediation)
			})
			//remediation is created from escalation remediation supporting same kind template
			When("node name is stored in remediation's annotation", func() {
				BeforeEach(func() {
					underTestFAR.Name = fmt.Sprintf("%s-%s", workerNode, "pseudo-random-test-sufix")
					underTestFAR.Annotations = map[string]string{"remediation.medik8s.io/node-name": workerNode}
				})
				It("should have finalizer and taint, while the tested pod will be deleted", testSuccessfulRemediation)
			})
		})

		When("creating invalid FAR CR Name", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(dummyNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
			})

			It("should not have a finalizer nor taint, while the tested pod will remain", func() {
				By("Not finding a matching node to FAR CR's name")
				Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: underTestFAR.Name}, node)).To(Not(Succeed()))

				By("Not having finalizer")
				Consistently(func(g Gomega) bool {
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: underTestFAR.Name, Namespace: defaultNamespace}, underTestFAR)).To(Succeed())
					return controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer)
				}, timeoutPreRemediation, pollInterval).Should(BeFalse(), "finalizer shouldn't be added")
				verifyEvent(corev1.EventTypeWarning, utils.EventReasonCrNodeNotFound, utils.EventMessageCrNodeNotFound)
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonRemediationStarted, utils.EventMessageRemediationStarted)

				// If finalizer is missing, then a taint shouldn't exist
				By("Not having remediation taint")
				Expect(utils.TaintExists(node.Spec.Taints, &farRemediationTaint)).To(BeFalse())
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)

				By("Still having one test pod")
				verifyPodExists(testPodName)

				By("Verifying correct conditions for unsuccessful remediation")
				verifyRemediationConditions(
					underTestFAR,
					conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
					conditionStatusPointer(metav1.ConditionFalse), // FenceAgentActionSucceededTypeStatus
					conditionStatusPointer(metav1.ConditionFalse)) // SucceededTypeStatus
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonNodeRemediationCompleted, utils.EventReasonNodeRemediationCompleted)
			})
		})

		Context("Fence agent failures", func() {
			BeforeEach(func() {
				plogs.Clear()
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.ResourceDeletionRemediationStrategy)
			})

			When("CR is deleted in between fence agent retries", func() {
				BeforeEach(func() {
					// Fail controlledRun to simulate a case where CR deletion occurs in between consecutive fence agent
					// command calls.
					mockError = errors.New("mock error")
					DeferCleanup(func() { mockError = nil })

					underTestFAR.Spec.RetryCount = 100
					underTestFAR.Spec.RetryInterval = metav1.Duration{Duration: 1 * time.Second}
				})

				It("should exit immediately without trying to update the status conditions", func() {
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

					By("Wait some retries")
					Eventually(func() int {
						return plogs.CountOccurences(cli.FenceAgentFailedCommandMessage)
					}, "10s", "1s").Should(BeNumerically(">", 3))

					By("Deleting FAR CR")
					Expect(k8sClient.Delete(context.Background(), underTestFAR)).To(Succeed())

					By("Verifying goroutine stopped without trying to update the conditions")
					Eventually(func() bool {
						return plogs.Contains(cli.FenceAgentContextCanceledMessage)
					}).Should(BeTrue())
					verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				})
			})

			When("CR is deleted during fence agent execution", func() {
				BeforeEach(func() {
					// Fail controlledRun to simulate a case where CR deletion occurs during a fence agent
					// command call.
					forcedDelay = 10 * time.Second
					DeferCleanup(func() { forcedDelay = 0 })

					underTestFAR.Spec.RetryCount = 100
					underTestFAR.Spec.RetryInterval = metav1.Duration{Duration: 1 * time.Second}
				})

				It("should exit immediately without trying to update the status conditions", func() {
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

					By("Deleting FAR CR")
					Expect(k8sClient.Delete(context.Background(), underTestFAR)).To(Succeed())

					By("Verifying goroutine stopped without trying to update the conditions")
					Eventually(func() bool {
						return plogs.Contains(cli.FenceAgentContextCanceledMessage)
					}).Should(BeTrue())
					verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				})
			})

			When("Fence Agent command fails", func() {
				BeforeEach(func() {
					mockError = errors.New("mock error")
					DeferCleanup(func() { mockError = nil })

					underTestFAR.Spec.RetryCount = 3
					underTestFAR.Spec.RetryInterval = metav1.Duration{Duration: 1 * time.Millisecond}
				})

				It("should retry the fence agent command as configured and update the status accordingly", func() {
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

					By("Still having one test pod")
					verifyPodExists(testPodName)

					By("Reading the expected number of retries")
					Eventually(func() int {
						return plogs.CountOccurences(cli.FenceAgentFailedCommandMessage)
					}).Should(Equal(3))

					By("Verifying correct conditions for un-successful remediation")
					verifyRemediationConditions(
						underTestFAR,
						conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
						conditionStatusPointer(metav1.ConditionFalse), // FenceAgentActionSucceededTypeStatus
						conditionStatusPointer(metav1.ConditionFalse)) // SucceededTypeStatus
					verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				})
			})

			When("Fence Agent command times out", func() {
				BeforeEach(func() {
					forcedDelay = 10 * time.Second
					DeferCleanup(func() { forcedDelay = 0 })

					underTestFAR.Spec.Timeout = metav1.Duration{Duration: 2 * time.Second}
				})

				It("should stop Fence Agent execution and update the status accordingly", func() {
					underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

					By("Still having one test pod")
					verifyPodExists(testPodName)

					By("Context timeout occurred")
					Eventually(func() bool {
						return plogs.Contains(cli.FenceAgentContextTimedOutMessage)
					}).Should(BeTrue(), "fence agent should have timed out")

					By("Verifying correct conditions for un-successful remediation")
					verifyRemediationConditions(
						underTestFAR,
						conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
						conditionStatusPointer(metav1.ConditionFalse), // FenceAgentActionSucceededTypeStatus
						conditionStatusPointer(metav1.ConditionFalse)) // SucceededTypeStatus
					verifyNoEvent(corev1.EventTypeNormal, utils.EventReasonFenceAgentSucceeded, utils.EventMessageFenceAgentSucceeded)
				})
			})
		})
	})

	Context("Reconcile with OutOfServiceTaint strategy", func() {
		farRemediationTaint := utils.CreateRemediationTaint()
		outOfServiceTaint := utils.CreateOutOfServiceTaint()
		conditionStatusPointer := func(status metav1.ConditionStatus) *metav1.ConditionStatus { return &status }

		BeforeEach(func() {
			farPod := createRunningPod("far-manager-test", farPodName, "")
			DeferCleanup(k8sClient.Delete, context.Background(), farPod)
		})

		JustBeforeEach(func() {
			// Create node, and FAR CR, and at the end clean them up with DeferCleanup
			Expect(k8sClient.Create(context.Background(), node)).To(Succeed())
			DeferCleanup(k8sClient.Delete, context.Background(), node)

			Expect(k8sClient.Create(context.Background(), underTestFAR)).To(Succeed())
			DeferCleanup(func() {
				Expect(cleanupFar(context.Background(), underTestFAR)).To(Succeed())
				deleteErr := k8sClient.Get(ctx, client.ObjectKeyFromObject(underTestFAR), &v1alpha1.FenceAgentsRemediation{})
				if !apierrors.IsNotFound(deleteErr) {
					verifyEvent(corev1.EventTypeNormal, utils.EventReasonRemoveFinalizer, utils.EventMessageRemoveFinalizer)
				}
				clearEvents()
			})

			// Sleep for a second to ensure dummy reconciliation has begun running before the unit tests
			time.Sleep(1 * time.Second)
		})

		When("creating valid FAR CR", func() {
			BeforeEach(func() {
				node = utils.GetNode("", workerNode)
				underTestFAR = getFenceAgentsRemediation(workerNode, fenceAgentIPMI, testShareParam, testNodeParam, v1alpha1.OutOfServiceTaintRemediationStrategy)
			})

			It("should have finalizer, both remediation taint and out-of-service taint, and at the end they will be deleted", func() {
				Eventually(func(g Gomega) {
					g.Expect(storedCommand).To(ConsistOf([]string{
						"fence_ipmilan",
						"--lanplus",
						"--password=password",
						"--username=admin",
						"--action=reboot",
						"--ip=192.168.111.1",
						"--ipport=6233"}))
				}, timeoutPreRemediation, pollInterval).Should(Succeed())

				underTestFAR = verifyPreRemediationSucceed(underTestFAR, defaultNamespace, &farRemediationTaint)

				By("Searching for out-of-service taint")
				Eventually(func(g Gomega) {
					node := &corev1.Node{}
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: workerNode}, node)).To(Succeed())
					g.Expect(utils.TaintExists(node.Spec.Taints, &outOfServiceTaint)).To(BeTrue(), "out-of-service taint should exist")
				}, timeoutPostRemediation, pollInterval).Should(Succeed())

				By("Verifying correct conditions for successful remediation")
				verifyRemediationConditions(
					underTestFAR,
					conditionStatusPointer(metav1.ConditionFalse), // ProcessingTypeStatus
					conditionStatusPointer(metav1.ConditionTrue),  // FenceAgentActionSucceededTypeStatus
					conditionStatusPointer(metav1.ConditionTrue))  // SucceededTypeStatus
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddOutOfServiceTaint, utils.EventMessageAddOutOfServiceTaint)
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonNodeRemediationCompleted, utils.EventMessageNodeRemediationCompleted)

				By("Deleting FAR CR")
				Expect(k8sClient.Delete(context.Background(), underTestFAR)).To(Succeed())

				Eventually(func(g Gomega) {
					node := &corev1.Node{}
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: workerNode}, node)).To(Succeed())
					g.Expect(utils.TaintExists(node.Spec.Taints, &outOfServiceTaint)).To(BeFalse(), "out-of-service taint should be removed")
				}, timeoutPostRemediation, pollInterval).Should(Succeed())
				verifyEvent(corev1.EventTypeNormal, utils.EventReasonRemoveOutOfServiceTaint, utils.EventMessageRemoveOutOfServiceTaint)
			})
		})
	})
})

// getFenceAgentsRemediation assigns the input to the FenceAgentsRemediation
func getFenceAgentsRemediation(nodeName, agent string, sharedparameters map[v1alpha1.ParameterName]string, nodeparameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string, strategy v1alpha1.RemediationStrategyType) *v1alpha1.FenceAgentsRemediation {
	sharedSecretName := "fence-agents-credentials-shared"
	return &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: defaultNamespace},
		Spec: v1alpha1.FenceAgentsRemediationSpec{
			Agent:            agent,
			SharedParameters: sharedparameters,
			NodeParameters:   nodeparameters,
			// Set the retry count to the minimum for the majority of the tests
			RetryCount:          1,
			RetryInterval:       metav1.Duration{Duration: 5 * time.Second},
			Timeout:             metav1.Duration{Duration: 60 * time.Second},
			RemediationStrategy: strategy,
			SharedSecretName:    &sharedSecretName,
			NodeSecretNames:     map[v1alpha1.NodeName]string{v1alpha1.NodeName(nodeName): fmt.Sprintf("fence-agents-credentials-node-%s", nodeName)},
		},
	}
}

// generateSecret assigns the input to the FenceAgentsRemediation
func generateSecret(secretName string, secretData map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: defaultNamespace},
		Data:       secretData,
		Type:       corev1.SecretType("Opaque"),
	}
}

// buildPod builds a dummy pod
func buildPod(containerName, podName, nodeName string) *corev1.Pod {
	pod := &corev1.Pod{}
	pod.Name = podName
	if podName == farPodName {
		// only when we build FAR pod then we add its label
		pod.Labels = faPodLabels
	} else {
		// testedPod should reside in unhealthy node
		pod.Spec.NodeName = nodeName
	}
	pod.Namespace = defaultNamespace
	container := corev1.Container{
		Name:  containerName,
		Image: "foo",
	}
	pod.Spec.Containers = []corev1.Container{container}
	return pod
}

// createRunningPod builds new pod format, create it, and set its status as running
func createRunningPod(containerName, podName, nodeName string) *corev1.Pod {
	pod := buildPod(containerName, podName, nodeName)
	Expect(k8sClient.Create(context.Background(), pod)).To(Succeed())
	pod.Status.Phase = corev1.PodRunning
	Expect(k8sClient.Status().Update(context.Background(), pod)).To(Succeed())
	return pod
}

// cleanupTestedResources fetches all the resources that we have crated for the test
// and if they are still exist at the end of the test, then we clean them up for next test
func cleanupTestedResources(pod *corev1.Pod) {
	podTest := &corev1.Pod{}
	key := client.ObjectKeyFromObject(pod)
	if err := k8sClient.Get(context.Background(), key, podTest); err == nil {
		log.Info("Cleanup: clean pod", "pod name", podTest.Name)

		// Delete the resource immediately
		var force client.GracePeriodSeconds = 0
		Expect(k8sClient.Delete(context.Background(), podTest, force)).To(Succeed())
	}
}

// verifyPodDeleted verifies whether the pod no longer exists for successful FAR CR
func verifyPodDeleted(podName string) {
	pod := &corev1.Pod{}
	podKey := client.ObjectKey{
		Namespace: defaultNamespace,
		Name:      podName,
	}
	EventuallyWithOffset(1, func() bool {
		err := k8sClient.Get(context.Background(), podKey, pod)
		return apierrors.IsNotFound(err)
	}, timeoutPostRemediation, pollInterval).Should(BeTrue())
	log.Info("Pod not longer exists", "pod", podName)
}

// verifyPodExists verifies whether the pod exists and was not deleted
func verifyPodExists(podName string) {
	pod := &corev1.Pod{}
	podKey := client.ObjectKey{
		Namespace: defaultNamespace,
		Name:      podName,
	}
	ConsistentlyWithOffset(1, func() bool {
		err := k8sClient.Get(context.Background(), podKey, pod)
		return apierrors.IsNotFound(err)
	}, timeoutPostRemediation, pollInterval).Should(BeFalse())
	log.Info("Pod exists", "pod", podName)
}

// verifyStatusCondition checks if the status condition is not set, and if it is set then it has an expected value
func verifyStatusCondition(far *v1alpha1.FenceAgentsRemediation, conditionType string, conditionStatus *metav1.ConditionStatus) {
	Eventually(func(g Gomega) {
		//g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), far)).To(Succeed())
		condition := meta.FindStatusCondition(far.Status.Conditions, conditionType)
		if conditionStatus == nil {
			g.Expect(condition).To(BeNil(), "expected condition %v to not be set", conditionType)
		} else {
			g.Expect(condition).ToNot(BeNil(), "expected condition %v to be set", conditionType)
			g.Expect(condition.Status).To(Equal(*conditionStatus), "expected condition %v to have status %v", conditionType, *conditionStatus)
		}
	}, timeoutPostRemediation, pollInterval).Should(Succeed())
}

// verifyPreRemediationSucceed checks if the remediation CR already has a finazliaer and a remediation taint
func verifyPreRemediationSucceed(underTestFAR *v1alpha1.FenceAgentsRemediation, namespace string, taint *corev1.Taint) *v1alpha1.FenceAgentsRemediation {
	By("Searching for finalizer ")
	Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: underTestFAR.GetName(), Namespace: namespace}, underTestFAR)).To(Succeed())
	Expect(controllerutil.ContainsFinalizer(underTestFAR, v1alpha1.FARFinalizer)).To(BeTrue())
	verifyEvent(corev1.EventTypeNormal, utils.EventReasonRemediationStarted, utils.EventMessageRemediationStarted)

	By("Searching for remediation taint if we have a finalizer")
	Eventually(func(g Gomega) {
		node := &corev1.Node{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{Name: getNodeName(underTestFAR)}, node)).To(Succeed())
		g.Expect(utils.TaintExists(node.Spec.Taints, taint)).To(BeTrue(), "remediation taint should exist")
	}, timeoutPreRemediation, pollInterval).Should(Succeed())
	verifyEvent(corev1.EventTypeNormal, utils.EventReasonAddRemediationTaint, utils.EventMessageAddRemediationTaint)

	return underTestFAR
}

func verifyEvent(eventType, eventReason, eventMessage string) {
	eventText := fmt.Sprintf(eventExist, eventReason)
	By(eventText)
	isEventMatch := isEventOccurred(eventType, eventReason, eventMessage)
	ExpectWithOffset(1, isEventMatch).To(BeTrue())
}

func verifyNoEvent(eventType, eventReason, eventMessage string) {
	eventText := fmt.Sprintf(eventNotExist, eventReason)
	By(eventText)
	isEventMatch := isEventOccurred(eventType, eventReason, eventMessage)
	ExpectWithOffset(1, isEventMatch).To(BeFalse())
}

// isEventOccurred checks whether an event has occoured
func isEventOccurred(eventType, eventReason, eventMessage string) bool {
	expected := fmt.Sprintf("%s %s [remediation] %s", eventType, eventReason, eventMessage)
	isEventMatch := false

	unMatchedEvents := make(chan string, len(fakeRecorder.Events))
	isDone := false
	for {
		select {
		case event := <-fakeRecorder.Events:
			if isEventMatch = event == expected; isEventMatch {
				isDone = true
			} else {
				unMatchedEvents <- event
			}
		default:
			isDone = true
		}
		if isDone {
			break
		}
	}

	close(unMatchedEvents)
	for unMatchedEvent := range unMatchedEvents {
		fakeRecorder.Events <- unMatchedEvent
	}
	return isEventMatch
}

// clearEvents loop over the events channel until it is empty from events
func clearEvents() {
	for len(fakeRecorder.Events) > 0 {
		<-fakeRecorder.Events
	}
	log.Info("Cleanup: events list is empty")
}

func verifyRemediationConditions(far *v1alpha1.FenceAgentsRemediation, processingTypeConditionStatus, fenceAgentSuccededTypeConditionStatus, succededTypeConditionStatus *metav1.ConditionStatus) {
	EventuallyWithOffset(1, func(g Gomega) {
		farCR := &v1alpha1.FenceAgentsRemediation{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(far), farCR)).To(Succeed())
		g.Expect(farCR.Status.LastUpdateTime).ToNot(BeNil())
		verifyStatusCondition(farCR, commonConditions.ProcessingType, processingTypeConditionStatus)
		verifyStatusCondition(farCR, utils.FenceAgentActionSucceededType, fenceAgentSuccededTypeConditionStatus)
		verifyStatusCondition(farCR, commonConditions.SucceededType, succededTypeConditionStatus)
	})
}

// cleanupFar deletes the FAR CR and waits until it is deleted. The function ignores if the CR is already deleted.
func cleanupFar(ctx context.Context, far *v1alpha1.FenceAgentsRemediation) error {
	cr := &v1alpha1.FenceAgentsRemediation{}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(far), cr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	var force client.GracePeriodSeconds = 0
	if err := k8sClient.Delete(ctx, cr, force); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	ConsistentlyWithOffset(1, func() error {
		deleteErr := k8sClient.Get(ctx, client.ObjectKeyFromObject(far), cr)
		if apierrors.IsNotFound(deleteErr) {
			// when trying to create far CR with invalid name
			log.Info("Cleanup: Got error 404", "name", cr.Name)
			return nil
		}
		return deleteErr
	}, pollInterval, timeoutPostRemediation).Should(BeNil(), "CR should be deleted")
	return nil
}
