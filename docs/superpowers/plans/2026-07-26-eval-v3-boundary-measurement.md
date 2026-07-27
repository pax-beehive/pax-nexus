# Eval v3 Boundary Measurement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Eval v3 actually measure the thing it was designed for — whether one agent can answer from knowledge another agent produced, through shared team memory, and whether the private/shared boundary holds.

**Architecture:** Three independent workstreams. (A) generate the supporting-author annotations that activate the already-implemented strict cross-agent selection. (B) add arms that separate shared memory from private memory so their contributions can be attributed. (C) replace paxm's global top-N recall merge with a per-provider budget so shared notes are not crowded out by verbatim private events.

**Tech Stack:** Go (`team-memory`: `internal/eval/v3`, `cmd/groupmembench-*`; `paxm`: `internal/memory`), POSIX `sh`, existing Eval v3 harness.

## Why

Eval v3's thesis is team memory: shared knowledge and its boundaries. The
protocol encodes that — every author is an Actor Agent, one Answering Agent is
chosen per case, and `SelectAnswerer` (`internal/eval/v3/protocol.go:138-172`)
excludes the authors of the supporting evidence so a strict trial can only be
answered from *someone else's* knowledge.

That machinery has never run. Measured on the current manifests:

- `supporting_agent_ids` present on **0 of 5** and **0 of 30** cases.
- Therefore every trial reports `answerer_source_overlap = "unknown"` and
  `strict_cross_agent` is unset.
- Therefore `summary.csv` has never emitted its `trial_class=strict_cross_agent`
  slice.

Separately, the one arm that carries shared memory,
`private_sqlite_plus_team_note`, bundles it with a private SQLite database
holding all 1605 source events verbatim. paxm's router
(`internal/memory/router.go:147-172`) queries every provider, merges the hits
into one pool, sorts globally by score, and truncates to a single top-N. There
is no per-provider allocation, so the verbatim private events crowd out the
shared notes: candidate pools stayed at ~15 and injected context was identical
across extractors that produced 31, 2, and 24 notes.

Consequence: the component that carries the thesis has almost no room to
demonstrate value, and no arm isolates it.

## Non-goals

- Mem0. Explicitly out of scope for this work.
- Changing what `no_memory_team` or `private_sqlite_plus_team_note` mean. New
  arms are added; existing arms keep their current semantics so prior runs stay
  comparable.
- Improving extraction quality. This plan makes the measurement possible; it
  does not tune what is measured.

## Global Constraints

- Existing configs and arm sets must keep working unchanged. `arm_set` absent
  still means the three-arm protocol, exactly as today.
- Annotations must be reviewable and their provenance recorded. A derived
  annotation that cannot be audited is worse than none, because it silently
  redefines what a strict trial is.
- The paxm change ships as a new tagged version; `PAXM_EXPECTED_VERSION` in
  `.env.eval-v2` is bumped only after the eval stack is confirmed to build.
- Go: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./... -count=1`.
- Scripts: POSIX `sh`, `set -eu`, standard repo header.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `cmd/groupmembench-annotate/main.go` (create) | Derive supporting-author annotations for each case and write them alongside the manifest. |
| `internal/eval/groupmembench/annotate.go` (create) | Annotation logic: match gold answer to domain events, resolve authors, score confidence. |
| `cmd/groupmembench-select/main.go` (modify) | Emit `supporting_agent_ids` into manifest cases when an annotation file is supplied. |
| `internal/eval/v3/protocol.go` (modify) | Add the isolated arms and the arm sets that include them. |
| `internal/eval/v3/validity.go` (modify) | Recall-provider expectations for the new arms. |
| `scripts/eval-v3-opencode.sh` (modify) | Consumer wiring for the new arms. |
| `evals/v3/config.decomposed.example.yaml` (create) | A config exercising the decomposed arm set. |
| paxm `internal/memory/router.go` (modify) | Per-provider recall budget in place of a single global truncation. |

---

### Task A1: Derive supporting-author annotations

The dataset does not carry evidence authorship. GroupMemBench questions have
only `answer`, `asking_user_id`, `id`, `question`, so the annotation has to be
produced from the gold answer and the domain conversation.

**Files:**
- Create: `internal/eval/groupmembench/annotate.go`
- Create: `cmd/groupmembench-annotate/main.go`
- Test: `internal/eval/groupmembench/annotate_test.go`

**Interfaces:**
- Produces: `Annotate(ctx, cases []Case, events []DomainEvent, judge LLM) ([]Annotation, error)` where `Annotation` is `{CaseID string, SupportingAgentIDs []string, SupportingEventIDs []string, Confidence string, Method string}`. Written as JSON to `<output>/annotations.json`.
- `Confidence` is one of `high` / `low`, and `Method` records how the annotation was derived, so a reviewer can tell a model judgement from a lexical match.

- [ ] **Step 1: Write the failing tests**

In `internal/eval/groupmembench/annotate_test.go`, with a stub judge:

1. A case whose gold answer restates a fact from exactly one event yields that event's author and `confidence: high`.
2. A case whose answer draws on events from two different authors yields both, deduplicated and sorted.
3. The asking user is never emitted as a supporting author, even when they authored a matching event.
4. A case where the judge returns no supporting events yields an empty `SupportingAgentIDs` and `confidence: low` — never a fabricated author.
5. An author ID appearing in the events but not in the case's `participant_agent_ids` is dropped, with the drop recorded.
6. The judge is called once per case, and a judge error fails that case without aborting the batch.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./internal/eval/groupmembench/ -count=1`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement**

For each case, send the judge the gold answer and the domain events for that
case's scope, and ask which event IDs supply the facts the answer asserts.
Resolve those events to their author agent IDs, drop the asking user, drop
authors outside `participant_agent_ids`, sort and deduplicate.

Mark `confidence: high` only when the judge returned at least one event AND every
returned event ID exists in the input. Anything else is `confidence: low`.
Record `method` as the model and prompt version used.

Never invent an author. An empty result is a valid, honest annotation — it means
this case cannot be a strict trial.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench cmd/groupmembench-annotate
git commit -m "feat(eval): derive supporting-author annotations for GroupMemBench cases

The dataset carries no evidence authorship, so strict cross-agent trials
could never activate. This derives it from the gold answer and records
confidence and method so each annotation is reviewable."
```

---

### Task A2: Emit annotations into the manifest and confirm strict trials activate

**Files:**
- Modify: `cmd/groupmembench-select/main.go`
- Modify: `scripts/eval-v3-prepare-groupmembench.sh`
- Test: `cmd/groupmembench-select/main_test.go`

**Interfaces:**
- Consumes: `annotations.json` from Task A1.
- Produces: manifest cases carry `supporting_agent_ids`. Absent an annotation file, manifests are byte-identical to today.

- [ ] **Step 1: Write the failing tests**

1. With no annotation file, the generated manifest is byte-identical to the current output — no `supporting_agent_ids` key at all.
2. With an annotation file, each case carries its `supporting_agent_ids`.
3. Annotations with `confidence: low` are NOT emitted into the manifest; they are reported in a summary line so a human can review them. A low-confidence annotation silently becoming a strict trial is the failure this guards.
4. An annotation for a case ID not in the selection is ignored, and the mismatch is reported.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./cmd/groupmembench-select/ -count=1`
Expected: FAIL — no annotation input.

- [ ] **Step 3: Implement**

Add an optional `-annotations` flag. Merge high-confidence annotations into the
emitted cases. Print a summary: how many cases got annotations, how many were
withheld as low confidence, how many were unmatched.

In `scripts/eval-v3-prepare-groupmembench.sh`, run the annotator after selection
and pass the resulting file back into a second selection pass, so
`make eval-v3-prepare` produces an annotated manifest end to end.

- [ ] **Step 4: Verify strict trials actually activate**

This is the acceptance test for the whole workstream. Regenerate the manifest,
then confirm on a real run:

```bash
GROUPMEMBENCH_TOTAL_CASES=30 make eval-v3-prepare
python3 -c "
import json; d=json.load(open('runs/groupmembench-v3-selection/manifest.json'))
n=sum(1 for c in d['cases'] if c.get('supporting_agent_ids'))
print(f'annotated: {n}/{len(d[\"cases\"])}')"
```

Expected: a non-zero count. Then run a small v3 sweep and confirm
`answerer_source_overlap == "excluded"` and `strict_trial == true` appear in
`trials.jsonl`, and that `summary.csv` contains a `trial_class=strict_cross_agent`
row. Report the counts. If every case still reports `unknown`, the workstream has
not achieved its goal regardless of what the unit tests say.

- [ ] **Step 5: Commit**

```bash
git add cmd/groupmembench-select scripts/eval-v3-prepare-groupmembench.sh
git commit -m "feat(eval): emit supporting-author annotations into v3 manifests

Activates the strict cross-agent selection that protocol.go has always
implemented. Low-confidence annotations are withheld rather than silently
redefining what a strict trial is."
```

---

### Task B1: Isolated arms for shared and private memory

**Files:**
- Modify: `internal/eval/v3/protocol.go`
- Modify: `internal/eval/v3/validity.go`
- Modify: `scripts/eval-v3-opencode.sh`
- Create: `evals/v3/config.decomposed.example.yaml`
- Test: `internal/eval/v3/protocol_test.go`, `internal/eval/v3/validity_test.go`

**Interfaces:**
- Produces: arm constants `ArmTeamNoteOnly = "team_note_only"` and `ArmPrivateSQLiteOnly = "private_sqlite_only"`; arm set `ArmSetDecomposed = "decomposed"` whose members are `no_memory_team`, `team_note_only`, `private_sqlite_only`, `private_sqlite_plus_team_note`. `expectedRecallProvider` returns `team-memory` for the team-note-only arm and `team-memory-sqlite` for the private-only arm.

- [ ] **Step 1: Write the failing tests**

Protocol:
1. `ArmsFor(ArmSetDecomposed)` returns exactly the four arms above.
2. A `decomposed` config listing exactly those four validates; one missing an arm fails naming it.
3. `baseline_arm` must still be `no_memory_team` in the decomposed set.
4. Existing sets are unchanged: `ArmsFor("")`, `ArmsFor(ArmSetThreeArm)`, `ArmsFor(ArmSetTwoArmNoMem0)` return what they return today.

Validity:
5. The team-note-only arm requires the `team-memory` recall provider; a trial recording `team-memory-sqlite` for it fails.
6. The private-only arm requires `team-memory-sqlite`.
7. Both new arms are still subject to every non-Mem0 check that applies to memory arms today.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./internal/eval/v3/ -count=1`
Expected: FAIL — undefined arm constants.

- [ ] **Step 3: Implement**

Add the constants, the arm set, and the recall-provider expectations. In
`scripts/eval-v3-opencode.sh`, wire the two new consumer arms: the team-note-only
arm mounts no private SQLite database; the private-only arm runs with the Team
Note provider disabled. Add `evals/v3/config.decomposed.example.yaml` listing
the four arms.

The decomposed set exists so shared and private contributions can be attributed
separately. If the two isolated arms are not genuinely isolated — if the
team-note-only arm can still see private data — the arm set measures nothing.
Verify the isolation from the trial records, not from the config.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./... -count=1`
Expected: PASS.

Then verify isolation on a real micro run: in `trials.jsonl`, the
`team_note_only` arm must record `memory_recall_providers` containing only the
team entry, and `private_sqlite_only` only the private entry.

- [ ] **Step 5: Commit**

```bash
git add internal/eval/v3 scripts/eval-v3-opencode.sh evals/v3/config.decomposed.example.yaml
git commit -m "feat(eval-v3): add isolated shared and private memory arms

private_sqlite_plus_team_note bundles both sources, so neither can be
attributed. The decomposed arm set separates them while leaving the
existing arms and arm sets untouched."
```

---

### Task C1: Per-provider recall budget in paxm

Work in the paxm repository at `/Users/toddzheng/Workspace/golang/paxm`.

**Files:**
- Modify: `internal/memory/router.go`
- Test: `internal/memory/router_test.go`

**Interfaces:**
- Produces: `SearchPolicy` gains a per-provider allocation. Absent that field, `SearchWithPolicy` behaves exactly as today — one global sort and truncation.

- [ ] **Step 1: Write the failing tests**

1. With no allocation configured, results are the current global top-N, in the current order. This is the regression guard for every existing caller.
2. With an allocation of N per provider and two providers each returning more than N hits, the result contains N from each, and provider identity is preserved.
3. When one provider returns fewer than its allocation, the shortfall is redistributed to the others rather than wasted — a quiet provider must not shrink the context.
4. Ordering within the final result is still by score, so the caller sees a ranked list.
5. A provider returning zero hits does not fail the search.
6. The total never exceeds the caller's overall limit.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./internal/memory/ -count=1`
Expected: FAIL — no allocation field.

- [ ] **Step 3: Implement**

In `SearchWithPolicy`, when an allocation is configured, group the collected hits
by provider, take up to the allocation from each in score order, redistribute
unused allocation, then sort the survivors by score and apply the overall limit.
Leave the existing path untouched when no allocation is set.

- [ ] **Step 4: Run the tests to verify they pass**

Run the paxm suite. Then tag a release and, in team-memory, bump
`PAXM_EXPECTED_VERSION` in `.env.eval-v2` and confirm `make eval-v3-up` builds
the opencode image against it before running anything expensive.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(memory): allocate recall budget per provider

A single global top-N lets a provider holding verbatim source events
crowd out a provider holding extracted notes. Absent an allocation the
behaviour is unchanged."
```

---

### Task D1: Measure the boundary

With A, B, and C in place, run the decomposed arm set over the annotated 30-case
manifest and report what the thesis actually asked.

- [ ] **Step 1: Confirm the inputs are live**

Verify the manifest carries `supporting_agent_ids`, the config names the
decomposed arm set, and the pinned paxm version is the one with per-provider
allocation. Any of the three missing makes the run meaningless.

- [ ] **Step 2: Run**

Run the sweep with a single extractor — this measures the protocol, not the
models, so one extractor is enough and three would triple the cost for nothing.

- [ ] **Step 3: Report**

From `trials.jsonl` and `summary.csv`, report:

- how many trials were strict cross-agent, and accuracy on that slice versus the rest;
- accuracy for `team_note_only` versus `private_sqlite_only` versus the combined arm, which is the shared-versus-private attribution;
- for the combined arm, how many injected context items came from each provider — the direct test of whether the per-provider budget fixed the starvation.

State plainly whether strict cross-agent trials now exist. If they do not, the
work has not achieved its goal, whatever the unit tests report.

---

## Self-Review

**Spec coverage:** annotation derivation (A1), manifest emission and activation
(A2), isolated arms (B1), per-provider budget (C1), measurement (D1).

**Placeholder scan:** none. Every step names files, symbols, and concrete
verification commands.

**Type consistency:** `SupportingAgentIDs`, `supporting_agent_ids`,
`ArmTeamNoteOnly`, `ArmPrivateSQLiteOnly`, `ArmSetDecomposed` are spelled
identically across tasks.

**Ordering:** A and B are independent and may run in parallel. C is independent
of both but must land, be released, and be re-pinned before D. D depends on all
three.

**Deliberate limitation:** annotations are model-derived, not human-authored.
Task A1 records confidence and method, and A2 withholds low-confidence
annotations, so a reviewer can audit them — but the first annotated run should be
read as provisional until someone has checked a sample by hand.
