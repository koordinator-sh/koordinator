# AGENTS.md

Koordinator. QoS-based scheduling system for Kubernetes that improves the efficiency and reliability of latency-sensitive and batch workloads through co-location. Multi-component Go project under the koordinator-sh GitHub organization. Review bandwidth is a shared community resource, so scope work tightly and discuss substantive changes in issues before code lands.

`make help` lists targets. `make build` compiles all binaries. `make test` runs unit tests with race detection. All targets run on the host; Go 1.21+ is required.


## Agent operating rules

**Allowed.** Edit code, run make targets, read the codebase and GitHub state.

**Ask first.** Pushing commits to any branch (including feature branches), rewriting pushed history, edits under `.github/` or to `OWNERS`/`OWNERS_ALIASES`, dependency upgrades in `go.mod`/`go.sum`, CRD schema changes under `apis/` or `config/crd/`. When asking, propose the specific change and the reason in one message; do not start the work in the same turn.

**Never, without explicit per-turn authorization.** Public actions under the user's identity: GitHub comments, reviews, reactions, PR state changes, label or reviewer assignment, posts to Slack or any external surface. Draft such replies as quoted text for the user to send. Authorization is per-action and does not carry between actions or to sub-agents.


## Working in the codebase

- State your interpretation before coding. When the task has multiple valid reads, ask; don't pick one silently. For clear failure signals (logs, failing tests, reproducer), act; the ask rule is about unclear requirements, not unclear bugs.
- Define success as a checkable outcome: "add validation" becomes "write failing tests for invalid inputs, then make them pass". Where the issue is reproducible, the failing test IS the success criterion; write it first and let it gate the implementation.
- Before changing or extending a component, read an analogous one in the repository. The closest existing implementation is the canonical pattern; follow its structure, naming, and tests rather than introducing new conventions.
- The project has four core components: `koordlet` (node agent), `koord-manager` (control plane controllers), `koord-scheduler` (enhanced scheduler with plugins), `koord-runtime-proxy` (container runtime proxy). Entry points are under `cmd/`; implementation lives under `pkg/`.
- Scheduler plugins follow the Kubernetes scheduling framework pattern. Existing plugins under `pkg/scheduler/plugins/` are the canonical references for Filter, Score, Reserve, PreBind, and other extension points.
- API types live under `apis/` and are organized by group (`slo/v1alpha1`, `scheduling/v1alpha1`, `config/v1alpha1`, etc.). Changes to API types require running `make generate` and `make manifests` to regenerate DeepCopy methods and CRD YAMLs.
- Tests in the same package describe the contract. Read them before changing behavior. Unit tests use `testing` + `testify`/`gomock`; integration tests under `test/e2e/` use Ginkgo/Gomega.
- Verify behavior against the code, not from filenames or familiarity. Run the build or read the test when uncertain.
- Do not claim work is complete without running `make test` (or the targeted test) and confirming the relevant output. "Tests pass" is a claim, not a fact, until the command output exists.
- If execution goes sideways (unexpected state, cascading failures, a fix that breaks adjacent code), stop and replan. Restate what you know, identify where the plan broke, propose a revised path before continuing.


## Pull requests

- Minimalism: smallest correct change inside the smallest scope.
- Non-trivial work must be tracked in an issue. If there isn't one, ask the user to file or link it.
- The PR addresses that issue and nothing else: no renames, reformatting, refactors, new abstractions, or pattern changes beyond what the issue requires.
- Unrelated improvements belong in their own issue and PR, not folded into this PR. If you spot dead code or unrelated bugs in passing, mention them; don't fix them.
- Self-check on the way out: if the change grew larger than expected or the fix feels hacky, rewrite the clean version before opening the PR.


## Code style

- Standard Go. `make fmt` and `make lint` are authoritative.
- Every `.go` file must carry the Apache 2.0 license header from `hack/boilerplate/boilerplate.go.txt`. New files will fail `make licensecheck` without it.
- Comments are terse and only present when the WHY is non-obvious. Never paraphrase the code.
- Docs and comments describe the current state on its own terms. No "previously", "now", "recently", "renamed from", "added to fix", or other temporal or conversational framing. A reader with no context for the change must still understand the text.
- State each fact once, in its canonical location. Do not duplicate across struct docs, prose, tables, inline comments, and examples.
- Generated code (`zz_generated.deepcopy.go`, `*_generated.go`, CRD YAMLs under `config/crd/`) must not be hand-edited. Regenerate with `make generate` and `make manifests`.


## Git workflow

- DCO sign-off is required. Use `git commit -s`.
- Commit subject: imperative, ~72 characters. Body short and focused on the WHY; long narrative belongs in the PR description.
- Do not add machine-generated co-author trailers. Sign-off is the only required trailer.
- Do not bypass hooks (`--no-verify`) or signing checks.
