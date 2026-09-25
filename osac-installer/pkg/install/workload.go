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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkloadChecks first checks whether namespace exists, then -- only if it
// does -- lists every Deployment, StatefulSet, DaemonSet, and Job in it and
// reports whether each has reached its expected ready state: the actual
// OSAC services and one-shot configuration jobs the osac Helm chart
// installs, plus the namespace they live in, all part of the same watched
// lifecycle from "not created yet" through "fully ready".
//
// Unlike DefaultChecks (a fixed matrix checked against live cluster
// state), there's no static list of OSAC's installed workloads to declare
// ahead of time: what actually exists depends on which services this
// specific install enabled (global.services.*, ui.enabled,
// metering.enabled, bundledVault.enabled, ...), so the check set itself is
// discovered from the cluster rather than read from
// data/prerequisites.yaml. For the same reason this returns []Result
// directly rather than []Check + a separate RunAll pass: enumerating what
// to check and finding out its status happen in the same List call.
func WorkloadChecks(ctx context.Context, clients *Clients, namespace string) ([]Result, error) {
	nsResult, exists, err := namespaceResult(ctx, clients, namespace)
	if err != nil {
		return nil, err
	}
	results := []Result{nsResult}
	if !exists {
		// Nothing to list yet -- `helm install` (or `--create-namespace`)
		// hasn't created the namespace. Not an error: this is the expected
		// state at the very start of an install, and --watch should keep
		// polling through it smoothly rather than failing here.
		return results, nil
	}

	deployments, err := clients.Typed.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list Deployments in namespace %q: %w", namespace, err)
	}
	for _, d := range deployments.Items {
		results = append(results, deploymentResult(d))
	}

	statefulSets, err := clients.Typed.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list StatefulSets in namespace %q: %w", namespace, err)
	}
	for _, s := range statefulSets.Items {
		results = append(results, statefulSetResult(s))
	}

	daemonSets, err := clients.Typed.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list DaemonSets in namespace %q: %w", namespace, err)
	}
	for _, ds := range daemonSets.Items {
		results = append(results, daemonSetResult(ds))
	}

	jobs, err := clients.Typed.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list Jobs in namespace %q: %w", namespace, err)
	}
	for _, j := range jobs.Items {
		results = append(results, jobResult(j))
	}

	return results, nil
}

// namespaceResult reports whether namespace exists. A Warning (not
// Required) severity when it doesn't: absent is the expected state before
// `helm install` runs, not a failure to alarm over -- the same reasoning
// jobResult applies to a still-running Job. The bool return distinguishes
// "checked, and it doesn't exist" from an actual error (RBAC denied,
// connection failure) reaching the caller.
func namespaceResult(ctx context.Context, clients *Clients, namespace string) (Result, bool, error) {
	_, err := clients.Typed.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return Result{
			Check:   Check{Name: namespace, Description: "Namespace", Severity: Warning, Category: NamespaceCategory},
			Status:  Progressing,
			Message: "not created yet",
		}, false, nil
	case err != nil:
		return Result{}, false, fmt.Errorf("failed to check namespace %q: %w", namespace, err)
	default:
		return Result{
			Check:   Check{Name: namespace, Description: "Namespace", Severity: Required, Category: NamespaceCategory},
			Status:  Pass,
			Message: "exists",
		}, true, nil
	}
}

func deploymentResult(d appsv1.Deployment) Result {
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	ready := d.Status.ReadyReplicas
	status := Pass
	if ready < desired {
		status = Progressing
	}
	return Result{
		Check:   Check{Name: d.Name, Description: "Deployment", Severity: Required, Category: ServiceCategory},
		Status:  status,
		Message: fmt.Sprintf("%d/%d replicas ready", ready, desired),
	}
}

func statefulSetResult(s appsv1.StatefulSet) Result {
	desired := int32(1)
	if s.Spec.Replicas != nil {
		desired = *s.Spec.Replicas
	}
	ready := s.Status.ReadyReplicas
	status := Pass
	if ready < desired {
		status = Progressing
	}
	return Result{
		Check:   Check{Name: s.Name, Description: "StatefulSet", Severity: Required, Category: ServiceCategory},
		Status:  status,
		Message: fmt.Sprintf("%d/%d replicas ready", ready, desired),
	}
}

func daemonSetResult(ds appsv1.DaemonSet) Result {
	desired := ds.Status.DesiredNumberScheduled
	ready := ds.Status.NumberReady
	status := Pass
	if ready < desired {
		status = Progressing
	}
	return Result{
		Check:   Check{Name: ds.Name, Description: "DaemonSet", Severity: Required, Category: ServiceCategory},
		Status:  status,
		Message: fmt.Sprintf("%d/%d ready", ready, desired),
	}
}

// jobResult distinguishes a Job still running (expected during a fresh
// install -- the AAP bootstrap job alone can take 10-40 minutes, and
// normally retries a few times while it waits on something else to become
// ready) from one that has actually given up, so a still-in-progress
// configuration step renders as "installing" rather than the same
// red/alarming failure state as a Job that's truly done retrying.
//
// status.failed alone can't tell these apart: Kubernetes increments it on
// every retried pod attempt, even ones well within backoffLimit that the
// Job will go on to recover from. The Job's own "Failed" condition is only
// True once Kubernetes itself has given up (backoffLimit or
// activeDeadlineSeconds exceeded) -- that's the Job's own built-in timeout,
// so nothing extra is needed here to distinguish "still retrying" from
// "actually failed".
func jobResult(j batchv1.Job) Result {
	switch {
	case j.Status.Succeeded > 0:
		return Result{
			Check:   Check{Name: j.Name, Description: "Job", Severity: Required, Category: JobCategory},
			Status:  Pass,
			Message: "completed",
		}
	case jobConditionTrue(j, batchv1.JobFailed):
		return Result{
			Check:   Check{Name: j.Name, Description: "Job", Severity: Required, Category: JobCategory},
			Status:  Failed,
			Message: "failed",
		}
	case j.Status.Failed > 0:
		return Result{
			Check:   Check{Name: j.Name, Description: "Job", Severity: Warning, Category: JobCategory},
			Status:  Progressing,
			Message: fmt.Sprintf("in progress (retrying, %d failed attempt(s) so far)", j.Status.Failed),
		}
	default:
		return Result{
			Check:   Check{Name: j.Name, Description: "Job", Severity: Warning, Category: JobCategory},
			Status:  Progressing,
			Message: "in progress",
		}
	}
}

func jobConditionTrue(j batchv1.Job, condType batchv1.JobConditionType) bool {
	for _, c := range j.Status.Conditions {
		if c.Type == condType && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
