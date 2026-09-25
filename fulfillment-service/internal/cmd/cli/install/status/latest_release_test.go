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
	"io"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

// stubHTTPResponse sets httpDo (restored automatically after the spec via
// DeferCleanup) to return a canned response for latestOSACReleaseVersion's
// request, without making any real network call.
func stubHTTPResponse(statusCode int, body string) {
	original := httpDo
	DeferCleanup(func() { httpDo = original })
	httpDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
}

func stubHTTPError(err error) {
	original := httpDo
	DeferCleanup(func() { httpDo = original })
	httpDo = func(*http.Request) (*http.Response, error) {
		return nil, err
	}
}

var _ = Describe("latestOSACReleaseVersion", func() {
	It("returns the highest real (non-nightly) osac/vX.Y.Z tag", func() {
		stubHTTPResponse(http.StatusOK, `[
			{"ref": "refs/tags/osac/v0.0.9"},
			{"ref": "refs/tags/osac/v0.0.21"},
			{"ref": "refs/tags/osac/v0.0.18"},
			{"ref": "refs/tags/osac/v0.0.9-nightly.20260910.abc123.80.1"}
		]`)

		version, err := latestOSACReleaseVersion(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(version).To(Equal("0.0.21"))
	})

	It("sorts numerically, not lexicographically (0.0.9 must lose to 0.0.21)", func() {
		stubHTTPResponse(http.StatusOK, `[
			{"ref": "refs/tags/osac/v0.0.21"},
			{"ref": "refs/tags/osac/v0.0.9"}
		]`)

		version, err := latestOSACReleaseVersion(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(version).To(Equal("0.0.21"))
	})

	It("fails when the request itself fails (offline, DNS failure, ...)", func() {
		stubHTTPError(errors.New("no route to host"))

		_, err := latestOSACReleaseVersion(context.Background())

		Expect(err).To(HaveOccurred())
	})

	It("fails on a non-200 response", func() {
		stubHTTPResponse(http.StatusServiceUnavailable, `{}`)

		_, err := latestOSACReleaseVersion(context.Background())

		Expect(err).To(HaveOccurred())
	})

	It("fails on malformed JSON", func() {
		stubHTTPResponse(http.StatusOK, `not json`)

		_, err := latestOSACReleaseVersion(context.Background())

		Expect(err).To(HaveOccurred())
	})

	It("fails when there are no matching real release tags", func() {
		stubHTTPResponse(http.StatusOK, `[
			{"ref": "refs/tags/osac/v0.0.9-nightly.20260910.abc123.80.1"},
			{"ref": "refs/tags/fulfillment-service/v0.0.111"}
		]`)

		_, err := latestOSACReleaseVersion(context.Background())

		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("pickDisplayVersion", func() {
	It("returns empty when the chart version itself is unknown", func() {
		Expect(pickDisplayVersion("", "0.0.21")).To(BeEmpty())
	})

	It("falls back to the chart version when the latest-release lookup found nothing", func() {
		Expect(pickDisplayVersion("0.0.1", "")).To(Equal("0.0.1"))
	})

	It("prefers the latest release when the chart version is stale (the placeholder case)", func() {
		Expect(pickDisplayVersion("0.0.1", "0.0.21")).To(Equal("0.0.21"))
	})

	It("keeps the chart version when it's already at least as new as the latest release", func() {
		Expect(pickDisplayVersion("0.0.21", "0.0.9")).To(Equal("0.0.21"))
		Expect(pickDisplayVersion("0.0.21", "0.0.21")).To(Equal("0.0.21"))
	})
})

var _ = Describe("compareVersions", func() {
	It("compares numerically, not lexicographically", func() {
		Expect(compareVersions("0.0.9", "0.0.21")).To(Equal(-1))
		Expect(compareVersions("0.0.21", "0.0.9")).To(Equal(1))
		Expect(compareVersions("1.2.3", "1.2.3")).To(Equal(0))
	})

	It("treats an unparseable version as lower than a parseable one", func() {
		Expect(compareVersions("not-a-version", "0.0.1")).To(Equal(-1))
		Expect(compareVersions("0.0.1", "not-a-version")).To(Equal(1))
	})

	It("falls back to a plain string compare when neither side parses", func() {
		Expect(compareVersions("abc", "abd")).To(Equal(-1))
	})
})
