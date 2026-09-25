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
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

var _ = Describe("watchModel", func() {
	var (
		m       watchModel
		clients *install.Clients
	)

	BeforeEach(func() {
		clients = &install.Clients{Typed: fake.NewSimpleClientset(
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "fulfillment-grpc-server", Namespace: "osac"},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
			},
		)}
		m = newWatchModel(context.Background(), clients, "osac", time.Second)
	})

	It("kicks off a workload listing from Init", func() {
		cmd := m.Init()
		Expect(cmd).NotTo(BeNil())

		msg := cmd()
		results, ok := msg.(checkResultsMsg)
		Expect(ok).To(BeTrue())
		Expect(results.err).NotTo(HaveOccurred())
		Expect(results.results).To(HaveLen(1))
		Expect(results.results[0].Check.Name).To(Equal("fulfillment-grpc-server"))
	})

	It("stores results and schedules the next tick on checkResultsMsg", func() {
		results := []install.Result{{Check: passingCheck, Status: install.Pass}}

		next, cmd := m.Update(checkResultsMsg{results: results})

		updated := next.(watchModel)
		Expect(updated.results).To(Equal(results))
		Expect(updated.err).NotTo(HaveOccurred())
		Expect(cmd).NotTo(BeNil()) // schedules the next tick
	})

	It("stores a listing error separately from results, still schedules the next tick", func() {
		next, cmd := m.Update(checkResultsMsg{err: errors.New("namespace not found")})

		updated := next.(watchModel)
		Expect(updated.err).To(HaveOccurred())
		Expect(cmd).NotTo(BeNil())
	})

	It("re-lists workloads on tickMsg", func() {
		_, cmd := m.Update(tickMsg(time.Now()))

		Expect(cmd).NotTo(BeNil())
		msg := cmd()
		_, ok := msg.(checkResultsMsg)
		Expect(ok).To(BeTrue())
	})

	It("tracks the terminal width from WindowSizeMsg", func() {
		next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

		Expect(next.(watchModel).width).To(Equal(120))
	})

	It("quits on q", func() {
		_, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
		Expect(cmd).NotTo(BeNil())
		Expect(cmd()).To(Equal(tea.QuitMsg{}))
	})

	It("ignores other keys", func() {
		_, cmd := m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
		Expect(cmd).To(BeNil())
	})

	It("renders the status view with AltScreen enabled", func() {
		m.results = []install.Result{{Check: passingCheck, Status: install.Pass, Message: "ok"}}

		v := m.View()

		Expect(v.AltScreen).To(BeTrue())
		Expect(v.Content).To(ContainSubstring("fulfillment-grpc-server"))
		Expect(v.Content).To(ContainSubstring("press q to quit"))
	})

	It("renders the error view instead of a stale dashboard when the last listing failed", func() {
		m.err = errors.New("namespace not found")

		v := m.View()

		Expect(v.AltScreen).To(BeTrue())
		Expect(v.Content).To(ContainSubstring("namespace not found"))
	})
})
