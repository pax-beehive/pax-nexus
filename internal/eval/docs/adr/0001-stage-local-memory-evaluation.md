# ADR-0001: Use stage-local control loops for memory evaluation

**Status:** Accepted

## Context

The memory path is ingest, extraction/admission, state, recall, and downstream
answering. End-to-end answer accuracy crosses every stage plus the consumer and
judge models. A failed answer therefore does not identify whether the source
was missing, the useful fact was not extracted, current state was wrong, or a
valid Note was not delivered.

GroupMemBench supplies questions and reference answers, but it does not supply
Team Note evidence, lifecycle, or passive-delivery labels. Token F1 is a lexical
diagnostic and an LLM judge is useful for semantic task acceptance, but neither
provides the internal attribution needed for daily memory optimization.

## Decision

Evaluation will use nested, stage-local control loops:

1. validate ingest independently with source Event and cursor invariants;
2. score extraction/admission/state against Stage Fixtures while source Events
   are fixed;
3. score recall against the same fixtures while the extracted Note set is
   fixed, and report both gold recall and Conditional Recall;
4. use end-to-end judged answer accuracy as an acceptance loop after the inner
   loops are healthy.

Stage Fixtures name required atoms, supporting Event IDs, forbidden or
superseded information, a source revision, and fixed recall identity/query/budget.
Observations preserve those controls plus item IDs, text, evidence links,
runtime provenance, duration, and error state. Stage artifacts must distinguish
upstream-missed atoms from atoms available after extraction but lost during
recall. Failed Observations are reported separately from quality denominators.

The first executable scorer contract is `pax-stage-eval-v1`. Matching is
explicit RE2 fixture logic so a stage score is deterministic and auditable.
Capture adapters emit the contract but remain separate from the scorer.
Semantic judges may later produce additional annotations, but they do not
replace raw Observations or deterministic safety checks.

## Consequences

- A Team Note change can be optimized against a smaller causal loop before a
  paid consumer/judge run.
- Gold recall and Conditional Recall must be reported together; either metric
  alone can hide the other stage's failure.
- GroupMemBench cases need PAX-native annotation before they can measure memory
  internals. The initial Finance subset has ten annotated cases and must be
  reviewed before being treated as a benchmark release.
- Observation adapters become stable seams. The scorer does not depend on the
  Team Note storage schema.
- Eval v2 captures successful PostgreSQL recall controls by query digest and
  exact envelopes, including zero-hit responses, plus the active extraction
  snapshot in the same transaction. The storage-specific adapter reads that
  fixed boundary after the trial matrix completes. Traces expire after seven days;
  judge-only export reuses prior stage artifacts instead of mutable live state.
- Missing trials and recalls remain explicit unscored Observation errors, while
  fixture/source control drift fails publication.
- End-to-end accuracy remains required for product claims because healthy inner
  stages do not prove downstream usefulness.

## Alternatives considered

- **Optimize only end-to-end LLM-judge accuracy.** Rejected because it mixes
  memory stages with consumer and judge variance and cannot localize failures.
- **Use Token F1 as the primary signal.** Rejected because paraphrases make it
  a poor semantic acceptance measure and it still cannot attribute stage loss.
- **Expose Team Note database tables directly to the scorer.** Rejected because
  it couples evaluation semantics to storage. Adapters should emit the stable
  Observation contract instead.
