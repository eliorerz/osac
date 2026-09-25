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
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/install/secretref"
	"github.com/osac-project/osac/fulfillment-service/internal/exit"
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	"github.com/osac-project/osac/osac-installer/pkg/install"
)

const defaultInterval = 5 * time.Second

// defaultServices is every OSAC service: the default for --services, so a
// bare `osac install status` shows prerequisite state for everything OSAC
// might need, matching `discover`/`validate`'s own default.
var defaultServices = []string{"vmaas", "caas", "bmaas", "maas", "metering"}

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
	flags.StringSliceVar(
		&runner.args.services,
		"services",
		defaultServices,
		servicesFlagHelp,
	)
	flags.BoolVar(
		&runner.args.metal3,
		"metal3",
		false,
		metal3FlagHelp,
	)
	flags.StringArrayVar(
		&runner.args.requireSecrets,
		"require-secret",
		nil,
		requireSecretFlagHelp,
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
		kubeconfig     string
		namespace      string
		watch          bool
		interval       time.Duration
		services       []string
		metal3         bool
		requireSecrets []string
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

	secretRefs, err := secretref.ParseAll(r.args.requireSecrets)
	if err != nil {
		console.Errorf(ctx, "Invalid --require-secret value: %v\n", err)
		return exit.Error(1)
	}
	checks, err := install.DefaultChecks(install.CheckOptions{Services: r.args.services, Metal3: r.args.metal3})
	if err != nil {
		console.Errorf(ctx, "Failed to build the prerequisite checks: %v\n", err)
		return exit.Error(1)
	}
	checks = append(checks, install.SecretChecks(secretRefs)...)

	if !r.args.watch {
		results, err := gatherResults(ctx, clients, r.args.namespace, checks)
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
	if err := runProgram(newWatchModel(ctx, clients, r.args.namespace, checks, r.args.interval)); err != nil {
		console.Errorf(ctx, "Failed to display status: %v\n", err)
		return exit.Error(1)
	}
	return nil
}

const shortHelp = `Show a live dashboard of the installed OSAC deployment's health`

const longHelp = `
Shows a single dashboard for the whole OSAC install: the Hub cluster
prerequisites (Operators, CRDs, StorageClass, OCP version -- the same checks
{{ bt }}osac install discover{{ bt }}/{{ bt }}validate{{ bt }} run) alongside the
namespace and every Deployment, StatefulSet, DaemonSet, and Job the osac Helm
chart installed into {{ bt }}--namespace{{ bt }} -- the actual OSAC services and
one-shot configuration jobs (like the AAP bootstrap job). The progress bar
only reflects the namespace/services/jobs, not the prerequisites: those
describe whether the cluster was ready to install, not how the install
itself is progressing, so a failed prerequisite is shown but never holds the
bar back from 100%.

What's running is discovered from the cluster, not a fixed list: only the
services this particular install actually enabled (vmaas/caas/bmaas/maas,
the web console, metering, the bundled secret store, ...) show up. The
prerequisite set, on the other hand, is the same fixed matrix
{{ bt }}discover{{ bt }}/{{ bt }}validate{{ bt }} use -- narrow it with
{{ bt }}--services{{ bt }}/{{ bt }}--metal3{{ bt }}/{{ bt }}--require-secret{{ bt }} the
same way you would there.

With {{ bt }}--watch{{ bt }}/{{ bt }}-w{{ bt }}, this re-checks on an interval and the
view redraws in place instead of printing once and exiting -- press
{{ bt }}q{{ bt }} to quit. Without it, this runs once and exits, the same as a
single frame of the watch view.

Requires kubectl/oc-level access to the target Hub cluster: a working
kubeconfig with permission to read Deployments, StatefulSets, DaemonSets,
Jobs, CustomResourceDefinitions, ClusterServiceVersions, ClusterVersion,
StorageClasses, and Secrets. This is different from {{ bt }}osac login{{ bt }},
which authenticates against the fulfillment-service API, not the Hub
cluster's Kubernetes API.
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
_DURATION_ - How often to re-check in {{ bt }}--watch{{ bt }} mode, e.g.
{{ bt }}10s{{ bt }}, {{ bt }}1m{{ bt }}. Ignored without {{ bt }}--watch{{ bt }}.
`

const servicesFlagHelp = `
_[SERVICE...]{{ bt }},{{ bt }}...{{ bt }}]_ - Which OSAC services to show
prerequisites for: {{ bt }}vmaas{{ bt }}, {{ bt }}caas{{ bt }}, {{ bt }}bmaas{{ bt }},
{{ bt }}maas{{ bt }}, {{ bt }}metering{{ bt }}. Defaults to every service; narrow it to
match {{ bt }}global.services.*{{ bt }} in your {{ bt }}my-values.yaml{{ bt }} to avoid
showing Operators an install that only enables some services doesn't need.
Never affects which workloads are shown -- those are always discovered from
the cluster, not this flag.
`

const metal3FlagHelp = `
_[BOOLEAN]_ - Also show Metal3 bare-metal prerequisites: the BareMetalHost
CRD and the Provisioning CR's {{ bt }}watchAllNamespaces{{ bt }} setting. Enable
this if you're installing OSAC with the Metal3 backend.
`

const requireSecretFlagHelp = `
_NAMESPACE/NAME[:KEY,...]_ - Also show whether the named Secret exists (and,
if given, that it has every listed key). Repeatable. Use this for Secrets
your own {{ bt }}my-values.yaml{{ bt }} references, such as the AAP license
manifest or a database connection Secret -- their names aren't fixed by
OSAC, so they aren't included by default.
`
