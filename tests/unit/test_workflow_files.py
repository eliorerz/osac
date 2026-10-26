"""Every workflow file is one YAML document GitHub can read, and every suite that runs as a run of its own follows the
contract the decision relies on (see .github/CI.md): a file that GitHub cannot parse is registered under its path,
cannot be started, and fails on every push."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
WORKFLOWS = ROOT / ".github" / "workflows"
FILES = sorted([*WORKFLOWS.glob("*.yml"), *WORKFLOWS.glob("*.yaml")])
POLICY = yaml.safe_load((ROOT / ".github" / "e2e-policy.yml").read_text(encoding="utf-8"))["jobs"]
DISPATCHED = {job["suite"]: job["workflow"] for job in POLICY.values() if job.get("workflow")}


def load(path: Path) -> dict:
    return yaml.safe_load(path.read_text(encoding="utf-8"))


@pytest.mark.parametrize("path", FILES, ids=lambda p: p.name)
def test_a_workflow_file_is_one_document_with_triggers_and_jobs(path: Path) -> None:
    documents = list(yaml.safe_load_all(path.read_text(encoding="utf-8")))
    assert len(documents) == 1, f"{path.name} holds {len(documents)} YAML documents (a stray line before `---`?)"
    assert isinstance(documents[0], dict) and True in documents[0] and "jobs" in documents[0]  # `on` loads as True


@pytest.mark.parametrize(("suite", "file"), sorted(DISPATCHED.items()))
def test_a_suite_that_runs_as_a_run_of_its_own_follows_the_contract_of_the_decision(suite: str, file: str) -> None:
    wrapper = load(WORKFLOWS / file)
    assert wrapper["run-name"] == f"E2E suite · {suite} · #${{{{ inputs.pr-number }}}} · ${{{{ inputs.sha }}}}"
    inputs = wrapper[True]["workflow_dispatch"]["inputs"]
    assert {"repo", "sha", "pr-number", "fork-pr-author", "fork-pr-author-association"} <= set(inputs)
    assert wrapper["jobs"]["verify"]["uses"].startswith("osac-project/osac-ci/.github/workflows/suite-inputs.yml@")
    assert wrapper["jobs"][suite]["needs"] == "verify", "the suite's job is named after the suite and needs verify"
    for given in ("fork-pr-author", "fork-pr-author-association"):
        assert "needs.verify.outputs" in wrapper["jobs"][suite]["with"][given], (
            "read from the pull request, not the input"
        )
    listed = load(WORKFLOWS / "e2e-pr.yml")[True]["workflow_run"]["workflows"]
    assert wrapper["name"] in listed, "e2e-pr.yml must listen to the end of the suite's run"
