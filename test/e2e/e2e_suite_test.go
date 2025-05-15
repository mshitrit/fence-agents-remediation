package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	machineclient "github.com/openshift/client-go/machine/clientset/versioned"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.
const operatorInstalledNamespcae = "OPERATOR_NS"

var (
	log           logr.Logger
	clientSet     *kubernetes.Clientset
	k8sClient     ctrl.Client
	configClient  configclient.Interface
	machineClient *machineclient.Clientset

	// The ns the operator is running in
	operatorNsName string

	// The ns test pods are started in
	testNsName = "far-test"
	secretMap  map[string]string
)

func TestE2e(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FAR e2e suite")
}

var _ = BeforeSuite(func() {
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseFlagOptions(&opts)))
	log = logf.Log

	operatorNsName = os.Getenv(operatorInstalledNamespcae)
	Expect(operatorNsName).ToNot(BeEmpty(), operatorInstalledNamespcae+" env var not set, can't start e2e test")

	// +kubebuilder:scaffold:scheme

	// Load the Kubernetes configuration from the default location or from a specified kubeconfig file or simply die
	config, err := config.GetConfig()
	if err != nil {
		Fail(fmt.Sprintf("Couldn't get kubeconfig %v", err))
	}

	configClient, err = configclient.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(configClient).NotTo(BeNil())

	// Create a Kubernetes clientset using the configuration
	clientSet, err = kubernetes.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(clientSet).NotTo(BeNil())

	// Create a Machine clientset using the configuration
	machineClient, err = machineclient.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	Expect(machineClient).NotTo(BeNil())

	scheme.AddToScheme(scheme.Scheme)
	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = ctrl.New(config, ctrl.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	os.Setenv("DEPLOYMENT_NAMESPACE", operatorNsName)

	// create test ns
	testNs := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNsName,
			Labels: map[string]string{
				// allow privileged pods in test namespace (disable PSA), needed for API blocker pod
				"pod-security.kubernetes.io/enforce":             "privileged",
				"security.openshift.io/scc.podSecurityLabelSync": "false",
			},
		},
	}
	err = k8sClient.Get(context.Background(), ctrl.ObjectKeyFromObject(testNs), testNs)
	if errors.IsNotFound(err) {
		err = k8sClient.Create(context.Background(), testNs)
	}
	Expect(err).ToNot(HaveOccurred(), "could not get or create test ns")
	preTestsSetup()
	debug()
})

func debug() {
	version, _ := clientSet.ServerVersion()
	fmt.Fprint(GinkgoWriter, version)
}
