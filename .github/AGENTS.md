# .github/ Agent Context

This area is part of the OSAC monorepo, not an isolated project. Its workflow
and automation changes may affect other components. Apply the repository-wide
rules in [`../AGENTS.md`](../AGENTS.md), consider downstream effects, and
follow the instructions for every affected component.

## CI, E2E and merge rules

Read [`.github/CI.md`](CI.md) before changing a workflow. In short: three required checks (`CI`, `E2E`, `jira-reference`)
plus a native approval on the head commit, and the merge queue.

- `workflows/ci.yml` runs the cheap checks as reusable workflows (`unit-tests.yml`, `integration-tests.yml`,
  `helm-lint.yaml`, ...) and ends in the job `CI`. The reusable workflows hold no path logic; `ci.yml`'s `plan` job reads
  `filters/ci-filters.yml`. A new check needs a filter, a job in `ci.yml` and an entry in the `needs` of `CI`.
- `workflows/e2e-pr.yml` decides, from the default branch with the repository token, when the full-install E2E starts for a
  pull request and keeps the single `E2E` check run up to date: approved on the exact head commit by a person with write
  access or CodeRabbit, `CI` passed, a suite affected (`e2e-policy.yml`). It checks out no pull request code (only the policy and
  filters of the default branch), and the suites it calls test the approved commit. `workflows/e2e-queue.yml` runs the
  suites for a merge queue entry. `workflows/e2e-review-relay.yml` only finishes, so a review wakes `e2e-pr.yml`.
- The `e2e-<suite>-full-install.yml` workflows hold one job each, no gating; they run on a schedule (every 5 hours), by hand
  and when called.
- `workflow_run` lists workflows by exact name (`CI`, `E2E review relay` in `e2e-pr.yml`): keep the list in step when one is
  renamed.
- Path filters: `tests/e2e/**` is the full-install suite and must still make the E2E apply (`e2e-suite` filter). Do not fold
  `tests/e2e` back into an ignore list.
- No Prow labels (`/lgtm`, `/approve`, `/hold`, `/e2e-ready`, `/ok-to-test`) are read. Prow still sets `jira/valid-reference`
  (the `jira-reference` check reads it) and runs the cherry-pick plugin.

## OSAC CI check

`workflows/osac-ci.yml` posts one `OSAC CI` check on every pull request: what the PR is waiting for, who has to act and
what happens next. It is informational and not a required check. The program, its policy (`policy/osac.yml`) and the
state descriptions live in [osac-project/osac-ci](https://github.com/osac-project/osac-ci); the workflow calls its
`publish` action pinned to a full commit SHA, so the code and policy only change by bumping that pin in a reviewed PR.

- `workflows/osac-ci-review.yml` does nothing but finish. It runs on a review, and `osac-ci.yml` lists it in its
  `workflow_run` trigger, so a review refreshes the verdict at once (a review on a fork pull request cannot write a
  check itself). Its name must match the entry in that list.
- `workflows/osac-ci-queue.yml` posts the same check on the commit the merge queue builds, so a required OSAC CI would
  be reported there too. It lists the same workflows as `osac-ci.yml` (minus the review relay): keep the two lists in
  step. It is informational until OSAC CI is required.
- `workflow_run` lists workflows by exact name. When a workflow that reports a required check is added, renamed or
  removed, update the list in `osac-ci.yml`. A missing name only delays an update until the next event or the 10-minute
  sweep.
- The workflow never checks out or runs pull request code and holds the read-only `osac-ci-reader` app credentials.
  Keep it that way: no `actions/checkout` of the PR, no PR text in a script.
- The sweep re-evaluates only the pull requests whose posted verdict is missing, errored, out of date, stuck or old
  (`stale-only`), so it is cheap. The built-in token allows 1,000 requests an hour; see the osac-ci README before
  changing it.
- `workflows/osac-ci-override.yml` answers `/override <full commit sha> <reason>` from a wg-infra member: it waives the
  protected-path approval for that exact commit and records who and why as a check run. The comment is the authority;
  never accept a check run as proof (any workflow can write one). Only the osac-ui lint and typecheck workflows are
  also approvable by the osac-ui maintainers (`osac-ui/OWNERS`); every other workflow reports a required check, uses
  secrets or publishes an image, so it stays with wg-infra.
- `workflows/osac-ci-refresh.yml` answers `/osac-ci refresh` from the pull request's author or an owner, member or
  collaborator: it recomputes the verdict from live state and posts it, for when a verdict is out of date. The comment
  only decides whether the job runs; keep it out of every script and keep the per-comment concurrency group.
- `workflows/bot-approve.yml` approves Dependabot and Konflux pull requests as the osac-ci bot when no person needs to read
  them (every commit the bot's own, plain and signed; only dependency files changed). The logic is in
  osac-project/osac-ci (`osac-ci bot-decide`), pinned to a full commit SHA; the credential is the secret
  `OSAC_CI_BOT_TOKEN` (Actions and Dependabot).
- Changes to the files that define the checks (`.github/workflows`, `actions`, `scripts`, `filters`, `CODEOWNERS`,
  `.pre-commit-config.yaml`; Markdown in them excepted) need an approval from `@osac-project/wg-infra`. `CODEOWNERS` lists them and the OSAC CI
  policy applies the same list; keep both in step.

## Release safety

Nightly builds use provisional `sha-*` image tags while all build, unit,
integration, security, and E2E gates run. Promote images to release-looking
nightly tags only after every required gate passes; failed runs must not publish
release-looking tags.

A real, permanent `<component>/vX.Y.Z` tag (release mode's `component_versions`
bump) must be pushed with real actor credentials, not the default
`GITHUB_TOKEN` — GitHub does not fire push-triggered workflows for a ref
created by `GITHUB_TOKEN`, so a component's own image/binary/proto publish
workflow would silently never run even though the tag exists.
Whatever creates such a tag must also verify each of that component's
downstream publish workflows actually started and succeeded before reporting
success; a tag existing is not evidence its publish happened.
