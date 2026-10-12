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
  pull request and keeps the single `E2E` check up to date (plus `E2E / VMaaS`, `CaaS`, `BMaaS` while a suite runs). The lines are check runs of the OSAC CI app (a token minted from the organization secrets `OSAC_CI_APP_ID` and `OSAC_CI_APP_PRIVATE_KEY`, with the checks permission), so the page draws them with a spinner; `E2E` is required from that app only: approved on the exact head commit by a person with write
  access or CodeRabbit, a suite affected (`e2e-policy.yml`), and no cheap check already failed. It does not wait for `CI`: the E2E runs in parallel with it and both are required for the merge. It checks out no pull request code (only the policy and
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

## Dependency updates and who may change the pipeline

- `workflows/bot-approve.yml` approves Dependabot and Konflux pull requests as the OSAC CI app (`osac-ci[bot]`) when no
  person needs to read them (every commit the bot's own, plain and signed; only dependency files changed). The logic is in
  osac-project/osac-ci (`osac-ci bot-decide`), pinned to a full commit SHA. The workflow mints a token from the app's
  organization secrets (`OSAC_CI_APP_ID`, `OSAC_CI_APP_PRIVATE_KEY`) with the pull-request permission only; for a pull
  request opened by Dependabot the two secrets must also exist as organization Dependabot secrets.
- Changes to the files that define the checks (`.github/workflows`, `actions`, `scripts`, `filters`, `e2e-policy.yml`,
  `CODEOWNERS`, `.pre-commit-config.yaml`; Markdown in them excepted) need an approval from `@osac-project/wg-infra`
  (see `CODEOWNERS`). Only the osac-ui lint and typecheck workflows are also approvable by the osac-ui maintainers
  (`osac-ui/OWNERS`); every other workflow reports a required check, uses secrets or publishes an image, so it stays with
  wg-infra.

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
