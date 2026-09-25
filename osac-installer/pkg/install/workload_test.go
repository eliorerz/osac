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
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func int32Ptr(i int32) *int32 { return &i }

// namespaceObj is the Namespace object every test below needs alongside
// its workload fixtures: WorkloadChecks checks the namespace itself before
// listing anything in it, and the fake clientset (like a real API server)
// doesn't implicitly create a Namespace object just because some other
// object references its name.
func namespaceObj(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// byName indexes results by check name, so assertions don't depend on the
// namespace result's fixed position in the slice.
func byName(results []Result) map[string]Result {
	m := map[string]Result{}
	for _, r := range results {
		m[r.Check.Name] = r
	}
	return m
}

var _ = Describe("WorkloadChecks", func() {
	It("passes a Deployment whose ready replicas match desired", func() {
		clients := newFakeClients([]runtime.Object{
			namespaceObj("osac"),
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "fulfillment-grpc-server", Namespace: "osac"},
				Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(2)},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: 2},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(2)) // the namespace itself + the Deployment
		d := byName(results)["fulfillment-grpc-server"]
		Expect(d.Check.Category).To(Equal(ServiceCategory))
		Expect(d.Status).To(Equal(Pass))
		Expect(d.Message).To(ContainSubstring("2/2"))
	})

	It("fails a Deployment whose ready replicas are below desired", func() {
		clients := newFakeClients([]runtime.Object{
			namespaceObj("osac"),
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "osac-ui", Namespace: "osac"},
				Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: 0},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(byName(results)["osac-ui"].Status).To(Equal(Failed))
	})

	It("defaults desired replicas to 1 when spec.replicas is nil", func() {
		clients := newFakeClients([]runtime.Object{
			namespaceObj("osac"),
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "osac-operator", Namespace: "osac"},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		op := byName(results)["osac-operator"]
		Expect(op.Status).To(Equal(Pass))
		Expect(op.Message).To(ContainSubstring("1/1"))
	})

	It("checks StatefulSets and DaemonSets the same way, categorized as Service", func() {
		clients := newFakeClients([]runtime.Object{
			namespaceObj("osac"),
			&appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: "openbao", Namespace: "osac"},
				Spec:       appsv1.StatefulSetSpec{Replicas: int32Ptr(1)},
				Status:     appsv1.StatefulSetStatus{ReadyReplicas: 1},
			},
			&appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Name: "some-daemon", Namespace: "osac"},
				Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 2},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(3)) // namespace + StatefulSet + DaemonSet
		names := byName(results)
		Expect(names["openbao"].Status).To(Equal(Pass))
		Expect(names["openbao"].Check.Category).To(Equal(ServiceCategory))
		Expect(names["some-daemon"].Status).To(Equal(Failed))
		Expect(names["some-daemon"].Message).To(ContainSubstring("2/3"))
	})

	It("marks a succeeded Job as passing", func() {
		clients := newFakeClients([]runtime.Object{
			namespaceObj("osac"),
			&batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "osac-db-init", Namespace: "osac"},
				Status:     batchv1.JobStatus{Succeeded: 1},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		job := byName(results)["osac-db-init"]
		Expect(job.Check.Category).To(Equal(JobCategory))
		Expect(job.Status).To(Equal(Pass))
		Expect(job.Message).To(Equal("completed"))
	})

	It("marks a failed Job as Failed/Required", func() {
		clients := newFakeClients([]runtime.Object{
			namespaceObj("osac"),
			&batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "osac-aap-bootstrap", Namespace: "osac"},
				Status:     batchv1.JobStatus{Failed: 1},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		job := byName(results)["osac-aap-bootstrap"]
		Expect(job.Check.Severity).To(Equal(Required))
		Expect(job.Status).To(Equal(Failed))
		Expect(job.Message).To(Equal("failed"))
	})

	It("marks a still-running Job as Failed/Warning, not Failed/Required -- it's expected, not broken", func() {
		clients := newFakeClients([]runtime.Object{
			namespaceObj("osac"),
			&batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "osac-aap-bootstrap", Namespace: "osac"},
				Status:     batchv1.JobStatus{},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		job := byName(results)["osac-aap-bootstrap"]
		Expect(job.Check.Severity).To(Equal(Warning))
		Expect(job.Status).To(Equal(Failed))
		Expect(job.Message).To(Equal("in progress"))
	})

	It("only lists workloads in the given namespace", func() {
		clients := newFakeClients([]runtime.Object{
			namespaceObj("osac"),
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "in-ns", Namespace: "osac"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "other-ns", Namespace: "kube-system"}},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		names := byName(results)
		Expect(names).To(HaveKey("in-ns"))
		Expect(names).NotTo(HaveKey("other-ns"))
	})

	It("reports the namespace itself as Pass/Required when it exists but is otherwise empty", func() {
		clients := newFakeClients([]runtime.Object{namespaceObj("osac")}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Check.Category).To(Equal(NamespaceCategory))
		Expect(results[0].Check.Severity).To(Equal(Required))
		Expect(results[0].Status).To(Equal(Pass))
		Expect(results[0].Message).To(Equal("exists"))
	})

	It("reports the namespace as Failed/Warning, and lists nothing else, when it doesn't exist yet", func() {
		clients := newFakeClients(nil, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Check.Category).To(Equal(NamespaceCategory))
		Expect(results[0].Check.Severity).To(Equal(Warning))
		Expect(results[0].Status).To(Equal(Failed))
		Expect(results[0].Message).To(Equal("not created yet"))
	})
})

var _ = Describe("jobResult and friends don't panic on zero-value objects", func() {
	It("handles a Deployment/StatefulSet/DaemonSet/Job with no status set at all", func() {
		Expect(func() { deploymentResult(appsv1.Deployment{}) }).NotTo(Panic())
		Expect(func() { statefulSetResult(appsv1.StatefulSet{}) }).NotTo(Panic())
		Expect(func() { daemonSetResult(appsv1.DaemonSet{}) }).NotTo(Panic())
		Expect(func() { jobResult(batchv1.Job{}) }).NotTo(Panic())
	})
})
