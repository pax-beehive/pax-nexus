# Eval v3 Two-Arm Extractor Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an Eval v3 run opt out of the Mem0 arm, so an extractor sweep can compare `no_memory_team` against `private_sqlite_plus_team_note` without paying Mem0's ingest cost.

**Architecture:** A single explicit config field selects the arm set. The default stays the three-arm protocol with its current contract, byte for byte. Two-arm runs skip Mem0 ingest, skip Mem0 validity checks, and label their validity report so no reader mistakes them for a three-arm result.

**Tech Stack:** Go (`internal/eval/v3`), POSIX `sh`, existing Eval v3 harness.

## Why

Mem0 ingest is infeasible at this domain size. Measured on the micro-canary
domain (1605 events, 15 batches):

- Mem0's fact extraction returned unparseable JSON on 70 of 102 `POST /memories`.
- Zero facts makes a batch NoOp, and `addMem0EventsWithEventFallback`
  (`internal/eval/v2/memoryprobe/client.go:194-214`) then retries every event in
  that batch individually.
- Observed throughput was ~3 LLM-backed posts per minute, i.e. roughly 9 hours
  for one round's Mem0 ingest and ~27 hours for a three-round sweep.

`mem0_chunks` is not an escape hatch: `ingestMem0Chunks` (`client.go:168-187`)
calls the same fallback, and at `mem0ChunkEventLimit = 8` a fully-NoOp domain
costs 201 chunk posts plus 1605 event posts — worse than the batch path.

The Team Note side is unaffected and already verified working: in the aborted
round, all 15 session streams reached `extraction_cursor >= last_sequence`, 24
`team_notes` rows were produced, and provenance recorded the swept extractor
correctly.

## Non-goals

- Fixing Mem0's JSON reliability or the per-event fallback. Both are real, both
  are out of scope here, and neither is needed to compare extractors.
- Changing the default three-arm contract, its validity requirements, or any
  existing config. A run that does not set the new field must behave exactly as
  it does today.
- Presenting two-arm output as comparable to three-arm output or to any
  published Mem0 number.

## Global Constraints

- The new mode is opt-in via one config field. Absent that field, every existing
  code path, validity check, and artifact field is unchanged.
- Two-arm runs must be self-labelling: `validity.json` records which arm set ran,
  so a reader can never mistake a two-arm run for a three-arm one.
- Arm set for two-arm mode, exactly: `no_memory_team`, `private_sqlite_plus_team_note`.
  `baseline_arm` stays `no_memory_team`.
- Go: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./... -count=1`.
- Scripts: POSIX `sh`, `set -eu`, standard repo header.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/eval/v3/protocol.go` (modify) | Accept the two-arm set when the config opts in; keep three arms the default. |
| `internal/eval/v3/validity.go` (modify) | Skip Mem0 receipt, mutation, and provider checks in two-arm mode; record the arm set in the report. |
| `scripts/eval-v3-opencode.sh` (modify) | Skip Mem0 ingest and its receipt validation when the mode is active. |
| `evals/v3/config.sweep-template.yaml` (modify) | Opt in and list two arms. |
| `evals/v3/README.md` (modify) | Document the mode and its reduced contract. |

---

### Task 1: Opt-in two-arm protocol validation

**Files:**
- Modify: `internal/eval/v3/protocol.go:32-80`
- Test: `internal/eval/v3/protocol_test.go`

**Interfaces:**
- Produces: a config field `arm_set` (string, YAML/JSON `arm_set`) on the v3
  config, valid values `three_arm` (default, and the value assumed when the
  field is absent or empty) and `two_arm_no_mem0`. Exported constants
  `ArmSetThreeArm = "three_arm"` and `ArmSetTwoArmNoMem0 = "two_arm_no_mem0"`,
  plus `ArmsFor(armSet string) []string` returning the required arm names.

- [ ] **Step 1: Write the failing tests**

In `internal/eval/v3/protocol_test.go`, add cases asserting:

1. A config with no `arm_set` and the three current arms validates — unchanged behaviour.
2. A config with no `arm_set` and only the two arms fails with the existing "exactly three architecture arms" error.
3. `arm_set: two_arm_no_mem0` with exactly `no_memory_team` and `private_sqlite_plus_team_note` validates.
4. `arm_set: two_arm_no_mem0` that still lists `groupmembench_mem0` fails, and the message names the offending arm.
5. `arm_set: two_arm_no_mem0` with `baseline_arm` other than `no_memory_team` fails.
6. `arm_set: three_arm` explicitly, with three arms, validates identically to case 1.
7. An unknown `arm_set` value fails with a message naming the value.
8. `ArmsFor("")` returns the three-arm list, so absent means default everywhere.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./internal/eval/v3/ -run TestProtocol -count=1`
Expected: FAIL — unknown field `arm_set`, undefined `ArmsFor`.

- [ ] **Step 3: Implement**

Add the `ArmSet` field to the v3 config struct, the two constants, and `ArmsFor`.
In `Validate`, replace the hardcoded `len(config.Arms) != len(architectureArms)`
check and the subsequent name comparison with the set returned by `ArmsFor`,
validating the `arm_set` value itself first. Keep every other rule — the
`baseline_arm`, `before_run`, `judge`, and per-arm `Producer`/`Ingest`/
`AfterProducer` prohibitions — untouched.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./internal/eval/v3/ -count=1`
Expected: PASS, including every pre-existing test.

- [ ] **Step 5: Commit**

```bash
git add internal/eval/v3/protocol.go internal/eval/v3/protocol_test.go
git commit -m "feat(eval-v3): add opt-in two-arm arm set

Mem0 ingest is infeasible at full-domain size, and an extractor sweep does
not need that arm. Absent arm_set, the three-arm contract is unchanged."
```

---

### Task 2: Validity in two-arm mode

**Files:**
- Modify: `internal/eval/v3/validity.go:180-270`
- Test: `internal/eval/v3/validity_test.go`

**Interfaces:**
- Consumes: `ArmSet`, `ArmsFor` from Task 1.
- Produces: `validity.json` gains an `arm_set` field carrying the run's arm set.
  In `two_arm_no_mem0`, the Mem0 receipt check, the Mem0 mutation check, and the
  Mem0 recall-provider check are skipped; every other check applies unchanged.

- [ ] **Step 1: Write the failing tests**

In `internal/eval/v3/validity_test.go`, add cases asserting:

1. Three-arm mode with a missing `mem0-ingest.json` is INVALID — unchanged.
2. Two-arm mode with no `mem0-ingest.json` at all is VALID, provided the Team
   Note and private-SQLite receipts are complete.
3. Two-arm mode still fails when `team-note-ingest.json` reports `memory_items: 0` —
   the guardrail that catches an extractor which produced nothing must survive.
4. Two-arm mode still fails when the private-SQLite receipt does not materialize
   every source event.
5. Two-arm mode fails if a Mem0 trial result is present anyway, since the arm
   should not have run.
6. `arm_set` is written into the report in both modes.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./internal/eval/v3/ -run TestValidity -count=1`
Expected: FAIL — two-arm runs reported invalid for missing Mem0 evidence.

- [ ] **Step 3: Implement**

Thread the arm set into the validity evaluation. Filter the receipt table
(`validity.go:213-238`) and the recall-provider expectations by the arm set, and
add `arm_set` to the report struct. Do not weaken any check that applies to the
arms actually being run.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOCACHE=${GOCACHE:-/tmp/team-memory-go-cache} go test ./... -count=1`
Expected: PASS across the repo.

- [ ] **Step 5: Commit**

```bash
git add internal/eval/v3/validity.go internal/eval/v3/validity_test.go
git commit -m "feat(eval-v3): scope validity checks to the configured arm set

Two-arm runs record arm_set in validity.json so a reader cannot mistake
them for a three-arm result. Team Note and private-SQLite checks are
untouched, including the memory_items guardrail."
```

---

### Task 3: Skip Mem0 ingest, wire the sweep, document the mode

**Files:**
- Modify: `scripts/eval-v3-opencode.sh:111-190`
- Modify: `evals/v3/config.sweep-template.yaml`
- Modify: `evals/v3/README.md`
- Test: `scripts/test-eval-v3-extractor-sweep.sh`

**Interfaces:**
- Consumes: `arm_set` from Tasks 1-2.
- Produces: `EVAL_V3_ARM_SET` (default `three_arm`) read by
  `scripts/eval-v3-opencode.sh`. When `two_arm_no_mem0`, `ingest_domain` skips
  the Mem0 ingest step and `validate_domain_receipts` does not require the Mem0
  receipt. Team Note and private-SQLite ingest, and the extraction readiness
  wait, are unchanged.

- [ ] **Step 1: Write the failing tests**

Append to `scripts/test-eval-v3-extractor-sweep.sh`, before the trailing
`if [ "$failures" -ne 0 ]` block, reusing the existing helpers:

1. The rendered sweep config declares `arm_set: two_arm_no_mem0` and exactly the
   two arms, with `groupmembench_mem0` absent.
2. `baseline_arm` is still `no_memory_team`.
3. `TEAM_MEMORY_EXTRACTOR_BASE_URL` is still in the rendered `runtime_env` — the
   provenance guarantee must survive this change.
4. With `EVAL_V3_ARM_SET=three_arm` (or unset), `scripts/eval-v3-opencode.sh`
   still references the Mem0 ingest path — assert against the script's own
   behaviour with a stubbed docker command, not by grepping for a literal.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `./scripts/test-eval-v3-extractor-sweep.sh`
Expected: FAIL — the template still lists three arms and has no `arm_set`.

- [ ] **Step 3: Implement**

Gate the Mem0 ingest block and its receipt requirement in
`scripts/eval-v3-opencode.sh` on `EVAL_V3_ARM_SET`. Set `arm_set: two_arm_no_mem0`
and the two arms in `evals/v3/config.sweep-template.yaml`. Document the mode in
`evals/v3/README.md`: what it skips, that its `validity.json` attests to a
reduced contract, and that its numbers are not comparable to three-arm runs or to
any published Mem0 figure.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `./scripts/test-eval-v3-extractor-sweep.sh` then `make test-scripts`
Expected: PASS.

Then confirm the real plan still resolves:
Run: `make eval-v3-extractor-sweep DRY_RUN=--dry-run PREFIX=twoarmcheck && rm -rf runs/eval-v3-sweep/twoarmcheck`
Expected: three planned rounds, all `key=present`, exit 0.

- [ ] **Step 5: Commit**

```bash
git add scripts/eval-v3-opencode.sh evals/v3/config.sweep-template.yaml evals/v3/README.md scripts/test-eval-v3-extractor-sweep.sh
git commit -m "feat(eval-v3): skip Mem0 ingest in two-arm mode and use it for sweeps

Extractor sweeps now run no_memory_team against private_sqlite_plus_team_note,
which is the comparison they exist to make, without Mem0's infeasible ingest."
```

---

## Self-Review

**Spec coverage:** protocol validation (Task 1), validity scoping and labelling
(Task 2), ingest skip plus sweep template and docs (Task 3). The "default
unchanged" constraint is asserted in Tasks 1 and 3; the self-labelling
constraint in Task 2.

**Placeholder scan:** none. Every step names concrete files, symbols, and test
cases.

**Type consistency:** `ArmSet`, `ArmSetThreeArm`, `ArmSetTwoArmNoMem0`, and
`ArmsFor` are spelled identically in Tasks 1 and 2. The config key `arm_set` and
the env var `EVAL_V3_ARM_SET` are spelled identically in Tasks 1-3.

**Known limitation, deliberate:** two-arm runs cannot speak to Mem0 at all. The
sweep answers "which extractor produces better Team Note memory", not "how does
PAX compare to Mem0". Answering the latter still requires the three-arm protocol
and a fix to Mem0 ingest throughput.
