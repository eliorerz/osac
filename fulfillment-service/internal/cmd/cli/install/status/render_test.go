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
	"errors"
	"strings"

	"charm.land/lipgloss/v2"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

var (
	passingCheck  = install.Check{Name: "fulfillment-grpc-server", Severity: install.Required, Category: install.ServiceCategory}
	warningCheck  = install.Check{Name: "osac-aap-bootstrap", Severity: install.Warning, Category: install.JobCategory}
	requiredCheck = install.Check{Name: "osac-db-init", Severity: install.Required, Category: install.JobCategory}
	resourceCheck = install.Check{Name: "default-storageclass", Severity: install.Warning, Category: install.ResourceCategory}
	operatorCheck = install.Check{Name: "cert-manager-operator", Severity: install.Required, Category: install.OperatorCategory}
)

var _ = Describe("renderStatus", func() {
	It("includes the passed/total count", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "1/1 replicas ready"},
			{Check: requiredCheck, Status: install.Failed, Message: "failed"},
		}

		got := renderStatus(results, 80)

		Expect(got).To(ContainSubstring("1/2 ready"))
	})

	It("includes every check's name and message", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "1/1 replicas ready"},
			{Check: warningCheck, Status: install.Failed, Message: "in progress"},
		}

		got := renderStatus(results, 80)

		Expect(got).To(ContainSubstring("fulfillment-grpc-server"))
		Expect(got).To(ContainSubstring("1/1 replicas ready"))
		Expect(got).To(ContainSubstring("osac-aap-bootstrap"))
		Expect(got).To(ContainSubstring("in progress"))
	})

	It("handles zero results without panicking", func() {
		got := renderStatus(nil, 80)

		Expect(got).To(ContainSubstring("0/0 ready"))
		Expect(got).To(ContainSubstring("No OSAC workloads found"))
	})

	It("falls back to a default width when given 0 or a negative width", func() {
		Expect(func() { renderStatus([]install.Result{{Check: passingCheck, Status: install.Pass}}, 0) }).NotTo(Panic())
		Expect(func() { renderStatus([]install.Result{{Check: passingCheck, Status: install.Pass}}, -5) }).NotTo(Panic())
	})

	It("excludes prerequisite results (Resources/Operators) from the progress bar's ready count", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass},    // counts: pass
			{Check: requiredCheck, Status: install.Pass},   // counts: pass
			{Check: resourceCheck, Status: install.Failed}, // prerequisite -- doesn't count
			{Check: operatorCheck, Status: install.Failed}, // prerequisite -- doesn't count
		}

		got := renderStatus(results, 80)

		Expect(got).To(ContainSubstring("2/2 ready"))
	})

	It("does not report 100% ready off the namespace alone, before any workload has been discovered", func() {
		namespaceCheck := install.Check{Name: "osac", Severity: install.Required, Category: install.NamespaceCategory}
		results := []install.Result{
			{Check: namespaceCheck, Status: install.Pass, Message: "exists"},
		}

		got := renderStatus(results, 80)

		Expect(got).NotTo(ContainSubstring("100%"))
		Expect(got).To(ContainSubstring("0/0 ready"))
	})
})

var _ = Describe("renderError", func() {
	It("shows the error message inside the same framed banner", func() {
		got := renderError(errors.New("namespaces \"osac\" not found"), 80)

		Expect(got).To(ContainSubstring("namespaces \"osac\" not found"))
		Expect(got).To(ContainSubstring("╭")) // still framed
	})
})

var _ = Describe("statusLine", func() {
	It("stays on one line and ellipsizes a check name longer than the name column", func() {
		longName := install.Check{Name: "metal3-provisioning-watch-all-namespaces", Severity: install.Required}
		result := install.Result{Check: longName, Status: install.Failed, Message: "short"}

		got := statusLine(result, 80)

		Expect(got).NotTo(ContainSubstring("\n"))
		Expect(got).To(ContainSubstring("…"))
		Expect(got).To(ContainSubstring("short"))
	})

	It("ellipsizes an overly long message instead of cutting it off mid-word with no indicator", func() {
		longMessage := "this message is deliberately much longer than any reasonable terminal-width budget for the message column and must be shortened"
		result := install.Result{Check: passingCheck, Status: install.Pass, Message: longMessage}

		got := statusLine(result, 80)

		Expect(got).NotTo(ContainSubstring("\n"))
		Expect(got).To(HaveSuffix("…"))
	})

	It("does not truncate a message that fits", func() {
		result := install.Result{Check: passingCheck, Status: install.Pass, Message: "short message"}

		got := statusLine(result, 80)

		Expect(got).To(ContainSubstring("short message"))
		Expect(got).NotTo(ContainSubstring("…"))
	})
})

var _ = Describe("padName", func() {
	It("right-pads a short name to the target width", func() {
		Expect(padName("abc", 6)).To(Equal("abc   "))
	})

	It("truncates a long name with an ellipsis instead of exceeding the width", func() {
		got := padName("abcdefghij", 6)
		Expect([]rune(got)).To(HaveLen(6))
		Expect(got).To(HaveSuffix("…"))
	})
})

var _ = Describe("progressColor", func() {
	It("is red below the low threshold", func() {
		Expect(progressColor(0)).To(Equal(lipgloss.Color(colorRed)))
		Expect(progressColor(0.49)).To(Equal(lipgloss.Color(colorRed)))
	})

	It("is yellow from the low threshold up to (not including) fully ready", func() {
		Expect(progressColor(0.5)).To(Equal(lipgloss.Color(colorYellow)))
		Expect(progressColor(0.99)).To(Equal(lipgloss.Color(colorYellow)))
	})

	It("is green once fully ready", func() {
		Expect(progressColor(1.0)).To(Equal(lipgloss.Color(colorGreen)))
	})
})

var _ = Describe("sections", func() {
	It("groups results under SERVICES and JOBS headers, services first", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass},
			{Check: requiredCheck, Status: install.Failed},
		}

		got := sections(results, 76)

		Expect(got).To(ContainSubstring("SERVICES"))
		Expect(got).To(ContainSubstring("JOBS"))
		Expect(strings.Index(got, "SERVICES")).To(BeNumerically("<", strings.Index(got, "JOBS")))
	})

	It("omits a section header when no result belongs to that category", func() {
		results := []install.Result{{Check: passingCheck, Status: install.Pass}}

		got := sections(results, 76)

		Expect(got).To(ContainSubstring("SERVICES"))
		Expect(got).NotTo(ContainSubstring("JOBS"))
	})

	It("groups prerequisite results under RESOURCES and OPERATORS, after the install-progress sections", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass},
			{Check: resourceCheck, Status: install.Failed},
			{Check: operatorCheck, Status: install.Failed},
		}

		got := sections(results, 76)

		Expect(got).To(ContainSubstring("RESOURCES"))
		Expect(got).To(ContainSubstring("OPERATORS"))
		Expect(strings.Index(got, "SERVICES")).To(BeNumerically("<", strings.Index(got, "RESOURCES")))
		Expect(strings.Index(got, "RESOURCES")).To(BeNumerically("<", strings.Index(got, "OPERATORS")))
	})
})

var _ = Describe("osacFont", func() {
	It("has every glyph at a consistent height and per-glyph row width", func() {
		for letter, glyph := range osacFont {
			Expect(glyph).To(HaveLen(osacFontHeight), "letter %q", string(letter))
			width := len([]rune(glyph[0]))
			for i, row := range glyph {
				Expect([]rune(row)).To(HaveLen(width), "letter %q row %d", string(letter), i)
			}
		}
	})
})

var _ = Describe("banner", func() {
	It("renders osacFontHeight lines plus a trailing blank line", func() {
		got := banner()

		lines := strings.Split(got, "\n")
		// osacFontHeight content lines + 1 blank line from the trailing
		// "\n\n" + the empty string split produces after the final "\n".
		Expect(lines).To(HaveLen(osacFontHeight + 2))
		Expect(lines[osacFontHeight]).To(BeEmpty())
	})
})

var _ = Describe("statusIcon", func() {
	It("shows a check mark for a passing check regardless of severity", func() {
		icon, _ := statusIcon(install.Result{Check: requiredCheck, Status: install.Pass})
		Expect(icon).To(Equal("✓"))
	})

	It("shows an X for a failed Required check", func() {
		icon, _ := statusIcon(install.Result{Check: requiredCheck, Status: install.Failed})
		Expect(icon).To(Equal("✗"))
	})

	It("shows an X for a failed Warning check too, just styled differently", func() {
		icon, _ := statusIcon(install.Result{Check: warningCheck, Status: install.Failed})
		Expect(icon).To(Equal("✗"))
	})

	It("shows a distinct icon and color for Progressing, never the Failed X", func() {
		icon, style := statusIcon(install.Result{Check: requiredCheck, Status: install.Progressing})
		Expect(icon).NotTo(Equal("✗"))
		Expect(icon).NotTo(Equal("✓"))
		Expect(style.GetForeground()).To(Equal(lipgloss.Color(colorCyan)))
	})
})
