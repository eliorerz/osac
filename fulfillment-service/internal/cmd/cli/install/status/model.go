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
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// checkResultsMsg carries a fresh workload listing into Update. err is set
// when the List call itself failed (e.g. the namespace doesn't exist, or
// RBAC denies it) -- a different failure mode from any individual
// workload's own Result, which always carries a Status/Message and never
// an error.
type checkResultsMsg struct {
	results []install.Result
	err     error
}

// tickMsg triggers the next workload listing in watch mode.
type tickMsg time.Time

// watchModel is the tea.Model driving `osac install status --watch`: it
// re-lists namespace's workloads on an interval and redraws in place,
// fitted to the terminal's current size (tracked via tea.WindowSizeMsg).
type watchModel struct {
	ctx       context.Context //nolint:containedctx // bubbletea's Update/Init have no context parameter to thread this through otherwise.
	clients   *install.Clients
	namespace string
	interval  time.Duration

	results []install.Result
	err     error
	width   int
}

func newWatchModel(ctx context.Context, clients *install.Clients, namespace string, interval time.Duration) watchModel {
	return watchModel{
		ctx:       ctx,
		clients:   clients,
		namespace: namespace,
		interval:  interval,
	}
}

func (m watchModel) Init() tea.Cmd {
	return listWorkloads(m.ctx, m.clients, m.namespace)
}

func listWorkloads(ctx context.Context, clients *install.Clients, namespace string) tea.Cmd {
	return func() tea.Msg {
		results, err := install.WorkloadChecks(ctx, clients, namespace)
		return checkResultsMsg{results: results, err: err}
	}
}

func tick(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
		return m, nil
	case checkResultsMsg:
		m.results = msg.results
		m.err = msg.err
		return m, tick(m.interval)
	case tickMsg:
		return m, listWorkloads(m.ctx, m.clients, m.namespace)
	}
	return m, nil
}

func (m watchModel) View() tea.View {
	body := renderStatus(m.results, m.width)
	if m.err != nil {
		body = renderError(m.err, m.width)
	}
	v := tea.NewView(body + "\n(press q to quit)")
	v.AltScreen = true
	return v
}
