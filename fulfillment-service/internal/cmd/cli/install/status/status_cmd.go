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
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/install/render"
	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/osac-installer/pkg/install"
)

const defaultInterval = 5 * time.Second

func Cmd() *cobra.Command {
	return newCmd(&runnerContext{loadClients: install.LoadClients})
}

// newCmd builds the command around the given runner: flags are bound to
// this exact runner instance and RunE calls its run method, so the two can
// never drift apart. Tests call this directly with a runner whose
// loadClients (and, for --watch, runProgram) is stubbed, instead of
// overwriting cmd.RunE on a command built by Cmd().
func newCmd(runner *runnerContext) *cobra.Command {
	result := &cobra.Command{
		Use:                   "status --namespace NAMESPACE [FLAG...]",
		Short:                 shortHelp,
		Long:                  longHelp,
		DisableFlagsInUseLine: true,
		Args:                  cobra.NoArgs,
		RunE:                  runner.run,
	}

	flags := result.Flags()
	flags.StringVar(
		&runner.args.kubeconfig,
		"kubeconfig",
		"",
		kubeconfigFlagHelp,
	)
	flags.StringVarP(
		&runner.args.namespace,
		"namespace",
		"n",
		"",
		namespaceFlagHelp,
	)
	flags.BoolVarP(
		&runner.args.watch,
		"watch",
		"w",
		false,
		watchFlagHelp,
	)
	flags.DurationVar(
		&runner.args.interval,
		"interval",
		defaultInterval,
		intervalFlagHelp,
	)

	return result
}

type runnerContext struct {
	// loadClients builds the clients checks run against. Defaults to
	// install.LoadClients; overridden in tests to avoid depending on a real
	// kubeconfig or cluster.
	loadClients func(kubeconfigPath string) (*install.Clients, error)
	// runProgram runs the interactive --watch program. Defaults to a real
	// tea.Program.Run; overridden in tests, since that needs a real
	// terminal to drive.
	runProgram func(tea.Model) error
	args       struct {
		kubeconfig string
		namespace  string
		watch      bool
		interval   time.Duration
	}
}

func (r *runnerContext) run(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	console := terminal.ConsoleFromContext(ctx)

	if r.args.namespace == "" {
		console.Errorf(ctx, "--namespace is required: the namespace OSAC is (or will be) installed into.\n")
		return exit.Error(1)
	}

	clients, err := r.loadClients(r.args.kubeconfig)
	if err != nil {
		console.Errorf(ctx, "Failed to connect to the Hub cluster: %v\n", err)
		return exit.Error(1)
	}

	if !r.args.watch {
		results, err := install.WorkloadChecks(ctx, clients, r.args.namespace)
		if err != nil {
			console.Errorf(ctx, "Failed to list OSAC's workloads: %v\n", err)
			return exit.Error(1)
		}
		console.Infof(ctx, "%s", renderStatus(results, render.Width(console.Stdout())))
		return nil
	}

	runProgram := r.runProgram
	if runProgram == nil {
		runProgram = func(m tea.Model) error {
			_, err := tea.NewProgram(m).Run()
			return err
		}
	}
	if err := runProgram(newWatchModel(ctx, clients, r.args.namespace, r.args.interval)); err != nil {
		console.Errorf(ctx, "Failed to display status: %v\n", err)
		return exit.Error(1)
	}
	return nil
}

const shortHelp = `Show a live dashboard of the installed OSAC deployment's health`

const longHelp = `
Lists every Deployment, StatefulSet, DaemonSet, and Job the osac Helm chart
installed into {{ bt }}--namespace{{ bt }} and shows a progress bar and
per-workload status, fitted to the terminal -- the actual OSAC services and
one-shot configuration jobs (like the AAP bootstrap job), not the
prerequisites a Hub cluster needs before installing. Use
{{ bt }}osac install discover{{ bt }}/{{ bt }}validate{{ bt }} for prerequisite
checks instead.

What's running is discovered from the cluster, not a fixed list: only the
services this particular install actually enabled (vmaas/caas/bmaas/maas,
the web console, metering, the bundled secret store, ...) show up.

With {{ bt }}--watch{{ bt }}/{{ bt }}-w{{ bt }}, this re-lists on an interval and the
view redraws in place instead of printing once and exiting -- press
{{ bt }}q{{ bt }} to quit. Without it, this runs once and exits, the same as a
single frame of the watch view.

Requires kubectl/oc-level access to the target Hub cluster: a working
kubeconfig with permission to read Deployments, StatefulSets, DaemonSets,
and Jobs in {{ bt }}--namespace{{ bt }}. This is different from
{{ bt }}osac login{{ bt }}, which authenticates against the fulfillment-service API,
not the Hub cluster's Kubernetes API.
`

const kubeconfigFlagHelp = `
_PATH_ - Path to the kubeconfig file to use. Defaults to the
{{ bt }}KUBECONFIG{{ bt }} environment variable, then {{ bt }}~/.kube/config{{ bt }},
using the current context — the same resolution {{ bt }}oc{{ bt }}/{{ bt }}kubectl{{ bt }} use.
`

const namespaceFlagHelp = `
_NAMESPACE_ - The namespace OSAC is (or will be) installed into, e.g. the
namespace you pass to {{ bt }}helm install osac ... -n <namespace>{{ bt }}. Required.
`

const watchFlagHelp = `
_[BOOLEAN]_ - Re-list workloads on an interval and redraw the view in place
instead of printing once and exiting. Press {{ bt }}q{{ bt }} to quit.
`

const intervalFlagHelp = `
_DURATION_ - How often to re-list workloads in {{ bt }}--watch{{ bt }} mode, e.g.
{{ bt }}10s{{ bt }}, {{ bt }}1m{{ bt }}. Ignored without {{ bt }}--watch{{ bt }}.
`
