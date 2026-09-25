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
	"fmt"
	stdcolor "image/color"
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// Colors match render.Table's STATUS column, for a consistent look across
// `osac install discover/validate/status`.
const (
	colorGreen  = "10" // Pass; also the progress bar while nothing has genuinely failed
	colorRed    = "9"  // Fail, Required; also the progress bar once something has
	colorYellow = "11" // Fail, Warning
	colorBlue   = "12" // Progressing spinner -- distinct from red/yellow/green so "installing" never reads as a failure
	colorGray   = "8"  // Progressing/"not created yet" -- queued, nothing actually happening yet
	colorBanner = "99" // Purple
	colorBorder = "99"
	colorHeader = "14" // Cyan section headers (RESOURCES/OPERATORS)
)

// notYetCreatedMessage is the exact Message install.WorkloadChecks sets on
// a Progressing result for something the Helm release declares but hasn't
// created yet (see notYetCreatedResult and namespaceResult in
// osac-installer/pkg/install/workload.go). Matched on here to tell that
// apart from a Progressing result something is actively doing (a Job
// retrying, a Deployment rolling out, a CSV installing) -- both share the
// same install.Progressing status, but only the latter warrants an
// animated spinner; something merely queued behind an earlier step hasn't
// started yet, and animating it implies work that isn't actually
// happening (confirmed live: this read as confusing/misleading with every
// not-yet-created Job showing the same spinner as one genuinely retrying).
const notYetCreatedMessage = "not created yet"

const (
	defaultWidth  = 80
	frameMinWidth = 24
	frameOverhead = 4 // border (2 cols) + horizontal padding (2 cols)
	maxBarWidth   = 100
	minBarWidth   = 10
	nameColWidth  = 36
	// barWidthTrim reserves room for the "  N/N ready" text progressLine
	// appends after the bar -- NOT extra padding, the space it needs.
	// progress.Model.ViewAs draws its own "NNN%" label *inside* the width
	// given to progress.WithWidth (confirmed by reading bubbles/progress's
	// barView: it subtracts the percentage label's width from the track
	// width itself), so the bar's rendered width is exactly barWidth; only
	// the trailing "  N/N ready" is genuinely extra. Sized for up to
	// 3-digit passed/total counts ("  999/999 ready" = 15 chars) plus a
	// 1-char margin. Confirmed live: with the old value of 4, "N/N ready"
	// wrapped onto its own line the moment maxBarWidth stopped being the
	// binding constraint (i.e. as soon as the frame got wide enough not to
	// hit the old 60-wide cap) -- this used to work only by accident.
	barWidthTrim    = 16
	layoutOverhead  = 3 // icon + the two spaces separating icon/name/message
	minMessageWidth = 10
)

// renderStatus renders results as a framed dashboard: a small "OSAC" banner,
// a one-line phase title (see phaseTitle), a progress bar scoped to install
// progress (Namespace/Services/Jobs; see installationCategories), and one
// section per install.Category, fitted to width. Pure and stateless --
// given the same results and width, it always renders the same string --
// so it's testable without a real terminal or tea.Program, and reusable by
// both the one-shot render path and the watch-mode tea.Model's View.
//
// spinnerFrame is which frame of the Progressing spinner (see
// spinnerFrames) to draw. The one-shot render path has no animation loop
// driving it, so it always passes 0 -- a single static frame, which is
// expected and fine for a single render.
//
// chartVersion is the "osac" Helm release's chart version (see
// install.ChartVersion), shown in the phase title. Empty when no release
// has been recorded yet -- phaseTitle omits it rather than showing a blank
// or placeholder version in that case.
func renderStatus(results []install.Result, width int, spinnerFrame int, chartVersion string) string {
	frameWidth, contentWidth := frameDimensions(width)

	var b strings.Builder
	b.WriteString(banner())
	b.WriteString(phaseTitle(results, chartVersion))
	b.WriteString("\n")
	b.WriteString(progressLine(results, contentWidth))
	b.WriteString("\n\n")
	b.WriteString(sections(results, contentWidth, spinnerFrame))

	return frame(strings.TrimRight(b.String(), "\n"), frameWidth)
}

// preInstallValidateJobName is the pre-install-validate hook's Job name
// with the release name every install-osac invocation in this repo uses
// (see helmReleaseName's Go counterpart in osac-installer/pkg/install) --
// {{ include "osac.fullname" . }}-pre-install-validate, which resolves to
// this literal name for the release name "osac".
const preInstallValidateJobName = "osac-pre-install-validate"

// phaseTitle is a one-line headline distinguishing the two things a viewer
// actually cares about in sequence: is the cluster still being validated as
// ready to install, or is OSAC itself now installing. It doesn't switch the
// progress bar's meaning or reset it: that bar's whole point is to climb
// smoothly and honestly from the start, and a phase-driven reset would
// reintroduce the same confusing jump this dashboard exists to avoid.
//
// Driven by whether anything OTHER than the pre-install-validate job has
// actually been created yet, not by that job's own visibility: Helm's
// hook-delete-policy can make a hook Job disappear on a retried
// install/upgrade even though it already succeeded earlier (confirmed
// live), which would otherwise make this claim "still validating" long
// after validation actually finished. Pre-install-validate runs before
// every other hook and before any of the chart's own plain resources, so
// "nothing else has been created yet" is the reliable signal that
// validation hasn't finished, regardless of whether its own Job is still
// visible.
func phaseTitle(results []install.Result, chartVersion string) string {
	installing := false
	for _, result := range results {
		if !installationCategories[result.Check.Category] || result.Check.Name == preInstallValidateJobName {
			continue
		}
		if result.Message != notYetCreatedMessage {
			installing = true
			break
		}
	}

	version := "OSAC"
	if chartVersion != "" {
		version = "OSAC v" + chartVersion
	}
	title := "Validating prerequisites for " + version + "..."
	if installing {
		title = "Installing " + version + "..."
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorHeader)).Render(title)
}

// renderError renders err inside the same framed banner as renderStatus,
// so a failed workload listing (a bad --namespace, RBAC denied) still gets
// a clear, boxed message instead of a blank or stale dashboard.
func renderError(err error, width int) string {
	frameWidth, contentWidth := frameDimensions(width)

	errorStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorRed))
	message := lipgloss.NewStyle().MaxWidth(contentWidth).Render(fmt.Sprintf("Error: %v", err))

	var b strings.Builder
	b.WriteString(banner())
	b.WriteString(errorStyle.Render(message))

	return frame(b.String(), frameWidth)
}

// frameDimensions fits the frame to the terminal's actual width (floored
// at frameMinWidth so a tiny terminal still gets a usable frame, but with
// no upper cap -- confirmed live that capping it left a wide terminal with
// a needlessly narrow dashboard and a large empty gutter beside it),
// falling back to defaultWidth when width is unknown (0, e.g. not a
// terminal), and returns both the outer frame width and the usable
// content width inside its border and padding.
func frameDimensions(width int) (frameWidth, contentWidth int) {
	if width <= 0 {
		width = defaultWidth
	}
	frameWidth = width
	if frameWidth < frameMinWidth {
		frameWidth = frameMinWidth
	}
	return frameWidth, frameWidth - frameOverhead
}

// frame wraps content (every line of which the caller must have already
// sized to fit within frameWidth-frameOverhead) in a colored rounded
// border. Content isn't re-truncated here: a second width-clamping pass
// over already-ANSI-styled multi-line text risks cutting escape codes
// mid-sequence, so callers size their own lines instead.
func frame(content string, frameWidth int) string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colorBorder)).
		Padding(0, 1).
		Width(frameWidth)
	return style.Render(content)
}

// installationCategories are the install.Category values that count toward
// the progress bar's ready-percentage: the workloads actually being
// installed right now. Prerequisite categories (Resources, Operators)
// don't count either, for the same reason -- both are shown in their own
// sections for context but don't move the bar, which tracks the install's
// own progress.
//
// NamespaceCategory is deliberately excluded too, even though it's part of
// the install lifecycle: the namespace existing is a precondition for
// anything else to be discoverable at all, not a component of the install
// itself, and counting it would make the bar read 100% the moment the
// namespace is created but before `helm install` has created a single
// Deployment or Job -- exactly the false "fully ready" reading this
// exclusion exists to prevent.
var installationCategories = map[install.Category]bool{
	install.ServiceCategory: true,
	install.JobCategory:     true,
}

// sections groups results into Namespace/Services/Jobs first (matching the
// actual install lifecycle -- the namespace has to exist before anything in
// it can), then Resources/Operators (the prerequisite checks, shown for
// context after the install-progress sections), rendering only the
// sections that actually have entries. WorkloadChecks always includes the
// namespace result, so this is empty only when called directly with
// results that omit it entirely (e.g. a test).
func sections(results []install.Result, width int, spinnerFrame int) string {
	if len(results) == 0 {
		return "No OSAC workloads found in this namespace.\n"
	}
	var b strings.Builder
	writeSection(&b, "NAMESPACE", filterCategory(results, install.NamespaceCategory), width, spinnerFrame)
	writeSection(&b, "SERVICES", filterCategory(results, install.ServiceCategory), width, spinnerFrame)
	writeSection(&b, "JOBS", filterCategory(results, install.JobCategory), width, spinnerFrame)
	writeSection(&b, "RESOURCES", filterCategory(results, install.ResourceCategory), width, spinnerFrame)
	writeSection(&b, "OPERATORS", filterCategory(results, install.OperatorCategory), width, spinnerFrame)
	return b.String()
}

func writeSection(b *strings.Builder, title string, results []install.Result, width int, spinnerFrame int) {
	if len(results) == 0 {
		return
	}
	b.WriteString(sectionHeader(title))
	b.WriteString("\n")
	for _, result := range results {
		b.WriteString(statusLine(result, width, spinnerFrame))
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func filterCategory(results []install.Result, category install.Category) []install.Result {
	var out []install.Result
	for _, result := range results {
		if result.Check.Category == category {
			out = append(out, result)
		}
	}
	return out
}

func sectionHeader(title string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorHeader)).Render(title)
}

func progressLine(results []install.Result, width int) string {
	barWidth := width - barWidthTrim
	if barWidth > maxBarWidth {
		barWidth = maxBarWidth
	}
	if barWidth < minBarWidth {
		barWidth = minBarWidth
	}

	passed := 0
	total := 0
	failed := false
	for _, result := range results {
		if !installationCategories[result.Check.Category] {
			continue
		}
		total++
		switch result.Status {
		case install.Pass:
			passed++
		case install.Failed:
			failed = true
		}
	}
	barColor := progressColor(failed)
	bar := progress.New(
		progress.WithColorFunc(func(_, _ float64) stdcolor.Color { return barColor }),
		progress.WithWidth(barWidth),
	)

	var percent float64
	if total > 0 {
		percent = float64(passed) / float64(total)
	}

	return fmt.Sprintf("%s  %d/%d ready", bar.ViewAs(percent), passed, total)
}

// progressColor picks red/yellow/green for the progress bar based on the
// overall ready-percentage, the same three colors a viewer already reads
// from the per-check ✓/✗ icons.
// progressColor is red when any counted result has genuinely Failed, green
// otherwise -- including at 10% ready, as long as everything not yet ready
// is still Progressing rather than actually broken. A percent-based
// red/yellow/green gradient would read early, entirely-normal install
// progress (nothing has failed, most things simply haven't started yet) as
// a problem; this reserves red for something that actually needs
// attention.
func progressColor(failed bool) stdcolor.Color {
	if failed {
		return lipgloss.Color(colorRed)
	}
	return lipgloss.Color(colorGreen)
}

// statusLine renders one check as "<icon> <name, padded/truncated> <message,
// truncated>". Padding and truncation happen on the plain text *before* any
// ANSI styling is applied (lipgloss.Style.Width wraps rather than
// truncating, and padding an already-styled string miscounts its escape
// codes as visible characters) -- both would silently break the single-line
// layout otherwise: a long check name would wrap onto a second line, and an
// overly long message would render with no indication it was cut off.
func statusLine(result install.Result, width int, spinnerFrame int) string {
	icon, style := statusIcon(result, spinnerFrame)
	name := lipgloss.NewStyle().Bold(true).Render(padName(result.Check.Name, nameColWidth))

	messageWidth := width - nameColWidth - layoutOverhead
	if messageWidth < minMessageWidth {
		messageWidth = minMessageWidth
	}
	message := truncateEllipsis(result.Message, messageWidth)

	return fmt.Sprintf("%s %s %s", style.Render(icon), name, message)
}

// padName truncates s to width (adding an ellipsis if it was cut) or
// right-pads it with spaces to reach width, so every row's message starts
// in the same column regardless of check name length.
func padName(s string, width int) string {
	s = truncateEllipsis(s, width)
	if n := width - len([]rune(s)); n > 0 {
		s += strings.Repeat(" ", n)
	}
	return s
}

// truncateEllipsis returns s unchanged if it's within width runes,
// otherwise cuts it to width-1 runes and appends "…" so a truncated value
// is visibly distinguishable from a short one that happened to fit exactly.
func truncateEllipsis(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		return string(r[:width])
	}
	return string(r[:width-1]) + "…"
}

// statusIcon picks a check's icon and color: ✓ green once it Pass-es, a
// spinner (or a static "○", see notYetCreatedMessage) while it's
// Progressing -- not a failure, so it must never render as red/yellow the
// way an actual failure does -- ✗ red once it's genuinely Failed at
// Required severity, and ⚠ yellow once it's Failed at Warning severity: a
// visibly different symbol, not just a different color, so a
// Warning-severity gap (optional depending on which services this install
// actually uses, e.g. cnv-operator only matters for vmaas) doesn't read at
// a glance as the same kind of problem as a genuinely blocking Required
// failure.
// spinnerFrames is a small Braille-pattern spinner (all single-width in
// virtually every terminal, unlike many other "animation" glyphs), cycled
// through by spinnerFrame to show a Progressing check as actively moving
// rather than merely a static, ambiguous symbol.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func statusIcon(result install.Result, spinnerFrame int) (string, lipgloss.Style) {
	bold := lipgloss.NewStyle().Bold(true)
	switch result.Status {
	case install.Pass:
		return "✓", bold.Foreground(lipgloss.Color(colorGreen))
	case install.Progressing:
		if result.Message == notYetCreatedMessage {
			// Queued behind an earlier step, nothing actually happening
			// yet -- a static, dim icon, not the animated spinner (which
			// would otherwise misleadingly imply active work).
			return "○", lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color(colorGray))
		}
		frame := spinnerFrames[spinnerFrame%len(spinnerFrames)]
		return frame, bold.Foreground(lipgloss.Color(colorBlue))
	default:
		if result.Check.Severity == install.Warning {
			return "⚠", bold.Foreground(lipgloss.Color(colorYellow))
		}
		return "✗", bold.Foreground(lipgloss.Color(colorRed))
	}
}

// osacFont is a small 6-row block font, just the letters OSAC needs. Every
// glyph's rows are the same rune-length (verified by TestOSACFontGlyphsAlign
// in render_test.go), so banner() can concatenate them column-wise without
// hand-aligning a giant multi-line string by eye.
var osacFont = map[rune][]string{
	'O': {
		" ████ ",
		"██  ██",
		"██  ██",
		"██  ██",
		"██  ██",
		" ████ ",
	},
	'S': {
		" █████",
		"██    ",
		" ████ ",
		"    ██",
		"    ██",
		"█████ ",
	},
	'A': {
		" ████ ",
		"██  ██",
		"██████",
		"██  ██",
		"██  ██",
		"██  ██",
	},
	'C': {
		" █████",
		"██    ",
		"██    ",
		"██    ",
		"██    ",
		" █████",
	},
}

// osacFontHeight is the row count every osacFont glyph has.
const osacFontHeight = 6

// banner renders "OSAC" as block letters, colored, with a trailing blank
// line separating it from the rest of the view.
func banner() string {
	rows := make([]string, osacFontHeight)
	for _, r := range "OSAC" {
		glyph := osacFont[r]
		for i := range rows {
			rows[i] += glyph[i] + " "
		}
	}
	style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorBanner))
	var b strings.Builder
	for _, row := range rows {
		b.WriteString(style.Render(row))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}
