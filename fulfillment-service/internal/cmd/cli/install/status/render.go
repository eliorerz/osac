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
	colorGreen  = "10" // Pass; also the progress bar once everything is ready
	colorRed    = "9"  // Fail, Required; also the progress bar early on
	colorYellow = "11" // Fail, Warning; also the progress bar approaching ready
	colorCyan   = "14" // Progressing -- distinct from red/yellow/green so "installing" never reads as a failure
	colorBanner = "99" // Purple
	colorBorder = "99"
	colorHeader = "14" // Cyan section headers (RESOURCES/OPERATORS)
)

// progressColorThresholds: below barColorLowThreshold the bar is red, below
// barColorHighThreshold it's yellow, at or above it it's green -- the same
// red/yellow/green a viewer already reads from the per-check icons, applied
// to the overall ready-percentage.
const (
	barColorLowThreshold  = 0.5
	barColorHighThreshold = 1.0
)

const (
	defaultWidth    = 80
	frameMaxWidth   = 100
	frameMinWidth   = 24
	frameOverhead   = 4 // border (2 cols) + horizontal padding (2 cols)
	maxBarWidth     = 60
	minBarWidth     = 10
	nameColWidth    = 36
	barWidthTrim    = 4 // margin subtracted from the content width for the bar
	layoutOverhead  = 3 // icon + the two spaces separating icon/name/message
	minMessageWidth = 10
)

// renderStatus renders results as a framed dashboard: a small "OSAC" banner,
// a progress bar scoped to install progress (Namespace/Services/Jobs; see
// installationCategories), and one section per install.Category, fitted to
// width. Pure and stateless -- given the same results and width, it always
// renders the same string -- so it's testable without a real terminal or
// tea.Program, and reusable by both the one-shot render path and the
// watch-mode tea.Model's View.
func renderStatus(results []install.Result, width int) string {
	frameWidth, contentWidth := frameDimensions(width)

	var b strings.Builder
	b.WriteString(banner())
	b.WriteString(progressLine(results, contentWidth))
	b.WriteString("\n\n")
	b.WriteString(sections(results, contentWidth))

	return frame(strings.TrimRight(b.String(), "\n"), frameWidth)
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

// frameDimensions clamps width to [frameMinWidth, frameMaxWidth] (falling
// back to defaultWidth when width is unknown) and returns both the outer
// frame width and the usable content width inside its border and padding.
func frameDimensions(width int) (frameWidth, contentWidth int) {
	if width <= 0 {
		width = defaultWidth
	}
	frameWidth = width
	if frameWidth > frameMaxWidth {
		frameWidth = frameMaxWidth
	}
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
func sections(results []install.Result, width int) string {
	if len(results) == 0 {
		return "No OSAC workloads found in this namespace.\n"
	}
	var b strings.Builder
	writeSection(&b, "NAMESPACE", filterCategory(results, install.NamespaceCategory), width)
	writeSection(&b, "SERVICES", filterCategory(results, install.ServiceCategory), width)
	writeSection(&b, "JOBS", filterCategory(results, install.JobCategory), width)
	writeSection(&b, "RESOURCES", filterCategory(results, install.ResourceCategory), width)
	writeSection(&b, "OPERATORS", filterCategory(results, install.OperatorCategory), width)
	return b.String()
}

func writeSection(b *strings.Builder, title string, results []install.Result, width int) {
	if len(results) == 0 {
		return
	}
	b.WriteString(sectionHeader(title))
	b.WriteString("\n")
	for _, result := range results {
		b.WriteString(statusLine(result, width))
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
	bar := progress.New(
		progress.WithColorFunc(func(total, _ float64) stdcolor.Color { return progressColor(total) }),
		progress.WithWidth(barWidth),
	)

	passed := 0
	total := 0
	for _, result := range results {
		if !installationCategories[result.Check.Category] {
			continue
		}
		total++
		if result.Status == install.Pass {
			passed++
		}
	}
	var percent float64
	if total > 0 {
		percent = float64(passed) / float64(total)
	}

	return fmt.Sprintf("%s  %d/%d ready", bar.ViewAs(percent), passed, total)
}

// progressColor picks red/yellow/green for the progress bar based on the
// overall ready-percentage, the same three colors a viewer already reads
// from the per-check ✓/✗ icons.
func progressColor(percent float64) stdcolor.Color {
	switch {
	case percent >= barColorHighThreshold:
		return lipgloss.Color(colorGreen)
	case percent >= barColorLowThreshold:
		return lipgloss.Color(colorYellow)
	default:
		return lipgloss.Color(colorRed)
	}
}

// statusLine renders one check as "<icon> <name, padded/truncated> <message,
// truncated>". Padding and truncation happen on the plain text *before* any
// ANSI styling is applied (lipgloss.Style.Width wraps rather than
// truncating, and padding an already-styled string miscounts its escape
// codes as visible characters) -- both would silently break the single-line
// layout otherwise: a long check name would wrap onto a second line, and an
// overly long message would render with no indication it was cut off.
func statusLine(result install.Result, width int) string {
	icon, style := statusIcon(result)
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

// statusIcon picks a check's icon and color: ✓ green once it Pass-es, ~ cyan
// while it's Progressing (actively installing -- not a failure, so it must
// never render as red/yellow the way an actual failure does), and ✗
// red/yellow (by Severity) once it's genuinely Failed.
func statusIcon(result install.Result) (string, lipgloss.Style) {
	bold := lipgloss.NewStyle().Bold(true)
	switch result.Status {
	case install.Pass:
		return "✓", bold.Foreground(lipgloss.Color(colorGreen))
	case install.Progressing:
		return "~", bold.Foreground(lipgloss.Color(colorCyan))
	default:
		if result.Check.Severity == install.Warning {
			return "✗", bold.Foreground(lipgloss.Color(colorYellow))
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
