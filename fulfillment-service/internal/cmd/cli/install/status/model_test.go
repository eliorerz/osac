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
	"fmt"
	"strings"
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
			namespaceObj("osac"),
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "fulfillment-grpc-server", Namespace: "osac"},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
			},
		)}
		m = newWatchModel(context.Background(), clients, "osac", nil, time.Second)
	})

	It("kicks off a workload listing from Init", func() {
		cmd := m.Init()
		Expect(cmd).NotTo(BeNil())

		results := findCheckResultsMsg(cmd)
		Expect(results.err).NotTo(HaveOccurred())
		Expect(results.results).To(HaveLen(2)) // the namespace itself + the Deployment
		names := map[string]bool{}
		for _, r := range results.results {
			names[r.Check.Name] = true
		}
		Expect(names).To(HaveKey("fulfillment-grpc-server"))
	})

	It("also runs the prerequisite checks from Init, when given any", func() {
		checks, err := install.DefaultChecks(install.CheckOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(checks).NotTo(BeEmpty())
		clientsWithDynamic := &install.Clients{Typed: clients.Typed, Dynamic: emptyDynamicClient()}
		withChecks := newWatchModel(context.Background(), clientsWithDynamic, "osac", checks, time.Second)

		results := findCheckResultsMsg(withChecks.Init())

		Expect(results.err).NotTo(HaveOccurred())
		// namespace + Deployment + every "requiredFor: [all]" prerequisite,
		// all failing since none of their cluster state was seeded.
		Expect(len(results.results)).To(BeNumerically(">", 2))
	})

	It("also kicks off the spinner animation from Init", func() {
		msg := findMsg[spinnerTickMsg](m.Init())
		Expect(msg).NotTo(BeZero())
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

	It("clips the view to a small terminal's height instead of overflowing it unscrollably", func() {
		var results []install.Result
		for i := range 30 {
			results = append(results, install.Result{
				Check:  install.Check{Name: fmt.Sprintf("service-%d", i), Category: install.ServiceCategory},
				Status: install.Pass,
			})
		}
		m.results = results
		next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
		small := next.(watchModel)

		lines := strings.Split(small.View().Content, "\n")

		Expect(len(lines)).To(BeNumerically("<=", 10))
		Expect(small.View().Content).To(ContainSubstring("scroll"))
	})

	It("scrolls down on the down arrow once a real terminal size is known", func() {
		var results []install.Result
		for i := range 30 {
			results = append(results, install.Result{
				Check:  install.Check{Name: fmt.Sprintf("service-%d", i), Category: install.ServiceCategory},
				Status: install.Pass,
			})
		}
		m.results = results
		next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
		sized := next.(watchModel)
		before := sized.View().Content

		scrolled, cmd := sized.Update(tea.KeyPressMsg{Code: tea.KeyDown})

		Expect(cmd).To(BeNil()) // viewport scrolling is synchronous, no async follow-up
		Expect(scrolled.(watchModel).View().Content).NotTo(Equal(before))
	})

	It("does not attempt to scroll before any WindowSizeMsg has arrived (haveSize false)", func() {
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

		Expect(cmd).To(BeNil())
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

	It("advances the spinner frame on spinnerTickMsg and schedules the next tick", func() {
		next, cmd := m.Update(spinnerTickMsg(time.Now()))

		Expect(next.(watchModel).spinnerFrame).To(Equal(1))
		Expect(cmd).NotTo(BeNil())
		Expect(cmd()).To(BeAssignableToTypeOf(spinnerTickMsg{}))
	})

	It("renders a different spinner frame each time spinnerFrame advances", func() {
		m.results = []install.Result{{Check: passingCheck, Status: install.Progressing, Message: "installing"}}

		first := m.View().Content
		m.spinnerFrame = 1
		second := m.View().Content

		Expect(first).NotTo(Equal(second))
	})
})

// findCheckResultsMsg runs cmd (as returned by Init/Update) and, if it's a
// tea.BatchMsg (Init batches the data-refresh command with the spinner
// ticker), runs each sub-command until it finds the checkResultsMsg --
// mirroring what the real bubbletea runtime does when dispatching a batch,
// without needing a full Program to drive the test.
func findCheckResultsMsg(cmd tea.Cmd) checkResultsMsg {
	return findMsg[checkResultsMsg](cmd)
}

// findMsg is findCheckResultsMsg's generic core, reused for other message
// types produced alongside it in the same batch (e.g. spinnerTickMsg).
func findMsg[T any](cmd tea.Cmd) T {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			if found, ok := sub().(T); ok {
				return found
			}
		}
		ExpectWithOffset(1, false).To(BeTrue(), "no message of the expected type found in batch")
	}
	found, ok := msg.(T)
	ExpectWithOffset(1, ok).To(BeTrue(), "message was not of the expected type")
	return found
}
