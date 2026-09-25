/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package status

import (
	"bytes"
	"context"
	"errors"
	"log/slog"

	tea "charm.land/bubbletea/v2"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// namespaceObj is the Namespace object every fixture below needs alongside
// its workload objects: WorkloadChecks checks the namespace itself first,
// and the fake clientset (like a real API server) doesn't implicitly
// create a Namespace object just because some other object references its
// name.
func namespaceObj(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

var crdGVR = schema.GroupVersionResource{
	Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
}

var provisioningGVR = schema.GroupVersionResource{
	Group: "metal3.io", Version: "v1alpha1", Resource: "provisionings",
}

var csvGVR = schema.GroupVersionResource{
	Group: "operators.coreos.com", Version: "v1alpha1", Resource: "clusterserviceversions",
}

// listKinds registers every custom-resource GVR any prerequisite check in
// this suite might LIST, so a fake dynamic client doesn't panic --
// client-go's fake dynamic client requires every listed GVR's list kind be
// known up front, even when the list will be empty.
var listKinds = map[schema.GroupVersionResource]string{
	crdGVR:          "CustomResourceDefinitionList",
	provisioningGVR: "ProvisioningList",
	csvGVR:          "ClusterServiceVersionList",
}

func newUnstructuredCRD(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": name},
	}}
}

func newUnstructuredCSV(namespace, name, phase, version string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "operators.coreos.com/v1alpha1",
		"kind":       "ClusterServiceVersion",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec":       map[string]any{"version": version},
		"status":     map[string]any{"phase": phase},
	}}
}

// emptyDynamicClient satisfies clients.Dynamic for tests that don't care
// about prerequisite results (they'd all report Failed, which is fine): the
// fake dynamic client still needs listKinds up front, or engines that LIST
// a GVR with nothing registered for it panic instead of returning an empty
// list.
func emptyDynamicClient() *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds)
}

var _ = Describe("Status command flags", func() {
	It("has the expected use string", func() {
		Expect(Cmd().Use).To(Equal("status --namespace NAMESPACE [FLAG...]"))
	})

	It("has short and long help text", func() {
		cmd := Cmd()
		Expect(cmd.Short).ToNot(BeEmpty())
		Expect(cmd.Long).ToNot(BeEmpty())
	})

	It("rejects positional arguments", func() {
		cmd := Cmd()
		Expect(cmd.Args(cmd, []string{"unexpected"})).To(HaveOccurred())
	})
})

var _ = Describe("Status command execution", func() {
	var (
		ctx    context.Context
		stdout *bytes.Buffer
		stderr *bytes.Buffer
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctx = logging.LoggerIntoContext(ctx, slog.Default())
		stdout = &bytes.Buffer{}
		stderr = &bytes.Buffer{}

		console, err := terminal.NewConsole().
			SetLogger(slog.Default()).
			SetStdout(stdout).
			SetStderr(stderr).
			Build()
		Expect(err).ToNot(HaveOccurred())
		ctx = terminal.ConsoleIntoContext(ctx, console)
	})

	It("requires --namespace", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{Typed: fake.NewSimpleClientset()}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{})

		err := cmd.Execute()

		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.Code()).To(Equal(1))
		Expect(stderr.String()).To(ContainSubstring("--namespace is required"))
	})

	It("prints a one-shot status view and exits cleanly, without --watch", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{
					Typed: fake.NewSimpleClientset(
						&appsv1.Deployment{
							ObjectMeta: metav1.ObjectMeta{Name: "fulfillment-grpc-server", Namespace: "osac"},
							Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
						},
						namespaceObj("osac"),
					),
					Dynamic: emptyDynamicClient(),
				}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--namespace=osac"})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("ready"))
		Expect(stdout.String()).To(ContainSubstring("fulfillment-grpc-server"))
	})

	It("never fails the command on an unready workload -- status is a report, not a gate", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{
					Typed: fake.NewSimpleClientset(
						&appsv1.Deployment{
							ObjectMeta: metav1.ObjectMeta{Name: "osac-ui", Namespace: "osac"},
							Status:     appsv1.DeploymentStatus{ReadyReplicas: 0},
						},
						namespaceObj("osac"),
					),
					Dynamic: emptyDynamicClient(),
				}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--namespace=osac"})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
	})

	It("exits with code 1 and a clear error when the kubeconfig can't be loaded", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return nil, errors.New("no such file or directory")
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--namespace=osac"})

		err := cmd.Execute()

		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.Code()).To(Equal(1))
		Expect(stderr.String()).To(ContainSubstring("Failed to connect to the Hub cluster"))
	})

	It("reports 'not created yet' instead of erroring when the namespace doesn't exist", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{Typed: fake.NewSimpleClientset(), Dynamic: emptyDynamicClient()}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--namespace=osac"})

		err := cmd.Execute()

		// A namespace that doesn't exist yet is the expected state right
		// before `helm install` creates it -- not an error -- so --watch
		// can keep polling through it. A genuine listing failure (RBAC
		// denied, connection error) is covered by WorkloadChecks' own
		// tests.
		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("not created yet"))
	})

	It("reports the namespace as existing with no workloads yet, once it's been created", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{Typed: fake.NewSimpleClientset(namespaceObj("osac")), Dynamic: emptyDynamicClient()}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--namespace=osac"})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("exists"))
		Expect(stdout.String()).NotTo(ContainSubstring("not created yet"))
	})

	It("shows prerequisite results alongside workloads, without them counting toward the progress bar", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{
					// No default StorageClass: that prerequisite fails, but
					// the Deployment (the only entry the progress bar
					// counts -- the namespace and prerequisites don't) is
					// ready.
					Typed: fake.NewSimpleClientset(
						&appsv1.Deployment{
							ObjectMeta: metav1.ObjectMeta{Name: "fulfillment-grpc-server", Namespace: "osac"},
							Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
						},
						namespaceObj("osac"),
					),
					// cert-manager's CRD and Operator are present (so those
					// prerequisites pass), but nothing else is -- a mix of
					// passing and failing prerequisites in the same run.
					Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
						runtime.NewScheme(),
						listKinds,
						newUnstructuredCRD("certificates.cert-manager.io"),
						newUnstructuredCSV("cert-manager-operator", "cert-manager-operator.v1.20.0", "Succeeded", "1.20.0"),
					),
				}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--namespace=osac"})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		out := stdout.String()
		Expect(out).To(ContainSubstring("RESOURCES"))
		Expect(out).To(ContainSubstring("OPERATORS"))
		Expect(out).To(ContainSubstring("1/1 ready")) // the Deployment only -- namespace and prerequisites don't count
	})

	It("with --watch, runs the interactive program instead of printing once", func() {
		var gotModel tea.Model
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{Typed: fake.NewSimpleClientset()}, nil
			},
			runProgram: func(m tea.Model) error {
				gotModel = m
				return nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--namespace=osac", "--watch"})

		err := cmd.Execute()

		Expect(err).ToNot(HaveOccurred())
		Expect(gotModel).NotTo(BeNil())
		Expect(stdout.String()).To(BeEmpty()) // nothing printed directly; the program owns rendering
	})

	It("propagates a --watch program error as exit code 1", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{Typed: fake.NewSimpleClientset()}, nil
			},
			runProgram: func(tea.Model) error {
				return errors.New("not a terminal")
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--namespace=osac", "--watch"})

		err := cmd.Execute()

		Expect(err).To(HaveOccurred())
		var exitErr exit.Error
		Expect(errors.As(err, &exitErr)).To(BeTrue())
		Expect(exitErr.Code()).To(Equal(1))
	})
})
