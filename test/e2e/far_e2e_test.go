package e2e

import (
	"context"
	"math/rand"
	"os"
	"time"

	commonConditions "github.com/medik8s/common/pkg/conditions"
	medik8sLabels "github.com/medik8s/common/pkg/labels"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
	e2eUtils "github.com/medik8s/fence-agents-remediation/test/e2e/utils"
)

const (
	nodeIdentifierPrefixAWS  = "--plug"
	nodeIdentifierPrefixIPMI = "--ipport"
	testContainerName        = "test-container"
	testPodName              = "test-pod"
	fenceAgentDefaultAction  = "reboot"
	//TODO: try to minimize timeout
	// eventually parameters
	timeoutTaint                = "20s"   // Timeout for checking the FAR taint
	timeoutReboot               = "6m0s"  // fencing with reboot should be completed within 6 minutes
	timeoutPowerOff             = "10m0s" // fencing with off should be completed within 10 minutes
	timeoutAfterFenceAction     = "5m0s"  // Timeout for verifying steps after fencing.
	timeoutForRemediationChecks = "5s"    // Timeout for remediation checks
	pollTaint                   = "100ms"
	pollReboot                  = "1s"
	pollPowerOff                = "10s"
	pollAfterFenceAction        = "10s"
	pollForRemediationChecks    = "250ms"
	skipOOSREnvVarName          = "SKIP_OOST_REMEDIATION_VERIFICATION"

	// test Action
	reboot = "reboot"
	off    = "off"
)

var (
	stopTesting                      bool
	remediationTimes                 []time.Duration
	fenceAgent, nodeIdentifierPrefix string
	clusterPlatform                  *configv1.Infrastructure
)

var _ = Describe("FAR E2e", func() {
	var (
		testShareParam map[v1alpha1.ParameterName]string
		testNodeParam  map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string
	)
	BeforeEach(func() {
		testShareParam = buildSharedParameters(clusterPlatform, fenceAgentDefaultAction)
		var err error
		testNodeParam, err = buildNodeParameters()
		Expect(err).ToNot(HaveOccurred(), "can't get node information")
	})

	// runFARTests is a utility function to run FAR tests.
	// It accepts a remediation strategy and a condition to determine if the tests should be skipped.
	runFARTests := func(remediationStrategy v1alpha1.RemediationStrategyType, testAction string, skipCondition func() bool) {
		var (
			availableWorkerNodes          *corev1.NodeList
			selectedNode                  *corev1.Node
			nodeName                      string
			pod                           *corev1.Pod
			startTime, nodeBootTimeBefore time.Time
			skipRemediationCreation       bool
		)
		BeforeEach(func() {
			skipRemediationCreation = false
			if stopTesting {
				Skip("Skip testing due to unsupported platform")
			}
			if skipCondition() {
				Skip("Skip this block due to unsupported condition")
			}

			if availableWorkerNodes == nil {
				availableWorkerNodes = getReadyWorkerNodes()
			}
			if len(availableWorkerNodes.Items) < 1 {
				Fail("There isn't an available (and Ready) worker node in the cluster")
			}

			selectedNode = pickRemediatedNode(availableWorkerNodes)
			nodeName = selectedNode.Name
			printNodeDetails(selectedNode, nodeIdentifierPrefix, testNodeParam)

			var err error
			// save the node's boot time prior to the fence agent call
			nodeBootTimeBefore, err = e2eUtils.GetBootTime(clientSet, nodeName, testNsName, log)
			Expect(err).ToNot(HaveOccurred(), "failed to get boot time of the node")

		})
		JustBeforeEach(func() {
			if skipRemediationCreation {
				return
			}
			// create tested pod which will be deleted by the far CR
			pod = createTestedPod(nodeName)
			DeferCleanup(cleanupTestedResources, pod)

			// set the node as "unhealthy" by disabling kubelet
			makeNodeUnready(selectedNode)
			testShareParam["--action"] = testAction

			startTime = time.Now()
			far := createFAR(nodeName, fenceAgent, testShareParam, testNodeParam, remediationStrategy)

			DeferCleanup(deleteFAR, far)
			// The node needs to be powered on after 'off' remediation
			if testAction == "off" {
				DeferCleanup(powerOnNodeAndWaitUntilReady, far, selectedNode)
			}
		})
		When("running FAR to remediate a node with secrets in shared parameters (legacy)", func() {
			BeforeEach(func() {
				testShareParam = addSecretsToSharedParams(testShareParam)
			})
			It("should successfully remediate the node", func() {
				checkRemediation(nodeName, nodeBootTimeBefore, pod, remediationStrategy, testAction)
				remediationTimes = append(remediationTimes, time.Since(startTime))
			})
		})

		When("running FAR with credentials in Secret", func() {
			BeforeEach(func() {
				secret := generateSecretResource()
				Expect(k8sClient.Create(context.TODO(), secret)).To(Succeed())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(context.TODO(), secret)).To(Succeed())
				})
			})
			It("should successfully remediate the node", func() {
				checkRemediation(nodeName, nodeBootTimeBefore, pod, remediationStrategy, testAction)
				remediationTimes = append(remediationTimes, time.Since(startTime))
			})
		})

		When("Trying to create or update to an invalid FAR/T CR", func() {
			BeforeEach(func() {
				// since we test here the webhook we are skipping remediation creation in order to make sure failed nodes wouldn't affect the tested webhooks
				skipRemediationCreation = true
				testShareParam = addSecretsToSharedParams(testShareParam)
			})
			It("it should fail", func() {
				far := &v1alpha1.FenceAgentsRemediation{
					ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: operatorNsName},
					Spec: v1alpha1.FenceAgentsRemediationSpec{
						Agent:               fenceAgent,
						SharedParameters:    nil,
						NodeParameters:      nil,
						RemediationStrategy: remediationStrategy,
						RetryCount:          10,
						RetryInterval:       metav1.Duration{Duration: 20 * time.Second},
						Timeout:             metav1.Duration{Duration: 60 * time.Second},
					},
				}
				Expect(k8sClient.Create(context.Background(), far)).To(MatchError(ContainSubstring("invalid spec: mandatory parameters are missing")), "update to invalid far without any params should be prevented")

				emptyParamsSpec := v1alpha1.FenceAgentsRemediationSpec{
					Agent:               fenceAgent,
					SharedParameters:    nil,
					NodeParameters:      nil,
					RemediationStrategy: remediationStrategy,
					RetryCount:          10,
					RetryInterval:       metav1.Duration{Duration: 20 * time.Second},
					Timeout:             metav1.Duration{Duration: 60 * time.Second},
				}
				fart := &v1alpha1.FenceAgentsRemediationTemplate{ObjectMeta: metav1.ObjectMeta{Name: "invalid-fart", Namespace: operatorNsName}}
				fart.Spec = v1alpha1.FenceAgentsRemediationTemplateSpec{Template: v1alpha1.FenceAgentsRemediationTemplateResource{Spec: emptyParamsSpec}}
				Expect(k8sClient.Create(context.Background(), fart)).To(MatchError(ContainSubstring("invalid spec: mandatory parameters are missing")), "create fart without any params should be prevented")

			})
		})

	}

	Context("stress cluster with ResourceDeletion remediation strategy under reboot scenario", func() {
		runFARTests(v1alpha1.ResourceDeletionRemediationStrategy, reboot, func() bool { return false })
	})

	Context("stress cluster with OutOfServiceTaint remediation strategy under reboot scenario", func() {
		runFARTests(v1alpha1.OutOfServiceTaintRemediationStrategy, reboot, func() bool {
			_, isExist := os.LookupEnv(skipOOSREnvVarName)
			return isExist
		})
	})

	Context("stress cluster with ResourceDeletion remediation strategy under power-off scenario", func() {
		runFARTests(v1alpha1.ResourceDeletionRemediationStrategy, off, func() bool { return false })
	})

	Context("stress cluster with OutOfServiceTaint remediation strategy under power-off scenario", func() {
		runFARTests(v1alpha1.OutOfServiceTaintRemediationStrategy, off, func() bool {
			_, isExist := os.LookupEnv(skipOOSREnvVarName)
			return isExist
		})
	})
})

var _ = AfterSuite(func() {
	if len(remediationTimes) > 0 {
		averageTimeDuration := 0.0
		for _, remTime := range remediationTimes {
			averageTimeDuration += remTime.Seconds()
			log.Info("Remediation was finished", "remediation time", remTime)
		}
		averageTime := int(averageTimeDuration) / len(remediationTimes)
		log.Info("Average remediation time", "minutes", averageTime/60, "seconds", averageTime%60)
	}
})

// buildSharedParameters returns a map key-value of shared parameters based on cluster platform type if it finds the credentials, otherwise an error
func buildSharedParameters(clusterPlatform *configv1.Infrastructure, action string) map[v1alpha1.ParameterName]string {
	var testShareParam map[v1alpha1.ParameterName]string

	// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.status.platformStatus.type}'
	clusterPlatformType := clusterPlatform.Status.PlatformStatus.Type
	if clusterPlatformType == configv1.AWSPlatformType {

		// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.status.platformStatus.aws.region}'
		regionAWS := clusterPlatform.Status.PlatformStatus.AWS.Region

		testShareParam = map[v1alpha1.ParameterName]string{
			"--region":          regionAWS,
			"--action":          action,
			"--skip-race-check": "",
			// "--verbose":    "", // for verbose result
		}
	} else if clusterPlatformType == configv1.BareMetalPlatformType {
		testShareParam = map[v1alpha1.ParameterName]string{
			"--ip":      "192.168.111.1",
			"--action":  action,
			"--lanplus": "",
		}
	}
	return testShareParam
}

// buildNodeParameters returns a map key-value of node parameters based on cluster platform type if it finds the node info list, otherwise an error
func buildNodeParameters() (map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string, error) {
	var (
		nodeListParam  map[v1alpha1.NodeName]string
		nodeIdentifier v1alpha1.ParameterName
		err            error
	)
	clusterPlatformType := clusterPlatform.Status.PlatformStatus.Type
	if clusterPlatformType == configv1.AWSPlatformType {
		nodeListParam, err = e2eUtils.GetAWSNodeInfoList(machineClient)
		if err != nil {
			log.Info("Can't get nodes' information - AWS instance ID is missing")
			return nil, err
		}
		nodeIdentifier = nodeIdentifierPrefixAWS

	} else if clusterPlatformType == configv1.BareMetalPlatformType {
		nodeListParam, err = e2eUtils.GetBMHNodeInfoList(machineClient)
		if err != nil {
			log.Info("Can't get nodes' information - ports are missing")
			return nil, err
		}
		nodeIdentifier = nodeIdentifierPrefixIPMI
	}
	testNodeParam := map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string{nodeIdentifier: nodeListParam}
	return testNodeParam, nil
}

// getReadyWorkerNodes returns a list of ready worker nodes in the cluster if any
func getReadyWorkerNodes() *corev1.NodeList {
	availableWorkerNodes := &corev1.NodeList{}
	selector := labels.NewSelector()
	requirement, err := labels.NewRequirement(medik8sLabels.WorkerRole, selection.Exists, []string{})
	Expect(err).To(BeNil())
	selector = selector.Add(*requirement)
	Expect(k8sClient.List(context.Background(), availableWorkerNodes, &client.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())

	// Filter nodes to only include those in "Ready" state
	readyWorkerNodes := &corev1.NodeList{}
	for _, node := range availableWorkerNodes.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				readyWorkerNodes.Items = append(readyWorkerNodes.Items, node)
				break // "Ready" was found
			}
		}
	}
	return readyWorkerNodes
}

// pickRemediatedNode randomly returns a next remediated node from the current available nodes,
// and then the node is removed from the list of available nodes
func pickRemediatedNode(availableNodes *corev1.NodeList) *corev1.Node {
	// Generate a random seed based on the current time
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	// Randomly select a worker node
	selectedNodeIndex := r.Intn(len(availableNodes.Items))
	selectedNode := availableNodes.Items[selectedNodeIndex]
	// Delete the selected node from the list of available nodes
	availableNodes.Items = append(availableNodes.Items[:selectedNodeIndex], availableNodes.Items[selectedNodeIndex+1:]...)
	return &selectedNode
}

// createTestedPod creates tested pod which will be deleted by the far CR
func createTestedPod(nodeName string) *corev1.Pod {
	pod := e2eUtils.GetPod(nodeName, testContainerName)
	pod.Name = testPodName
	pod.Namespace = testNsName
	pod.Spec.Tolerations = []corev1.Toleration{
		{
			Key:      v1alpha1.FARNoExecuteTaintKey,
			Operator: corev1.TolerationOpExists,
			Effect:   corev1.TaintEffectNoExecute,
		},
	}
	Expect(k8sClient.Create(context.Background(), pod)).To(Succeed())

	return pod
}

// printNodeDetail prints the node details
func printNodeDetails(selectedNode *corev1.Node, nodeIdentifierPrefix string, testNodeParam map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string) {
	nodeNameParam := v1alpha1.NodeName(selectedNode.Name)
	parameterName := v1alpha1.ParameterName(nodeIdentifierPrefix)
	testNodeID := testNodeParam[parameterName][nodeNameParam]
	log.Info("Testing Node", "Node name", selectedNode.Name, "Node ID", testNodeID)
}

// createFAR assigns the input to FenceAgentsRemediation object, creates CR, and returns the CR object
func createFAR(nodeName string, agent string, sharedParameters map[v1alpha1.ParameterName]string, nodeParameters map[v1alpha1.ParameterName]map[v1alpha1.NodeName]string, strategy v1alpha1.RemediationStrategyType) *v1alpha1.FenceAgentsRemediation {
	far := &v1alpha1.FenceAgentsRemediation{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: operatorNsName},
		Spec: v1alpha1.FenceAgentsRemediationSpec{
			Agent:               agent,
			SharedParameters:    sharedParameters,
			NodeParameters:      nodeParameters,
			RemediationStrategy: strategy,
			RetryCount:          10,
			RetryInterval:       metav1.Duration{Duration: 20 * time.Second},
			Timeout:             metav1.Duration{Duration: 60 * time.Second},
		},
	}
	ExpectWithOffset(1, k8sClient.Create(context.Background(), far)).ToNot(HaveOccurred())
	return far
}

// deleteFAR deletes the CR with offset
func deleteFAR(far *v1alpha1.FenceAgentsRemediation) {
	EventuallyWithOffset(1, func() error {
		err := k8sClient.Delete(context.Background(), far)
		if apiErrors.IsNotFound(err) {
			return nil
		}
		return err
	}, 2*time.Minute, 10*time.Second).ShouldNot(HaveOccurred(), "failed to delete far")
}

// cleanupTestedResources deletes an old pod and old va if it was not deleted from FAR CR
func cleanupTestedResources(pod *corev1.Pod) {
	newPod := &corev1.Pod{}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), newPod); err == nil {
		// set GracePeriodSeconds to 0 to delete the pod immediately
		var force client.GracePeriodSeconds = 0
		Expect(k8sClient.Delete(context.Background(), newPod, force)).To(Succeed())
		log.Info("cleanup: Pod has not been deleted by remediation", "pod name", pod.Name)
	}
}

// wasTaintAdded checks whether the specified taint was added to the tested node
func wasTaintAdded(taint corev1.Taint, nodeName string) {
	var node *corev1.Node
	Eventually(func(g Gomega) bool {
		var err error
		node, err = utils.GetNodeWithName(k8sClient, nodeName)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(node).ToNot(BeNil())
		return utils.TaintExists(node.Spec.Taints, &taint)
	}, timeoutTaint, pollTaint).Should(BeTrue())
	log.Info("Taint was added", "node name", node.Name, "taint key", taint.Key, "taint effect", taint.Effect)
}

// waitForNodeHealthyCondition waits until the node's ready condition matches the given status, and it fails after timeout
func waitForNodeHealthyCondition(node *corev1.Node, condStatus corev1.ConditionStatus) {
	Eventually(func(g Gomega) corev1.ConditionStatus {
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(node), node)).To(Succeed())
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				return cond.Status
			}
		}
		return "failure"
	}, timeoutReboot, pollReboot).Should(Equal(condStatus))
}

// makeNodeUnready stops kubelet and wait for the node condition to be not ready unless the node was already unready
func makeNodeUnready(node *corev1.Node) {
	log.Info("making node unready", "node name", node.GetName())
	// check if node is unready already
	Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(node), node)).To(Succeed())
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionUnknown {
			log.Info("node is already unready", "node name", node.GetName())
			return
		}
	}

	Expect(e2eUtils.StopKubelet(clientSet, node.Name, testNsName, log)).To(Succeed())
	waitForNodeHealthyCondition(node, corev1.ConditionUnknown)
	log.Info("node is unready", "node name", node.GetName())
}

// verifyNodeRebooted waits until there is a newer boot time than before, a reboot occurred, otherwise it falls with an error
func verifyNodeRebooted(nodeName string, nodeBootTimeBefore time.Time) {
	log.Info("checking if Node was rebooted", "node", nodeName, "previous boot time", nodeBootTimeBefore)
	var nodeBootTimeAfter time.Time
	Eventually(func() (time.Time, error) {
		var errBootAfter error
		nodeBootTimeAfter, errBootAfter = e2eUtils.GetBootTime(clientSet, nodeName, testNsName, log)
		if errBootAfter != nil {
			log.Error(errBootAfter, "Can't get boot time of the node")
		}
		return nodeBootTimeAfter, errBootAfter
	}, timeoutReboot, pollReboot).Should(
		BeTemporally(">", nodeBootTimeBefore), "Timeout for node reboot has passed, even though FAR CR has been created")

	log.Info("successful reboot", "node", nodeName, "offset between last boot", nodeBootTimeAfter.Sub(nodeBootTimeBefore), "new boot time", nodeBootTimeAfter)
}

// verifyNodePoweredOff checks if the node remains powered off
func verifyNodePoweredOff(nodeName string) {
	log.Info("checking if Node was powered off", "node", nodeName)
	var nodePowerStatus string

	Eventually(func() (string, error) {
		var err error
		nodePowerStatus, err = e2eUtils.GetPowerStatus(machineClient, nodeName)
		if err != nil {
			log.Error(err, "Can't get power status of the node")
		}
		return nodePowerStatus, err
	}, timeoutPowerOff, pollReboot).Should(Equal("stopped"))

	log.Info("Successfully confirmed node is powered off", "node", nodeName)
}

// checkPodDeleted verifies if the pod has already been deleted due to resource deletion
func checkPodDeleted(pod *corev1.Pod) {
	Eventually(func() bool {
		newPod := &corev1.Pod{}
		err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(pod), newPod)
		return apiErrors.IsNotFound(err)
	}, timeoutAfterFenceAction, pollAfterFenceAction).Should(BeTrue())

	log.Info("Pod has already been deleted", "pod name", pod.Name)
}

// verifyStatusCondition checks if the status condition is not set, and if it is set then it has an expected value
func verifyStatusCondition(nodeName, conditionType string, conditionStatus *metav1.ConditionStatus) {
	far := &v1alpha1.FenceAgentsRemediation{}
	farNamespacedName := client.ObjectKey{Name: nodeName, Namespace: operatorNsName}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(context.Background(), farNamespacedName, far)).To(Succeed())
		condition := meta.FindStatusCondition(far.Status.Conditions, conditionType)
		if conditionStatus == nil {
			g.Expect(condition).To(BeNil(), "expected condition %v to not be set", conditionType)
		} else {
			g.Expect(condition).ToNot(BeNil(), "expected condition %v to be set", conditionType)
			g.Expect(condition.Status).To(Equal(*conditionStatus), "expected condition %v to have status %v", conditionType, *conditionStatus)
		}
	}, timeoutForRemediationChecks, pollForRemediationChecks).Should(Succeed())
}

// checkRemediation verify whether the node was remediated
func checkRemediation(nodeName string, nodeBootTimeBefore time.Time, pod *corev1.Pod, strategy v1alpha1.RemediationStrategyType, testAction string) {

	By("Check if FAR NoExecute taint was added")
	wasTaintAdded(utils.CreateRemediationTaint(), nodeName)

	if testAction == reboot {
		By("Getting new node's boot time")
		verifyNodeRebooted(nodeName, nodeBootTimeBefore)
	} else if testAction == off {
		By("Check if the node powered off")
		verifyNodePoweredOff(nodeName)
	}

	if strategy == v1alpha1.OutOfServiceTaintRemediationStrategy {
		By("Check if out-of-service taint was added")
		wasTaintAdded(utils.CreateOutOfServiceTaint(), nodeName)
	}

	By("checking if old pod has been deleted")
	checkPodDeleted(pod)

	By("checking if the status conditions match a successful remediation")
	conditionStatusPointer := func(status metav1.ConditionStatus) *metav1.ConditionStatus { return &status }
	verifyStatusCondition(nodeName, commonConditions.ProcessingType, conditionStatusPointer(metav1.ConditionFalse))
	verifyStatusCondition(nodeName, utils.FenceAgentActionSucceededType, conditionStatusPointer(metav1.ConditionTrue))
	verifyStatusCondition(nodeName, commonConditions.SucceededType, conditionStatusPointer(metav1.ConditionTrue))
}

// preTestsSetup will initialize values with are required in all of the tests before the suite is run
func preTestsSetup() {
	//Building the params once for all of the tests
	var err error
	clusterPlatform, err = e2eUtils.GetClusterInfo(configClient)
	Expect(err).ToNot(HaveOccurred(), "can't identify the cluster platform")
	log.Info("Getting Cluster Information", "Cluster name", clusterPlatform.Name, "PlatformType", string(clusterPlatform.Status.PlatformStatus.Type))

	//Set up the proper fence agent so we can use the param names that match the agents
	setFenceAgentParams(clusterPlatform.Status.PlatformStatus.Type)

	//Populate the secret map which will be retrieved according to the cluster type
	secretMap, err = buildSecretMap(clusterPlatform)
	Expect(err).ToNot(HaveOccurred())

}

func setFenceAgentParams(platformType configv1.PlatformType) {
	switch platformType {
	case configv1.AWSPlatformType:
		fenceAgent = e2eUtils.FenceAgentAWS
		nodeIdentifierPrefix = nodeIdentifierPrefixAWS
		By("running fence_aws")
	case configv1.BareMetalPlatformType:
		fenceAgent = e2eUtils.FenceAgentIPMI
		nodeIdentifierPrefix = nodeIdentifierPrefixIPMI
		By("running fence_ipmilan")
	default:
		stopTesting = true // Mark to stop subsequent tests
		Fail("FAR haven't been tested on this kind of cluster (non AWS or BareMetal)")
	}
}

func buildSecretMap(clusterPlatform *configv1.Infrastructure) (map[string]string, error) {
	secrets := map[string]string{}
	// oc get Infrastructure.config.openshift.io/cluster -o jsonpath='{.status.platformStatus.type}'
	if clusterPlatform.Status.PlatformStatus.Type == configv1.AWSPlatformType {
		accessKey, secretKey, err := e2eUtils.GetSecretData(clientSet, e2eUtils.AWSSecretName, e2eUtils.AWSSecretNamespace, e2eUtils.AWSAccessKeyID, e2eUtils.AWSSecretAccessKey)
		if err != nil {
			log.Info("Can't get AWS credentials")
			return nil, err
		}
		secrets["--access-key"] = accessKey
		secrets["--secret-key"] = secretKey

	} else if clusterPlatform.Status.PlatformStatus.Type == configv1.BareMetalPlatformType {
		//TODO: secret BM should be based on node name - > oc get bmh -n openshift-machine-api BM_NAME -o jsonpath='{.spec.bmc.credentialsName}'
		secretBMHName := "ostest-master-0-bmc-secret"
		// TODO : get ip from GetCredientals
		// oc get bmh -n openshift-machine-api ostest-master-0 -o jsonpath='{.spec.bmc.address}'
		// then parse ip
		username, password, err := e2eUtils.GetSecretData(clientSet, secretBMHName, e2eUtils.BMHCredentialNamespace, e2eUtils.BMHCredentialUserKey, e2eUtils.BMHCredentialPasswordKey)
		if err != nil {
			log.Info("Can't get BMH credentials")
			return nil, err
		}
		secrets["--username"] = username
		secrets["--password"] = password
	}
	return secrets, nil
}

func addSecretsToSharedParams(testShareParam map[v1alpha1.ParameterName]string) map[v1alpha1.ParameterName]string {
	for key, value := range secretMap {
		testShareParam[v1alpha1.ParameterName(key)] = value
	}
	return testShareParam
}

func generateSecretResource() *corev1.Secret {
	//using shared secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fence-agents-credentials-shared",
			Namespace: operatorNsName,
		},
		StringData: secretMap,
		Type:       corev1.SecretTypeOpaque,
	}
	return secret
}

func powerOnNodeAndWaitUntilReady(far *v1alpha1.FenceAgentsRemediation, selectedNode *corev1.Node) (bool, error) {
	_, err := e2eUtils.PowerOnNode(clientSet, far, selectedNode.Name, log)

	if err != nil {
		return false, err
	}
	waitForNodeHealthyCondition(selectedNode, corev1.ConditionTrue)

	return true, nil
}
