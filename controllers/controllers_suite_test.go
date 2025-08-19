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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	//+kubebuilder:scaffold:imports ## https://github.com/kubernetes-sigs/kubebuilder/issues/1487 ?
	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/cli"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const defaultNamespace = "default"

var (
	k8sClient    client.Client
	k8sManager   manager.Manager
	testEnv      *envtest.Environment
	ctx          context.Context
	cancel       context.CancelFunc
	fakeRecorder *record.FakeRecorder

	plogs *peekLogger

	storedCommand []string
	mockError     error
	forcedDelay   time.Duration
)

// peekLogger allows to inspect operator's log for testing purpose.
type peekLogger struct {
	logs []string
}

func (p *peekLogger) Write(b []byte) (n int, err error) {
	n, err = GinkgoWriter.Write(b)
	if err != nil {
		return n, err
	}
	p.logs = append(p.logs, string(b))
	return n, err
}

func (p *peekLogger) Contains(s string) bool {
	for _, log := range p.logs {
		if strings.Contains(log, s) {
			return true
		}
	}
	return false
}

// CountOccurences returns number of occurences of string s in logs.
func (p *peekLogger) CountOccurences(s string) int {
	count := 0
	for _, log := range p.logs {
		if strings.Contains(log, s) {
			count++
		}
	}
	return count
}

func (p *peekLogger) Clear() {
	p.logs = make([]string, 0)
}

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers Suite")
}

var _ = BeforeSuite(func() {
	plogs = &peekLogger{logs: make([]string, 0)}
	opts := zap.Options{
		DestWriter:  plogs,
		Development: true,
		TimeEncoder: zapcore.RFC3339NanoTimeEncoder,
	}
	logf.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	fakeRecorder = record.NewFakeRecorder(30)
	executor := cli.NewFakeExecuter(k8sClient, controlledRun, fakeRecorder)
	os.Setenv("DEPLOYMENT_NAMESPACE", defaultNamespace)

	err = (&FenceAgentsRemediationReconciler{
		Client:   k8sClient,
		Log:      k8sManager.GetLogger().WithName("test far reconciler"),
		Scheme:   k8sManager.GetScheme(),
		Recorder: fakeRecorder,
		Executor: executor,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	templateExecutor := cli.NewFakeExecuter(k8sClient, cli.ControlTemplateRunner, fakeRecorder)
	err = (&FenceAgentsRemediationTemplateReconciler{
		Client:   k8sClient,
		Log:      k8sManager.GetLogger().WithName("test fart reconciler"),
		Scheme:   k8sManager.GetScheme(),
		Recorder: fakeRecorder,
		Executor: templateExecutor,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		// https://github.com/kubernetes-sigs/controller-runtime/issues/1571
		ctx, cancel = context.WithCancel(ctrl.SetupSignalHandler())
		err := k8sManager.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func controlledRun(ctx context.Context, command []string) (stdout, stderr string, err error) {
	storedCommand = command
	if forcedDelay > 0 {
		select {
		case <-ctx.Done():
			return "", "", fmt.Errorf("context expired")
		case <-time.After(forcedDelay):
			log.Info("forced delay expired", "delay", forcedDelay)
		}
	}

	return "", "", mockError
}
