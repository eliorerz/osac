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
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// encodeHelmRelease reproduces Helm's own release Secret encoding
// (base64(gzip(json(release)))) so tests can seed a fake Secret exactly as
// a real `helm install`/`upgrade` would, without depending on Helm's SDK.
func encodeHelmRelease(release helmRelease) []byte {
	data, err := json.Marshal(release)
	Expect(err).NotTo(HaveOccurred())

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err = gz.Write(data)
	Expect(err).NotTo(HaveOccurred())
	Expect(gz.Close()).To(Succeed())

	return []byte(base64.StdEncoding.EncodeToString(buf.Bytes()))
}

// newHelmReleaseSecret builds a fake Helm release Secret for namespace,
// exactly as `helm install`/`upgrade` would name and label it, so
// latestHelmRelease's own label selector (owner=helm,name=<name>) finds it.
func newHelmReleaseSecret(namespace, name string, version int, manifest string, hookManifests ...string) *corev1.Secret {
	hooks := make([]helmHook, len(hookManifests))
	for i, m := range hookManifests {
		hooks[i] = helmHook{Manifest: m}
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("sh.helm.release.v1.%s.v%d", name, version),
			Namespace: namespace,
			Labels:    map[string]string{"owner": "helm", "name": name},
		},
		Data: map[string][]byte{
			"release": encodeHelmRelease(helmRelease{Manifest: manifest, Hooks: hooks, Version: version}),
		},
	}
}

var _ = Describe("decodeHelmRelease", func() {
	It("round-trips a release through Helm's own base64(gzip(json())) encoding", func() {
		encoded := encodeHelmRelease(helmRelease{Manifest: "kind: Deployment", Version: 3})

		release, err := decodeHelmRelease(encoded)

		Expect(err).NotTo(HaveOccurred())
		Expect(release.Manifest).To(Equal("kind: Deployment"))
		Expect(release.Version).To(Equal(3))
	})

	It("fails on data that isn't valid base64", func() {
		_, err := decodeHelmRelease([]byte("not base64!!!"))
		Expect(err).To(HaveOccurred())
	})

	It("fails on valid base64 that isn't gzip", func() {
		_, err := decodeHelmRelease([]byte(base64.StdEncoding.EncodeToString([]byte("plain text"))))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("latestHelmRelease", func() {
	It("returns nil, not an error, when no release secret exists", func() {
		clients := newFakeClients(nil, nil)

		release, err := latestHelmRelease(context.Background(), clients, "osac", "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(release).To(BeNil())
	})

	It("picks the highest-version release when multiple revisions exist", func() {
		clients := newFakeClients([]runtime.Object{
			newHelmReleaseSecret("osac", "osac", 1, "kind: Deployment\nmetadata:\n  name: v1-only"),
			newHelmReleaseSecret("osac", "osac", 3, "kind: Deployment\nmetadata:\n  name: v3-only"),
			newHelmReleaseSecret("osac", "osac", 2, "kind: Deployment\nmetadata:\n  name: v2-only"),
		}, nil)

		release, err := latestHelmRelease(context.Background(), clients, "osac", "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(release.Version).To(Equal(3))
		Expect(release.Manifest).To(ContainSubstring("v3-only"))
	})

	It("ignores a release secret for a different release name", func() {
		clients := newFakeClients([]runtime.Object{
			newHelmReleaseSecret("osac", "some-other-release", 1, "kind: Deployment"),
		}, nil)

		release, err := latestHelmRelease(context.Background(), clients, "osac", "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(release).To(BeNil())
	})
})

var _ = Describe("expectedWorkloads", func() {
	It("finds a Deployment in the plain manifest", func() {
		release := &helmRelease{Manifest: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: fulfillment-grpc-server\n"}

		items, err := expectedWorkloads(release)

		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(ContainElement(expectedWorkload{Kind: "Deployment", Name: "fulfillment-grpc-server"}))
	})

	It("finds a Job declared in a hook, not just the plain manifest", func() {
		release := &helmRelease{
			Manifest: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: fulfillment-grpc-server\n",
			Hooks: []helmHook{
				{Manifest: "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: osac-aap-bootstrap\n"},
			},
		}

		items, err := expectedWorkloads(release)

		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(ContainElement(expectedWorkload{Kind: "Job", Name: "osac-aap-bootstrap"}))
		Expect(items).To(ContainElement(expectedWorkload{Kind: "Deployment", Name: "fulfillment-grpc-server"}))
	})

	It("ignores kinds that aren't workloads (Secret, ConfigMap, RBAC, ...)", func() {
		release := &helmRelease{
			Manifest: "apiVersion: v1\nkind: Secret\nmetadata:\n  name: some-secret\n",
		}

		items, err := expectedWorkloads(release)

		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(BeEmpty())
	})

	It("handles multiple YAML documents separated by ---, including blank ones a false 'if' renders", func() {
		release := &helmRelease{
			Manifest: "---\n# Source: osac/templates/a.yaml\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: a\n---\n# Source: osac/templates/b.yaml (rendered empty by a false if)\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: b\n",
		}

		items, err := expectedWorkloads(release)

		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(ContainElement(expectedWorkload{Kind: "Deployment", Name: "a"}))
		Expect(items).To(ContainElement(expectedWorkload{Kind: "Deployment", Name: "b"}))
	})

	It("deduplicates the same kind+name declared more than once", func() {
		release := &helmRelease{
			Manifest: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: dup\n",
			Hooks: []helmHook{
				{Manifest: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: dup\n"},
			},
		}

		items, err := expectedWorkloads(release)

		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(1))
	})

	It("fails clearly on a manifest that isn't valid YAML", func() {
		release := &helmRelease{Manifest: "not: valid: yaml: at: all: [[["}

		_, err := expectedWorkloads(release)

		Expect(err).To(HaveOccurred())
	})
})
