/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

// helmReleaseName is the Helm release name every install-osac invocation in
// this repo uses (`helm upgrade --install osac charts/osac ...`) --
// hardcoded rather than a flag/parameter since nothing in this codebase
// (Makefile, docs, CI) ever installs it under a different name.
const helmReleaseName = "osac"

// helmRelease is the minimal subset of Helm 3's internal release.Release
// struct WorkloadChecks needs: the rendered manifest (every plain resource
// this release creates) and its hooks (pre/post-install/upgrade resources,
// stored separately from Manifest since Helm applies and deletes them on a
// different lifecycle than the chart's normal templates). Decoding just
// these fields directly from the release Secret -- rather than depending
// on helm.sh/helm/v3 -- avoids pulling in Helm's full SDK (and its own
// pinned client-go version, which risks conflicting with this repo's) for
// what's otherwise a read of a handful of fields from a storage format
// that's been stable across Helm 3 since its first release (see Helm's own
// pkg/storage/driver.decodeRelease, which this mirrors).
type helmRelease struct {
	Manifest string     `json:"manifest"`
	Hooks    []helmHook `json:"hooks"`
	Version  int        `json:"version"`
	Chart    *helmChart `json:"chart"`
}

type helmHook struct {
	Manifest string `json:"manifest"`
}

// helmChart is the minimal subset of a release's embedded chart this
// package needs: just enough to read the chart's own version (Chart.yaml's
// "version" field, e.g. "0.1.0") -- not the app version, and not anything
// about the chart's actual templates (those live in Manifest/Hooks, read
// separately).
type helmChart struct {
	Metadata *helmChartMetadata `json:"metadata"`
}

type helmChartMetadata struct {
	Version string `json:"version"`
}

// ChartVersion returns the currently recorded "osac" Helm release's chart
// version (Chart.yaml's own "version" field, e.g. "0.1.0" -- not the app
// version, and not the release's revision number, which is a different
// "version" field on the release itself). Returns "" (not an error) when
// no release has been recorded yet -- the same "nothing to report yet"
// case latestHelmRelease itself treats as normal, not a failure.
func ChartVersion(ctx context.Context, clients *Clients, namespace string) (string, error) {
	release, err := latestHelmRelease(ctx, clients, namespace, helmReleaseName)
	if err != nil {
		return "", err
	}
	if release == nil || release.Chart == nil || release.Chart.Metadata == nil {
		return "", nil
	}
	return release.Chart.Metadata.Version, nil
}

// latestHelmRelease reads and decodes the most recent (highest Version)
// Helm release Secret for name in namespace. Returns (nil, nil) -- not an
// error -- when no release Secret exists yet: the expected state before
// `helm install`'s own hooks (including pre-install-validate, which runs
// before Helm records the release at all) have finished, and WorkloadChecks
// falls back to discovering whatever's actually in the cluster for that
// case, the same as it always has.
func latestHelmRelease(ctx context.Context, clients *Clients, namespace, name string) (*helmRelease, error) {
	secrets, err := clients.Typed.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("owner=helm,name=%s", name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list Helm release secrets for %q in namespace %q: %w", name, namespace, err)
	}
	var latest *helmRelease
	for _, secret := range secrets.Items {
		release, err := decodeHelmRelease(secret.Data["release"])
		if err != nil {
			// A malformed or unrelated secret shouldn't prevent discovering
			// the real release among the others -- skip it rather than
			// failing the whole lookup.
			continue
		}
		if latest == nil || release.Version > latest.Version {
			latest = release
		}
	}
	return latest, nil
}

// decodeHelmRelease reverses Helm's own encoding of a release Secret's
// "release" data key: base64(gzip(json(release.Release))). The Kubernetes
// API already base64-decodes a Secret's data values once (that's the wire
// format for Secret.Data), so what's passed in here is Helm's own
// (separate) base64 layer on top of that.
func decodeHelmRelease(data []byte) (*helmRelease, error) {
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to base64-decode release data: %w", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(decoded))
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip reader for release data: %w", err)
	}
	defer gz.Close()
	jsonData, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress release data: %w", err)
	}
	var release helmRelease
	if err := json.Unmarshal(jsonData, &release); err != nil {
		return nil, fmt.Errorf("failed to unmarshal release JSON: %w", err)
	}
	return &release, nil
}

// expectedWorkload is one Deployment/StatefulSet/DaemonSet/Job this
// release's rendered manifest says should exist, whether or not it's been
// created in the cluster yet.
type expectedWorkload struct {
	Kind string
	Name string
}

// expectedWorkloadKinds are the resource kinds WorkloadChecks reports on;
// anything else in the manifest (Secrets, ConfigMaps, RBAC, ...) is beyond
// what "is the install progressing" needs to answer.
var expectedWorkloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"Job":         true,
}

// expectedWorkloads parses release's plain manifest and every hook's own
// manifest (hooks are stored separately from Manifest, not appended to it)
// for every Deployment/StatefulSet/DaemonSet/Job this release declares,
// deduplicated by kind+name -- a resource could in principle appear in
// both a hook and the plain manifest across upgrades, though not normally
// within one release.
func expectedWorkloads(release *helmRelease) ([]expectedWorkload, error) {
	manifests := make([]string, 0, len(release.Hooks)+1)
	manifests = append(manifests, release.Manifest)
	for _, hook := range release.Hooks {
		manifests = append(manifests, hook.Manifest)
	}

	seen := map[expectedWorkload]bool{}
	var items []expectedWorkload
	for _, manifest := range manifests {
		decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096)
		for {
			var doc struct {
				Kind     string `json:"kind"`
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
			}
			err := decoder.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("failed to parse Helm release manifest: %w", err)
			}
			if !expectedWorkloadKinds[doc.Kind] || doc.Metadata.Name == "" {
				continue
			}
			item := expectedWorkload{Kind: doc.Kind, Name: doc.Metadata.Name}
			if seen[item] {
				continue
			}
			seen[item] = true
			items = append(items, item)
		}
	}
	return items, nil
}
