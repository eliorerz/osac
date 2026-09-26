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
	"fmt"
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

var _ = Describe("phaseTitle", func() {
	It("reads 'Installing OSAC...' once any workload besides pre-install-validate has been created", func() {
		results := []install.Result{{Check: passingCheck, Status: install.Pass, Message: "1/1 replicas ready"}}

		Expect(phaseTitle(results, "")).To(ContainSubstring("Installing OSAC"))
	})

	It("reads 'Validating prerequisites...' while nothing besides pre-install-validate has been created yet", func() {
		validateCheck := install.Check{Name: preInstallValidateJobName, Category: install.JobCategory}
		results := []install.Result{
			{Check: validateCheck, Status: install.Progressing, Message: "not created yet"},
			{Check: warningCheck, Status: install.Progressing, Message: "not created yet"}, // some other Job, also not created yet
		}

		Expect(phaseTitle(results, "")).To(ContainSubstring("Validating prerequisites"))
	})

	It("reads 'Installing OSAC...' even when pre-install-validate's own Job has disappeared (deleted on a retried upgrade)", func() {
		// No pre-install-validate result at all here -- Helm's
		// hook-delete-policy can remove it even after it already
		// succeeded, on a retried install/upgrade (confirmed live). The
		// signal has to come from elsewhere having actually been created.
		results := []install.Result{{Check: passingCheck, Status: install.Progressing, Message: "0/1 replicas ready"}}

		Expect(phaseTitle(results, "")).To(ContainSubstring("Installing OSAC"))
	})

	It("ignores prerequisite results (Resources/Operators) when deciding the phase", func() {
		results := []install.Result{
			{Check: resourceCheck, Status: install.Pass, Message: "default StorageClass found"},
			{Check: operatorCheck, Status: install.Pass, Message: "installed"},
		}

		Expect(phaseTitle(results, "")).To(ContainSubstring("Validating prerequisites"))
	})

	It("includes the chart version in both phases, when known", func() {
		validateCheck := install.Check{Name: preInstallValidateJobName, Category: install.JobCategory}
		validating := []install.Result{{Check: validateCheck, Status: install.Progressing, Message: "not created yet"}}
		installing := []install.Result{{Check: passingCheck, Status: install.Pass, Message: "1/1 replicas ready"}}

		Expect(phaseTitle(validating, "0.1.0")).To(ContainSubstring("v0.1.0"))
		Expect(phaseTitle(installing, "0.1.0")).To(ContainSubstring("v0.1.0"))
	})

	It("omits the version entirely when it isn't known yet", func() {
		results := []install.Result{{Check: passingCheck, Status: install.Pass}}

		Expect(phaseTitle(results, "")).To(ContainSubstring("Installing OSAC..."))
	})
})

var _ = Describe("renderStatus", func() {
	It("includes the passed/total count", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "1/1 replicas ready"},
			{Check: requiredCheck, Status: install.Failed, Message: "failed"},
		}

		got := renderStatus(results, 80, 0, "")

		Expect(got).To(ContainSubstring("1/2 ready"))
	})

	It("includes the phase title above the progress bar", func() {
		results := []install.Result{{Check: passingCheck, Status: install.Pass}}

		got := renderStatus(results, 80, 0, "")

		Expect(got).To(ContainSubstring("Installing OSAC"))
	})

	It("includes every check's name and message", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "1/1 replicas ready"},
			{Check: warningCheck, Status: install.Failed, Message: "in progress"},
		}

		got := renderStatus(results, 80, 0, "")

		Expect(got).To(ContainSubstring("fulfillment-grpc-server"))
		Expect(got).To(ContainSubstring("1/1 replicas ready"))
		Expect(got).To(ContainSubstring("osac-aap-bootstrap"))
		Expect(got).To(ContainSubstring("in progress"))
	})

	It("handles zero results without panicking", func() {
		got := renderStatus(nil, 80, 0, "")

		Expect(got).To(ContainSubstring("0/0 ready"))
		Expect(got).To(ContainSubstring("No OSAC workloads found"))
	})

	It("falls back to a default width when given 0 or a negative width", func() {
		Expect(func() { renderStatus([]install.Result{{Check: passingCheck, Status: install.Pass}}, 0, 0, "") }).NotTo(Panic())
		Expect(func() { renderStatus([]install.Result{{Check: passingCheck, Status: install.Pass}}, -5, 0, "") }).NotTo(Panic())
	})

	It("fits the frame to a wide terminal instead of capping it, leaving a gutter unused", func() {
		got := renderStatus([]install.Result{{Check: passingCheck, Status: install.Pass}}, 131, 0, "")

		lines := strings.Split(got, "\n")
		Expect(lines).NotTo(BeEmpty())
		// The frame border line should span the full requested width, not
		// stop short at some smaller cap.
		Expect(lipgloss.Width(lines[0])).To(Equal(131))
	})

	It("excludes prerequisite results (Resources/Operators) from the progress bar's ready count", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass},    // counts: pass
			{Check: requiredCheck, Status: install.Pass},   // counts: pass
			{Check: resourceCheck, Status: install.Failed}, // prerequisite -- doesn't count
			{Check: operatorCheck, Status: install.Failed}, // prerequisite -- doesn't count
		}

		got := renderStatus(results, 80, 0, "")

		Expect(got).To(ContainSubstring("2/2 ready"))
	})

	It("does not report 100% ready off the namespace alone, before any workload has been discovered", func() {
		namespaceCheck := install.Check{Name: "osac", Severity: install.Required, Category: install.NamespaceCategory}
		results := []install.Result{
			{Check: namespaceCheck, Status: install.Pass, Message: "exists"},
		}

		got := renderStatus(results, 80, 0, "")

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

		got := statusLine(result, 80, 0)

		Expect(got).NotTo(ContainSubstring("\n"))
		Expect(got).To(ContainSubstring("…"))
		Expect(got).To(ContainSubstring("short"))
	})

	It("ellipsizes an overly long message instead of cutting it off mid-word with no indicator", func() {
		longMessage := "this message is deliberately much longer than any reasonable terminal-width budget for the message column and must be shortened"
		result := install.Result{Check: passingCheck, Status: install.Pass, Message: longMessage}

		got := statusLine(result, 80, 0)

		Expect(got).NotTo(ContainSubstring("\n"))
		Expect(got).To(HaveSuffix("…"))
	})

	It("does not truncate a message that fits", func() {
		result := install.Result{Check: passingCheck, Status: install.Pass, Message: "short message"}

		got := statusLine(result, 80, 0)

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

var _ = Describe("progressLine", func() {
	It("stays on one line at ordinary terminal widths, never wrapping the trailing ready count", func() {
		var results []install.Result
		for i := range 20 {
			results = append(results, install.Result{
				Check:  install.Check{Name: fmt.Sprintf("service-%d", i), Category: install.ServiceCategory},
				Status: install.Pass,
			})
		}

		for _, width := range []int{40, 76, 96, 127} {
			got := progressLine(results, width)
			Expect(got).NotTo(ContainSubstring("\n"), "width %d produced a wrapped progress line: %q", width, got)
			Expect(got).To(ContainSubstring("20/20 ready"))
		}
	})

	It("excludes Blocked results from both the numerator and the denominator", func() {
		results := []install.Result{
			{Check: install.Check{Name: "a", Category: install.JobCategory}, Status: install.Pass},
			{Check: install.Check{Name: "b", Category: install.JobCategory}, Status: install.Failed},
			// Two Blocked jobs that can never complete without a retry --
			// if counted, this would read "2/4 ready" (50%) instead of the
			// honest "2/2 ready" (100% of what can actually run right now).
			{Check: install.Check{Name: "c", Category: install.JobCategory}, Status: install.Blocked},
			{Check: install.Check{Name: "d", Category: install.JobCategory}, Status: install.Blocked},
		}

		got := progressLine(results, 76)

		Expect(got).To(ContainSubstring("1/2 ready"))
	})
})

var _ = Describe("progressColor", func() {
	It("is green when nothing has genuinely failed, no matter how little is ready yet", func() {
		Expect(progressColor(false)).To(Equal(lipgloss.Color(colorGreen)))
	})

	It("is red once something has genuinely failed", func() {
		Expect(progressColor(true)).To(Equal(lipgloss.Color(colorRed)))
	})
})

var _ = Describe("sections", func() {
	It("groups results under SERVICES and JOBS headers, services first", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass},
			{Check: requiredCheck, Status: install.Failed},
		}

		got := sections(results, 76, 0)

		Expect(got).To(ContainSubstring("SERVICES"))
		Expect(got).To(ContainSubstring("JOBS"))
		Expect(strings.Index(got, "SERVICES")).To(BeNumerically("<", strings.Index(got, "JOBS")))
	})

	It("omits a section header when no result belongs to that category", func() {
		results := []install.Result{{Check: passingCheck, Status: install.Pass}}

		got := sections(results, 76, 0)

		Expect(got).To(ContainSubstring("SERVICES"))
		Expect(got).NotTo(ContainSubstring("JOBS"))
	})

	It("groups prerequisite results under RESOURCES and OPERATORS, after the install-progress sections", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass},
			{Check: resourceCheck, Status: install.Failed},
			{Check: operatorCheck, Status: install.Failed},
		}

		got := sections(results, 76, 0)

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
		icon, _ := statusIcon(install.Result{Check: requiredCheck, Status: install.Pass}, 0)
		Expect(icon).To(Equal("✓"))
	})

	It("shows an X for a failed Required check", func() {
		icon, _ := statusIcon(install.Result{Check: requiredCheck, Status: install.Failed}, 0)
		Expect(icon).To(Equal("✗"))
	})

	It("shows a warning triangle, not the X, for a failed Warning check", func() {
		icon, style := statusIcon(install.Result{Check: warningCheck, Status: install.Failed}, 0)
		Expect(icon).To(Equal("⚠"))
		Expect(icon).NotTo(Equal("✗"))
		Expect(style.GetForeground()).To(Equal(lipgloss.Color(colorYellow)))
	})

	It("shows a distinct icon and color for Progressing, never the Failed X", func() {
		icon, style := statusIcon(install.Result{Check: requiredCheck, Status: install.Progressing}, 0)
		Expect(icon).NotTo(Equal("✗"))
		Expect(icon).NotTo(Equal("✓"))
		Expect(style.GetForeground()).To(Equal(lipgloss.Color(colorBlue)))
	})

	It("animates the Progressing spinner across frames as spinnerFrame advances", func() {
		result := install.Result{Check: requiredCheck, Status: install.Progressing}

		first, _ := statusIcon(result, 0)
		second, _ := statusIcon(result, 1)

		Expect(first).NotTo(Equal(second))
		Expect(spinnerFrames).To(ContainElement(first))
		Expect(spinnerFrames).To(ContainElement(second))
	})

	It("wraps the spinner frame index instead of panicking on an out-of-range value", func() {
		Expect(func() {
			statusIcon(install.Result{Check: requiredCheck, Status: install.Progressing}, len(spinnerFrames)+3)
		}).NotTo(Panic())
	})

	It("shows a static, dim icon (not the animated spinner) for something not created yet", func() {
		result := install.Result{Check: requiredCheck, Status: install.Progressing, Message: notYetCreatedMessage}

		icon, style := statusIcon(result, 0)

		Expect(spinnerFrames).NotTo(ContainElement(icon))
		Expect(style.GetFaint()).To(BeTrue())
		Expect(style.GetForeground()).To(Equal(lipgloss.Color(colorGray)))
	})

	It("shows a distinct, dim red icon for Blocked -- neither the plain queued gray nor the bold Failed X", func() {
		result := install.Result{Check: requiredCheck, Status: install.Blocked, Message: `blocked ("osac-aap-bootstrap" failed)`}

		icon, style := statusIcon(result, 0)

		Expect(icon).NotTo(Equal("✗"))
		Expect(style.GetFaint()).To(BeTrue())
		Expect(style.GetForeground()).To(Equal(lipgloss.Color(colorRed)))
	})

	It("doesn't animate the not-created-yet icon across spinner frames, unlike an actively-progressing one", func() {
		result := install.Result{Check: requiredCheck, Status: install.Progressing, Message: notYetCreatedMessage}

		first, _ := statusIcon(result, 0)
		second, _ := statusIcon(result, 1)

		Expect(first).To(Equal(second))
	})
})
