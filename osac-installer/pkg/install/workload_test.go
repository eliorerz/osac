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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func int32Ptr(i int32) *int32 { return &i }

var _ = Describe("WorkloadChecks", func() {
	It("passes a Deployment whose ready replicas match desired", func() {
		clients := newFakeClients([]runtime.Object{
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "fulfillment-grpc-server", Namespace: "osac"},
				Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(2)},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: 2},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Check.Name).To(Equal("fulfillment-grpc-server"))
		Expect(results[0].Check.Category).To(Equal(ServiceCategory))
		Expect(results[0].Status).To(Equal(Pass))
		Expect(results[0].Message).To(ContainSubstring("2/2"))
	})

	It("fails a Deployment whose ready replicas are below desired", func() {
		clients := newFakeClients([]runtime.Object{
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "osac-ui", Namespace: "osac"},
				Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: 0},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results[0].Status).To(Equal(Failed))
	})

	It("defaults desired replicas to 1 when spec.replicas is nil", func() {
		clients := newFakeClients([]runtime.Object{
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "osac-operator", Namespace: "osac"},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results[0].Status).To(Equal(Pass))
		Expect(results[0].Message).To(ContainSubstring("1/1"))
	})

	It("checks StatefulSets and DaemonSets the same way, categorized as Service", func() {
		clients := newFakeClients([]runtime.Object{
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
		Expect(results).To(HaveLen(2))
		byName := map[string]Result{}
		for _, r := range results {
			byName[r.Check.Name] = r
		}
		Expect(byName["openbao"].Status).To(Equal(Pass))
		Expect(byName["openbao"].Check.Category).To(Equal(ServiceCategory))
		Expect(byName["some-daemon"].Status).To(Equal(Failed))
		Expect(byName["some-daemon"].Message).To(ContainSubstring("2/3"))
	})

	It("marks a succeeded Job as passing", func() {
		clients := newFakeClients([]runtime.Object{
			&batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "osac-db-init", Namespace: "osac"},
				Status:     batchv1.JobStatus{Succeeded: 1},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results[0].Check.Category).To(Equal(JobCategory))
		Expect(results[0].Status).To(Equal(Pass))
		Expect(results[0].Message).To(Equal("completed"))
	})

	It("marks a failed Job as Failed/Required", func() {
		clients := newFakeClients([]runtime.Object{
			&batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "osac-aap-bootstrap", Namespace: "osac"},
				Status:     batchv1.JobStatus{Failed: 1},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results[0].Check.Severity).To(Equal(Required))
		Expect(results[0].Status).To(Equal(Failed))
		Expect(results[0].Message).To(Equal("failed"))
	})

	It("marks a still-running Job as Failed/Warning, not Failed/Required -- it's expected, not broken", func() {
		clients := newFakeClients([]runtime.Object{
			&batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "osac-aap-bootstrap", Namespace: "osac"},
				Status:     batchv1.JobStatus{},
			},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results[0].Check.Severity).To(Equal(Warning))
		Expect(results[0].Status).To(Equal(Failed))
		Expect(results[0].Message).To(Equal("in progress"))
	})

	It("only lists workloads in the given namespace", func() {
		clients := newFakeClients([]runtime.Object{
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "in-ns", Namespace: "osac"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "other-ns", Namespace: "kube-system"}},
		}, nil)

		results, err := WorkloadChecks(context.Background(), clients, "osac")

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Check.Name).To(Equal("in-ns"))
	})

	It("returns an empty, non-nil-error result for a namespace with nothing in it", func() {
		clients := newFakeClients(nil, nil)

		results, err := WorkloadChecks(context.Background(), clients, "empty-namespace")

		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(BeEmpty())
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
