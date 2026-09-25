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

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// checkResultsMsg carries a fresh workload listing into Update. err is set
// when the List call itself failed (e.g. the namespace doesn't exist, or
// RBAC denies it) -- a different failure mode from any individual
// workload's own Result, which always carries a Status/Message and never
// an error.
type checkResultsMsg struct {
	results      []install.Result
	chartVersion string
	err          error
}

// tickMsg triggers the next workload listing in watch mode.
type tickMsg time.Time

// latestReleaseMsg carries the result of the one-time (not re-fetched per
// tick, see watchModel.latestReleaseVersion) lookup of the latest
// published "osac" release version. An empty version means the lookup
// failed or found nothing -- not distinguished from "not fetched yet",
// since both cases mean the same thing to pickDisplayVersion: fall back to
// the chart's own version.
type latestReleaseMsg struct {
	version string
}

// spinnerTickMsg advances the Progressing spinner's animation by one frame.
// Ticks on its own fast, fixed cadence (spinnerTickInterval) independent of
// --interval, which is usually far too slow (default 5s) to animate
// against directly -- re-checking cluster state and animating "this is
// still moving" are different concerns on different clocks.
type spinnerTickMsg time.Time

// spinnerTickInterval is fast enough to read as continuous motion but far
// below anything a re-render at this rate would noticeably burden a
// terminal with.
const spinnerTickInterval = 120 * time.Millisecond

// watchModel is the tea.Model driving `osac install status --watch`: it
// re-checks namespace's workloads and the prerequisite matrix (checks) on
// an interval and redraws in place, fitted to the terminal's current size
// (tracked via tea.WindowSizeMsg).
type watchModel struct {
	ctx       context.Context //nolint:containedctx // bubbletea's Update/Init have no context parameter to thread this through otherwise.
	clients   *install.Clients
	namespace string
	// checks is the prerequisite matrix (built once from CheckOptions, not
	// re-derived per tick): unlike workloads, what to check doesn't change
	// between ticks, only each check's live result does.
	checks   []install.Check
	interval time.Duration

	results      []install.Result
	chartVersion string
	err          error
	width        int
	spinnerFrame int

	// latestReleaseVersion is fetched exactly once, at Init, never
	// re-fetched on later ticks -- unlike everything else in this model,
	// which legitimately changes over the course of an install and needs
	// re-checking on --interval. The latest published osac release does
	// not change over the lifetime of one `--watch` run, and GitHub's
	// unauthenticated API rate limit (60 req/hour) would be exhausted
	// within minutes at the default 5s --interval if this were re-fetched
	// every tick alongside everything else.
	latestReleaseVersion string

	// vp scrolls the dashboard when it's taller than the terminal -- without
	// it, a small terminal (or an install with many components) just
	// truncates the bottom of the frame with no way to see the rest.
	// haveSize is false until the first real tea.WindowSizeMsg arrives (the
	// zero-value viewport has no real height yet), so a one-shot render or
	// a test that never sends one still renders the full, unclipped body
	// exactly as before.
	vp       viewport.Model
	haveSize bool
}

func newWatchModel(ctx context.Context, clients *install.Clients, namespace string, checks []install.Check, interval time.Duration) watchModel {
	return watchModel{
		ctx:       ctx,
		clients:   clients,
		namespace: namespace,
		checks:    checks,
		interval:  interval,
	}
}

func (m watchModel) Init() tea.Cmd {
	return tea.Batch(
		gatherResultsCmd(m.ctx, m.clients, m.namespace, m.checks),
		spinnerTick(),
		latestReleaseCmd(m.ctx),
	)
}

func spinnerTick() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(t time.Time) tea.Msg { return spinnerTickMsg(t) })
}

// latestReleaseCmd runs the one-time latestOSACReleaseVersion lookup (see
// watchModel.latestReleaseVersion for why this is fetched only once, not
// on every tick). Never itself an error to the caller: a failed lookup
// just carries an empty version, which pickDisplayVersion already treats
// as "fall back to the chart's own version".
func latestReleaseCmd(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		version, _ := latestOSACReleaseVersion(ctx)
		return latestReleaseMsg{version: version}
	}
}

// gatherResultsCmd combines the namespace/workload results (WorkloadChecks),
// the prerequisite results (RunAll over checks), and the Helm release's
// chart version into a single checkResultsMsg, so the view always renders
// all of it from one consistent snapshot rather than independently-timed
// updates.
func gatherResultsCmd(ctx context.Context, clients *install.Clients, namespace string, checks []install.Check) tea.Cmd {
	return func() tea.Msg {
		results, chartVersion, err := gatherResults(ctx, clients, namespace, checks)
		if err != nil {
			return checkResultsMsg{err: err}
		}
		return checkResultsMsg{results: results, chartVersion: chartVersion}
	}
}

// gatherResults is gatherResultsCmd's non-tea.Cmd core, shared with the
// one-shot render path (without --watch) in status_cmd.go.
func gatherResults(ctx context.Context, clients *install.Clients, namespace string, checks []install.Check) ([]install.Result, string, error) {
	results, err := install.WorkloadChecks(ctx, clients, namespace)
	if err != nil {
		return nil, "", err
	}
	results = append(results, install.RunAll(ctx, clients, checks)...)
	chartVersion, err := install.ChartVersion(ctx, clients, namespace)
	if err != nil {
		return nil, "", err
	}
	return results, chartVersion, nil
}

func tick(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// body is the framed dashboard (or error box) content, before any
// viewport scrolling/clipping is applied to it.
func (m watchModel) body() string {
	if m.err != nil {
		return renderError(m.err, m.width)
	}
	displayVersion := pickDisplayVersion(m.chartVersion, m.latestReleaseVersion)
	return renderStatus(m.results, m.width, m.spinnerFrame, displayVersion)
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.haveSize = true
		m.vp.SetWidth(msg.Width)
		// -1 for the "(press q to quit)" footer line View() appends below
		// the scrollable area.
		m.vp.SetHeight(max(msg.Height-1, 1))
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
		if m.haveSize {
			m.vp, cmd = m.vp.Update(msg)
		}
	case checkResultsMsg:
		m.results = msg.results
		m.chartVersion = msg.chartVersion
		m.err = msg.err
		cmd = tick(m.interval)
	case tickMsg:
		cmd = gatherResultsCmd(m.ctx, m.clients, m.namespace, m.checks)
	case spinnerTickMsg:
		m.spinnerFrame++
		cmd = spinnerTick()
	case latestReleaseMsg:
		m.latestReleaseVersion = msg.version
	default:
		return m, nil
	}
	// Keep the viewport's content in sync with the model on every update,
	// not just when rendering: SetContent is what lets it compute how far
	// there is left to scroll (maxYOffset), and View() has a value
	// receiver -- calling SetContent only there would mutate a throwaway
	// copy, leaving the real model's viewport thinking it has no content
	// and clamping every scroll key to a no-op.
	if m.haveSize {
		m.vp.SetContent(m.body())
	}
	return m, cmd
}

func (m watchModel) View() tea.View {
	content := m.body()
	footer := "\n(press q to quit)"
	if m.haveSize {
		content = m.vp.View()
		if m.vp.TotalLineCount() > m.vp.VisibleLineCount() {
			footer = "\n(press q to quit, ↑/↓ to scroll)"
		}
	}
	v := tea.NewView(content + footer)
	v.AltScreen = true
	return v
}
