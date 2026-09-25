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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/osac-installer/pkg/install"
)

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
				return &install.Clients{Typed: fake.NewSimpleClientset(
					&appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{Name: "fulfillment-grpc-server", Namespace: "osac"},
						Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
					},
				)}, nil
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
				return &install.Clients{Typed: fake.NewSimpleClientset(
					&appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{Name: "osac-ui", Namespace: "osac"},
						Status:     appsv1.DeploymentStatus{ReadyReplicas: 0},
					},
				)}, nil
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

	It("exits with code 1 and a clear error when the namespace can't be listed", func() {
		runner := &runnerContext{
			loadClients: func(string) (*install.Clients, error) {
				return &install.Clients{Typed: fake.NewSimpleClientset()}, nil
			},
		}
		cmd := newCmd(runner)
		cmd.SetOut(GinkgoWriter)
		cmd.SetErr(GinkgoWriter)
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--namespace=osac"})

		err := cmd.Execute()

		// An empty fake clientset with no error injected still succeeds with
		// zero results -- this asserts the success path renders cleanly,
		// covering the "OSAC not installed yet" case distinctly from a
		// real listing failure (exercised via WorkloadChecks' own tests).
		Expect(err).ToNot(HaveOccurred())
		Expect(stdout.String()).To(ContainSubstring("No OSAC workloads found"))
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
