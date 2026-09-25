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
	"net/http"
	"testing"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

func TestStatus(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Status command")
}

// Reset httpDo (latest_release.go's injection point for
// latestOSACReleaseVersion's HTTP call) to a fast, deterministic "offline"
// stub before every spec, regardless of which Describe it's in: unit tests
// must never depend on real network egress, and without this, any spec
// that exercises watchModel.Init() (which now always fires a
// latestReleaseCmd as part of its batch) would otherwise make a real
// request to api.github.com. A spec that wants to test
// latestOSACReleaseVersion's own success/parsing path overrides httpDo
// itself and restores it via DeferCleanup, same as any other test-local
// stub.
var _ = BeforeEach(func() {
	httpDo = func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network access disabled in tests")
	}
})
