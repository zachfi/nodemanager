---
name: issue-to-release
description: >-
  Repeatable loop for turning open Forgejo issues on znet/nodemanager (code.znet)
  into merged, optionally released code. Use this whenever the user wants to
  "work an issue", "triage issues", "pick something to work on", "check for new
  issues", asks what to do next on nodemanager, or wants to take a filed issue
  through design → code → test → PR → merge → release. Surveys all open issues,
  evaluates them against project value, calls out conflicts, and drives the
  selected one through implementation with human approval gates at design and
  before merge. Trigger it even when the user only says "anything new on the
  tracker?" or names a specific issue number.
---

# issue-to-release

Turn an open `znet/nodemanager` issue into merged (and, when it's major
functionality bound for prod, released) code. The point of this skill is
consistency and judgment: every issue gets evaluated against whether the work is
actually worth doing before any code is written, and the risky, hard-to-reverse
steps (merge, release) only happen behind an explicit human gate.

This repo has no committed CI yet (restoring it is issue #19), so the merge gate
today is local `make test` + `make build`. Treat Woodpecker as out of scope until
that issue lands.

## Operating principles

- **Two gates, and only two.** Design approval and pre-merge. Between and around
  them, work autonomously — don't stop to ask permission for routine steps. The
  gates exist because design rework is cheap before code and expensive after, and
  because merge/release are hard to reverse.
- **Never commit to `main`.** Branch first, always.
- **Lean on existing preferences.** Memory and CLAUDE.md already capture the
  user's conventions (lower-case log messages except acronyms, minimize disk
  writes, no internal IPs in the public repo, no system tools in make targets,
  git HTTPS fallback for code.znet). Honor them; don't restate them back.
- **Evaluate honestly.** Part of the value here is saying "this issue isn't worth
  doing right now, and here's why." A loop that rubber-stamps every issue is
  worse than no loop.

## Preflight

Before anything, confirm the environment and report state in one or two lines:

```sh
fj -H code.znet whoami                 # auth works
git -C . status --short --branch       # clean? on what branch?
```

If the tree is dirty or you're mid-work on another branch, surface that and ask
how to proceed rather than silently stacking changes.

## 1 — Survey

Pull the full open-issue picture plus anything already in flight, so the
evaluation in step 2 can see conflicts:

```sh
fj -H code.znet issue search -r znet/nodemanager -s open
fj -H code.znet pr search    -r znet/nodemanager -s open
git -C . branch -a
ls .claude/worktrees 2>/dev/null
```

Read each open issue's body (`fj -H code.znet issue view -r znet/nodemanager <n>`).
You need the actual content, not just the titles, to judge value and conflicts.

## 2 — Evaluate & select

Score **every** open issue on the project's four axes and present a compact
ranked table:

| Axis | What it rewards |
|---|---|
| Project value | Moves the product/users forward; unblocks a real need |
| Maintenance burden | Reduces toil, flakiness, or recurring manual work |
| Reliability | Makes the system more stable/predictable in prod |
| Automation | Removes a human from a loop that doesn't need one |

Then **explicitly call out conflicts** — this is a first-class output, not a
footnote:

- Two issues that overlap or would touch the same code.
- A dependency order (issue B is much easier after issue A ships).
- An issue that contradicts current direction or a recent decision.
- An issue already being worked (open PR, existing branch/worktree).

Recommend one pick with a sentence of reasoning, and name any issues you'd
*decline* and why. The user chooses; default to your recommendation if they just
say "go."

## 3 — Accept & design 🚦 GATE 1

For the selected issue, explore the relevant code (`pkg/`, `internal/controller/`,
`api/`, `cmd/`) and produce a focused solution design: the approach, files to
create/modify, the test strategy, and **observability impact** per the rubric in
`references/observability.md` — does this introduce a failure mode that warrants a
metric, alert, runbook, or dashboard panel?

For non-trivial features, use the `superpowers:brainstorming` skill to pin down
requirements first, and `superpowers:writing-plans` to produce the plan. For small
well-scoped fixes, a few sentences of design is enough — scale ceremony to risk.

**Stop and get the user's approval on the design before writing implementation
code.**

## 4 — Implement

- Branch off `main`: `git switch -c <type>/<short-slug>` (e.g. `fix/upgrade-timeout`).
- Use `superpowers:test-driven-development` where it fits — most controller logic,
  handlers, and pkg/ code is unit-testable with envtest/Ginkgo.
- Keep the change focused on the issue. Resist unrelated refactoring.
- If the design called for observability changes, make them now (see
  `references/observability.md`) — a feature isn't done if its new failure modes
  are invisible in prod.

## 5 — Verify

This is the CI gate today. Run and report real output, not a claim:

```sh
make test
make build
make lint        # best-effort; note if golangci-lint can't run on this toolchain
```

If tests fail, fix and re-run — don't proceed to PR on red. Use
`superpowers:verification-before-completion` discipline: evidence before
assertions.

## 6 — PR

Push (HTTPS fallback if ssh:22 to code.znet is refused — see memory) and open the
PR, referencing the issue so it auto-links/closes:

```sh
git push -u origin <branch>
# title is positional; body is --body/--body-file (not --description); pipe via stdin
fj -H code.znet pr create -r znet/nodemanager --base main --head <branch> \
  --body-file /dev/stdin "<type>(<scope>): <summary>" <<'EOF'
Closes #<n>.

<what + why>
EOF
```

Consider `superpowers:requesting-code-review` for substantial changes before the
merge gate.

## 7 — Pre-merge 🚦 GATE 2

Present: the diff summary, the test/build evidence from step 5, and anything the
reviewer flagged. **Get explicit approval, then merge** via `fj` or the web UI per
the user's preference.

## 8 — Release (conditional)

Only for **major functionality the user wants in prod**, and only after an
explicit go-ahead — most merges do not release. When releasing, drive the
documented downstream automation:

```sh
tools/release-downstream.sh        # see memory: release_automation.md for the flow
```

Releases fan out to four downstream repos and publish artifacts; treat the
go/no-go as its own decision, not an automatic tail of the merge.

## Reference

- `references/observability.md` — when a change needs a metric/alert/runbook/
  dashboard panel, and the notification-discipline rubric shared with `/fleet-sre`.
