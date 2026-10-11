# How CI works

Three checks must pass before a pull request merges, and one approval must be on the commit that merges.

| Check | What it is | Where |
|---|---|---|
| `CI` | Everything cheap: linters, unit and integration tests, builds, chart checks, generated-code checks. Only the parts your change touches run. | `workflows/ci.yml` |
| `E2E` | The full-install end-to-end suites on a real cluster, about 100 minutes. | `workflows/e2e-pr.yml`, `workflows/e2e-queue.yml` |
| `jira-reference` | The title names a Jira issue (`OSAC-1234:`) or says `NO-ISSUE:`. | `workflows/jira-reference.yml` |

The approval is a normal GitHub review from someone with write access (and from a code owner when you touch `.github/`,
`proto/` or the installer, see `CODEOWNERS`). A new commit drops it; a clean rebase or "Update branch" keeps it. When the
checks are green and the approval is there, the author adds the pull request to the merge queue. The queue runs `CI` and
`E2E` again on the result of merging, so what lands is what was tested.

## What you see on a pull request

`CI` is one check. Open it to see which parts ran: a part your change does not touch shows as skipped inside that run,
not as a separate check.

`E2E` is one check too, and it always says why it is where it is:

| The check says | What to do |
|---|---|
| Waiting for an approval | Ask for a review. CodeRabbit's approval or a person with write access starts the E2E; the author cannot approve their own pull request. |
| Waiting for CI | Nothing; it starts when `CI` passes. |
| CI failed | Fix CI; the E2E does not run on a commit that failed the cheap checks. |
| Changes requested | The reviewer's request has to be resolved first. |
| Draft | Mark the pull request ready. |
| Running | Open the check for the run. |
| Not affected by this change | Docs, tests or other files no suite exercises. Nothing runs. |

The E2E runs once for a commit. To run it again for the same commit, re-run the finished run from the Actions page
(`E2E for pull requests`); a new commit runs it again by itself. A fix pushed after the approval needs a new review,
because a push drops the approval, and then runs the E2E once.

## Dependency updates

Dependabot and Konflux pull requests are approved by the `osac-ci` bot account when every commit is the bot's own (plain,
signed) and only dependency files changed (`workflows/bot-approve.yml`). Anything else gets a person.

## Changing the pipeline

- **Which parts run for which paths:** `filters/ci-filters.yml` is the one place. `ci.yml` has a `plan` job that reads it.
- **Add a check:** write it as a reusable workflow (`on: workflow_call`, no path logic inside), add a filter for it to
  `ci-filters.yml`, add a job to `ci.yml` that runs when the filter matched, and add that job to the `needs` of `CI`.
- **Which suites a change needs:** `e2e-policy.yml` (suite, and the filters that decide it). `e2e-pr.yml` and
  `e2e-queue.yml` read it; `e2e-pr.yml` reads it from the default branch, never from the pull request.
- **The decision and the `E2E` check** (approval, CI result, the wording above) live in
  [osac-project/osac-ci](https://github.com/osac-project/osac-ci). The workflows pin its actions to a full commit; bump
  the pin in a reviewed pull request.
- **Emergency:** setting the repository variable `CI_GATING_ENABLED` to `false` makes `CI` run every part.
- `workflow_run` lists workflows by exact name. `e2e-pr.yml` lists `CI` and `E2E review relay`; renaming either needs the
  list changed.
- Changes to `.github/workflows`, `.github/actions`, `.github/scripts`, `.github/filters` and `CODEOWNERS` need a code
  owner's approval.
