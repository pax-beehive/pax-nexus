# Evaluation

The Evaluation context owns reproducible experiments that measure Nexus product quality.

## Language

**Case**:
A fixed input, question, expected answer, and identity scope used in an evaluation.

**Arm**:
One product strategy or control executed for the same Case.

**Run**:
A versioned collection of Cases, Arms, configuration, and artifacts.

**Trial**:
The durable final projection of a Case through an Arm. A Trial is the unit of
resume and paired comparison.

**Trial Attempt**:
One claimed execution of a Trial. Attempts are append-only and retain their
sequence, last entered Stage, failure class, timing, and artifact references.

**Validity Report**:
The durable Eval v3 decision that states whether one Run may support a
comparative score. It checks the complete Trial matrix, source coverage,
observable memory mutation, recall observations, Attempt artifacts, and
resolved configuration provenance. Invalid Runs retain artifacts but are not
benchmark-quality.

**Stage Fixture**:
A fixed contract for one memory stage. It names required atoms, supporting
Events, and forbidden or superseded information while adjacent stages are held
constant.

**Observation**:
An immutable capture of one stage output, including item identity, text,
evidence links, duration, and error state.

**Conditional Recall**:
Recall measured only over required atoms present in the paired extraction
Observation. Actor-neutral evaluation uses Available Atoms; consumer-scoped
evaluation uses Eligible Atoms.

**Available Atom**:
A required atom present in the fixed extraction Observation for a recall
evaluation Case, independent of consumer eligibility.

**Eligible Atom**:
An Available Atom supported by knowledge the Recall Consumer may access and
that is valid for the Case's Query Time. It is the denominator for
consumer-scoped recall and hint-opportunity metrics.

**Recall Consumer**:
The asking user, answering agent, and recipient session whose authenticated
identity governs one recall opportunity.

**Knowledge Origin**:
The user, agent, and source session that produced a recalled fact's evidence.
It is provenance and is distinct from the Recall Consumer.

**Observation Time**:
The fixed instant when a Case's passive recall and any focused intervention
observe memory state and current authorization.

**Query Time**:
The semantic instant or boundary requested by a current, as-of, or
changes-since query. It governs factual validity, not authorization.

**Source Time**:
The instant when the evidence supporting recalled knowledge occurred. It is
distinct from storage mutation time.

**Hint Opportunity**:
A safe lead whose focused recall retrieves a new Eligible Atom absent from the
passive evidence set within the configured call and token budgets.

**Recall Loss Ledger**:
The per-Available-Atom record of the first recall stage that lost the atom and
the observable rejection or budget reason.

## Relationships

- A **Run** contains many **Cases**.
- Each **Case** executes one or more **Arms**.
- A **Run** persists the full Case-by-Arm Trial matrix before execution.
- A **Trial** contains one or more ordered **Trial Attempts**; retries append an
  Attempt and never replace prior execution evidence.
- Eval v3 produces one **Validity Report** after all Trial Attempts finish and
  before comparative artifacts are accepted.
- A **Stage Fixture** may produce paired extraction and recall **Observations**.
- A **Case** pins one **Recall Consumer**, **Observation Time**, and **Query
  Time** interpretation.
- An **Eligible Atom** is an **Available Atom** filtered by consumer access and
  temporal validity.
- Evaluation may call Team Note or LLM Wiki through their public seams.
- A storage-specific capture adapter may read evaluation Observations after a
  Run; the stage scorer remains independent of product storage.

## V2 artifact boundary

Eval v2 owns orchestration, durable run state, scoring, and standard data
artifacts. It does not own a dashboard. Workstation renderers consume the CSV
and JSONL contract without importing product modules.

## Example dialogue

> **Dev:** "Can a benchmark helper be imported by Team Note?"
> **Domain expert:** "No. An **Arm** depends on the product under test, never the reverse."

## Flagged ambiguities

- "test" means a Go verification unless it is explicitly called an evaluation **Run**.
