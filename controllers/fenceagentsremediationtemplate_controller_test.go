/*
Copyright 2025.
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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
)

var _ = Describe("FART Controller", func() {
	var fart *v1alpha1.FenceAgentsRemediationTemplate

	Context("Fence Status Validation", func() {
		JustBeforeEach(func() {
			Expect(k8sClient.Create(context.Background(), fart)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), fart) })
		})
		When("Single node status is ok", func() {
			BeforeEach(func() {
				fart = &v1alpha1.FenceAgentsRemediationTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tmpl-happy",
						Namespace: defaultNamespace,
					},
					Spec: v1alpha1.FenceAgentsRemediationTemplateSpec{
						Template: v1alpha1.FenceAgentsRemediationTemplateResource{
							Spec: v1alpha1.FenceAgentsRemediationSpec{
								Agent: "fence_ipmilan",
								SharedParameters: map[v1alpha1.ParameterName]string{
									"--username": "admin",
									"--password": "password",
								},
								NodeParameters: map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{
									"--ip": {"worker-1": cli.SuccessfulStatusCheckIp},
								},
							},
						},
					},
				}
			})
			It("Status validation should succeed", func() {
				verifySuccess(fart)
			})
		})
		When("2/3  nodes status isn't ok", func() {
			BeforeEach(func() {
				statusValidationTimeout = 3 * time.Second
				fart = &v1alpha1.FenceAgentsRemediationTemplate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tmpl-mixed",
						Namespace: defaultNamespace,
					},
					Spec: v1alpha1.FenceAgentsRemediationTemplateSpec{
						Template: v1alpha1.FenceAgentsRemediationTemplateResource{
							Spec: v1alpha1.FenceAgentsRemediationSpec{
								Agent: "fence_ipmilan",
								SharedParameters: map[v1alpha1.ParameterName]string{
									"--username": "admin",
									"--password": "password",
								},
								NodeParameters: map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{
									"--ip": {
										"worker-1": cli.SuccessfulStatusCheckIp, // success
										"worker-2": cli.TimedOutStatusCheckIp,   // timeout
										"worker-3": cli.OffStatusCheckIp,        // not-ON
									},
								},
							},
						},
					},
				}
			})
			It("failed status should report correctly", func() {
				Eventually(func(g Gomega) {
					updated := &v1alpha1.FenceAgentsRemediationTemplate{}
					g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(fart), updated)).To(Succeed())

					cond := meta.FindStatusCondition(updated.Status.Conditions, ConditionParametersValidation)
					g.Expect(cond).NotTo(BeNil())
					g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(cond.Reason).To(Equal(ReasonValidationFailed))

					g.Expect(updated.Status.ValidationFailures).To(HaveLen(2))
					// worker-2 timed out
					g.Expect(updated.Status.ValidationFailures).To(HaveKey("worker-2"))
					g.Expect(updated.Status.ValidationFailures["worker-2"]).To(ContainSubstring("timed out"))
					// worker-3 not ON
					g.Expect(updated.Status.ValidationFailures).To(HaveKey("worker-3"))
					g.Expect(updated.Status.ValidationFailures["worker-3"]).To(ContainSubstring("not ON"))
				}, "12s", "200ms").Should(Succeed())
			})
			Context("StatusValidationSample configuration ", func() {
				When("StatusValidationSample is set to check one node", func() {
					BeforeEach(func() {
						fart.Spec.Template.Spec.StatusValidationSample = &intstr.IntOrString{Type: intstr.Int, IntVal: 1}
					})
					It("Status validation should succeed", func() {
						verifySuccess(fart)
					})
				})
				When("StatusValidationSample is set to check 66% of the nodes", func() {
					BeforeEach(func() {
						fart.Spec.Template.Spec.StatusValidationSample = &intstr.IntOrString{Type: intstr.String, StrVal: "66%"}
					})
					It("Status validation should fails for node 2 and omit for node 3", func() {
						Eventually(func(g Gomega) {
							updated := &v1alpha1.FenceAgentsRemediationTemplate{}
							g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(fart), updated)).To(Succeed())

							cond := meta.FindStatusCondition(updated.Status.Conditions, ConditionParametersValidation)
							g.Expect(cond).NotTo(BeNil())
							g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
							g.Expect(cond.Reason).To(Equal(ReasonValidationFailed))

							g.Expect(updated.Status.ValidationFailures).To(HaveLen(1))
							// worker-2 timed out
							g.Expect(updated.Status.ValidationFailures).To(HaveKey("worker-2"))
							g.Expect(updated.Status.ValidationFailures["worker-2"]).To(ContainSubstring("timed out"))
							// worker-3 not ON
						}, "12s", "200ms").Should(Succeed())
					})
				})
			})
		})
	})

})

func verifySuccess(fart *v1alpha1.FenceAgentsRemediationTemplate) {
	Eventually(func(g Gomega) {
		updated := &v1alpha1.FenceAgentsRemediationTemplate{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(fart), updated)).To(Succeed())

		g.Expect(updated.Status.ValidationFailures).To(BeEmpty())

		cond := meta.FindStatusCondition(updated.Status.Conditions, ConditionParametersValidation)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(cond.Reason).To(Equal(ReasonValidationSucceeded))
	}, "5s", "200ms").Should(Succeed())
}
