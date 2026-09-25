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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkloadChecks lists every Deployment, StatefulSet, DaemonSet, and Job
// that currently exists in namespace and reports whether each has reached
// its expected ready state -- the actual OSAC services and one-shot
// configuration jobs the osac Helm chart installs.
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
	var results []Result

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

func deploymentResult(d appsv1.Deployment) Result {
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	ready := d.Status.ReadyReplicas
	status := Pass
	if ready < desired {
		status = Failed
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
		status = Failed
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
		status = Failed
	}
	return Result{
		Check:   Check{Name: ds.Name, Description: "DaemonSet", Severity: Required, Category: ServiceCategory},
		Status:  status,
		Message: fmt.Sprintf("%d/%d ready", ready, desired),
	}
}

// jobResult distinguishes a Job still running (Warning: expected during a
// fresh install -- the AAP bootstrap job alone can take 10-40 minutes) from
// one that has actually failed (Required), so a still-in-progress
// configuration step doesn't render as red/alarming the same way a genuine
// failure does.
func jobResult(j batchv1.Job) Result {
	switch {
	case j.Status.Succeeded > 0:
		return Result{
			Check:   Check{Name: j.Name, Description: "Job", Severity: Required, Category: JobCategory},
			Status:  Pass,
			Message: "completed",
		}
	case j.Status.Failed > 0:
		return Result{
			Check:   Check{Name: j.Name, Description: "Job", Severity: Required, Category: JobCategory},
			Status:  Failed,
			Message: "failed",
		}
	default:
		return Result{
			Check:   Check{Name: j.Name, Description: "Job", Severity: Warning, Category: JobCategory},
			Status:  Failed,
			Message: "in progress",
		}
	}
}
