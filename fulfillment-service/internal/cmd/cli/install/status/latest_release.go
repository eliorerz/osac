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
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// latestReleaseTimeout bounds how long the best-effort check against
// GitHub for the latest published "osac" release waits before giving up.
// This must never make `osac install status` feel like it's hanging when
// there's no internet access -- a failed or slow lookup just falls back to
// the chart's own (possibly unstamped) version, unchanged from before this
// existed.
const latestReleaseTimeout = 3 * time.Second

// httpDo performs latestOSACReleaseVersion's HTTP request. A package-level
// var (not a hardcoded http.DefaultClient.Do call) so tests can stub it
// out -- unit tests must never depend on real network egress, and this
// codebase's own sandboxed CI/dev environments frequently have none.
var httpDo = http.DefaultClient.Do

// osacReleaseTagPattern matches a real, non-nightly "osac" umbrella chart
// release tag (e.g. "osac/v0.1.0") -- excludes the "-nightly.<suffix>"
// pre-release tags osac-build-and-publish.yaml also creates for every
// nightly build. Mirrors resolve_release_tag's own validation regex in
// osac-installer/scripts/lib.sh, which resolves the same thing from local
// git tags during a release build; this is the network equivalent for a
// deployed osac binary, which (unlike a release-pipeline runner) has no
// local git checkout of the source repo to read tags from directly.
var osacReleaseTagPattern = regexp.MustCompile(`^osac/v\d+\.\d+\.\d+$`)

// pickDisplayVersion chooses what to show as "the osac version" in the
// phase title: chartVersion (read from the Helm release's own recorded
// Chart.Metadata.Version, see install.ChartVersion) if it's already at
// least as new as latestRelease, or latestRelease otherwise.
//
// Why this is needed at all: git-committed Chart.yaml carries a permanent
// placeholder version ("0.0.1" as of this writing) that's only overwritten
// at `helm package` time during the actual release pipeline (see
// osac-build-and-publish.yaml's "Package umbrella chart" step) -- never
// committed back to source. An install built directly from source (main, a
// PR branch, a local checkout, exactly what a dev/CI install does) always
// reports that stale placeholder, not a real version. Confirmed live: a
// vmaas-ci test install reported chart version "0.0.1" while the real
// latest published osac release was 0.0.21.
//
// latestRelease may be empty (lookup never completed, failed, or found
// nothing) -- pickDisplayVersion falls back to chartVersion as-is in that
// case, which is the "if no internet access, take from the chart" behavior
// this exists to preserve. Pure and side-effect-free so it's usable both
// right after a fresh network fetch (the one-shot render path) and against
// a previously cached fetch result (the --watch path, which fetches once
// at Init rather than on every re-check -- see watchModel's rate-limit
// comment).
func pickDisplayVersion(chartVersion, latestRelease string) string {
	if chartVersion == "" {
		return ""
	}
	if latestRelease == "" {
		return chartVersion
	}
	if compareVersions(chartVersion, latestRelease) >= 0 {
		return chartVersion
	}
	return latestRelease
}

// latestOSACReleaseVersion queries GitHub for the highest real (non-
// nightly) "osac/vX.Y.Z" tag on osac-project/osac, returning just the
// "X.Y.Z" part (no "osac/v" prefix) -- the same shape install.ChartVersion
// already returns, so both can be compared and displayed uniformly.
func latestOSACReleaseVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, latestReleaseTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/osac-project/osac/git/matching-refs/tags/osac/v", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpDo(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d fetching latest osac release tags", resp.StatusCode)
	}

	var refs []struct {
		Ref string `json:"ref"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&refs); err != nil {
		return "", err
	}

	var versions []string
	for _, r := range refs {
		tag := strings.TrimPrefix(r.Ref, "refs/tags/")
		if osacReleaseTagPattern.MatchString(tag) {
			versions = append(versions, strings.TrimPrefix(tag, "osac/v"))
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no real osac/vX.Y.Z release tags found")
	}
	sort.Slice(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) < 0 })
	return versions[len(versions)-1], nil
}

// compareVersions compares two plain "X.Y.Z" version strings numerically,
// not lexicographically -- "0.0.9" must sort before "0.0.21", which a
// plain string comparison would get backwards. Returns -1, 0, or 1 like
// strings.Compare. A version that doesn't parse as three dot-separated
// non-negative integers sorts as lower than one that does (falling back to
// a plain string compare if neither parses), rather than erroring -- this
// is a best-effort comparison backing a best-effort feature, not a
// validating parser.
func compareVersions(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka && !okb {
		return strings.Compare(a, b)
	}
	if !oka {
		return -1
	}
	if !okb {
		return 1
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
