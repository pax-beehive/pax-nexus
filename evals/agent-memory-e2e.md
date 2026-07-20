# Authoring an agent-memory end-to-end evaluation

This guide is for contributors who want to evaluate a memory change through a
real answering Agent. It defines the minimum contract for a result that can be
compared with Team Note rather than only demonstrating that a retrieval API
returned text.

An end-to-end trial has six separate stages:

```text
source Session Events
  -> memory ingest and readiness
  -> real Agent recall or context injection
  -> answering Agent output
  -> independent answer judge
  -> durable artifacts and paired comparison
```

Do not use an answer failure to diagnose recall until the required fact is
shown to be present in the source and memory-ingest observations.

## Choose the right harness

| Need | Harness | Use when |
| --- | --- | --- |
| Fast integration smoke test | [`opencode/`](opencode/README.md) | Verifying the production paxm/OpenCode/provider path with a small fixture. |
| Current memory quality, latency, and cost comparison | Eval v2 | Comparing a control with Team Note, a recall-policy variant, or an external provider on the same selected cases. |
| Full-domain, multi-agent architecture comparison | [Eval v3](v3/README.md) | The source corpus must be independent of the question and an Answering Agent must differ from the source authors. |
| Extraction or recall-policy iteration | [`stage/`](stage/README.md) or `recall-v2/` | Diagnosing deterministic extraction and planning before paying for an Agent cohort. |

Use the smallest harness that can falsify the hypothesis. A recall-ranking
change first needs stage evidence on a fixed fixture. An end-to-end run is the
outer validation that an Agent can use the delivered evidence to improve its
answer.

## Define the experiment before writing code

Write these fields in the issue, PR description, or result note:

| Field | Required decision |
| --- | --- |
| Hypothesis | The memory behavior expected to improve and the failure mode it addresses. |
| Unit | One case is one question plus its immutable source Session Events. |
| Cohort | Pinned manifest path and dataset revision; do not select cases after seeing results. |
| Arms | A contemporaneous baseline and one changed memory architecture or policy. |
| Fixed conditions | Answering model, prompt, token budget, judge, identity mapping, and source events. |
| Primary metric | Judge correctness and paired win/loss/tie. Token F1 is diagnostic only. |
| Safety slices | Identity, authorization, temporal validity, abstention, and superseded-fact behavior relevant to the change. |

Use a fresh `run.id` and `output_dir` for each publishable run. The resolved
configuration hash is part of experiment identity; changing a strategy,
provider, prompt, or budget without changing the run identity mixes arms and
invalidates the comparison.

## Preserve source, identity, and time

Each case needs a manifest entry and a native session-batch artifact:

```text
runs/<selection>/
  manifest.json
  cases/<case-id>/
    producer/source.md
    producer/session-batches.json
    consumer/README.md
```

The manifest pins the question, gold answer, `asking_user_id`, and an isolated
`scope_id`. `session-batches.json` is the source of truth for events. Every
event must retain its original user, agent, session, sequence, occurrence time,
visibility, and channel or phase metadata.

Do not flatten all messages into anonymous text for a new architecture eval.
The memory system must receive the same event set as every competing memory
arm. Do not choose source messages by looking at the question in a full-domain
eval; use Eval v3 when that separation is a requirement.

Keep these identities distinct:

- **Source Actor**: the original `(user_id, agent_id, session_id)` on every
  event.
- **Asking User**: the user whose perspective the question represents.
- **Answering Agent**: the Agent that receives recalled context and answers.
- **Memory scope**: the collaboration boundary resolved by credentials or
  provider configuration, never by untrusted event metadata.

For multi-agent claims, select one Answering Agent deterministically and reuse
it across all paired arms. Record whether that Agent authored any supporting
source event. Eval v3 calls the reviewed no-overlap subset
`strict_cross_agent`.

Time also has to be explicit. Preserve source occurrence time, memory
observation time, and question/as-of time. A `current`, `latest`, `before`, or
`as of` question must not return a superseded fact merely because it is
lexically similar.

## Make the Agent path real

The answer must be produced by the same kind of Agent integration used in the
product path. For the current OpenCode harness that means the production paxm
plugin and provider adapter, not a test-only prompt helper.

For every successful memory arm, retain evidence for all of the following:

1. **Ingest receipt**: accepted event/session counts and provider identity.
2. **Readiness**: the provider had finished processing the exact ingest before
   the answerer ran.
3. **Agent exposure**: passive context insertion or an actual focused-recall
   call was observed.
4. **Recall diagnostics**: provider calls, candidates, eligibility, hits,
   injected context items, budget drops, and rejection reasons when available.
5. **Answer and judgment**: raw Agent JSON output plus an independent judge
   result.

An external provider that returns text to a helper is not a valid Agent-memory
trial until that text is injected into the real answering Agent and the run
records the injection. Conversely, an Agent answer with no successful provider
call is a control-like result, not evidence that a memory strategy worked.

## Start from Eval v2 for a paired memory comparison

Copy a nearby local profile and keep the standard lifecycle commands unless the
provider contract genuinely differs:

```bash
cp evals/v2/config.example.yaml evals/v2/config.<experiment>.local.yaml

make eval-v2-up \
  MANIFEST=runs/<selection>/manifest.json \
  RUN_ID=<unique-run-id>

make eval-v2 CONFIG=evals/v2/config.<experiment>.local.yaml
make eval-v2-down
```

At minimum configure a real baseline and a candidate arm. Do not claim a
one-arm rerun against a historical baseline is a paired experiment; it is a
follow-up measurement and must state which conditions were reused from the
older artifact. For a fair external-provider comparison, run the external arm
and the baseline in the same clean stack, manifest, model, and judge protocol.

The standard lifecycle is:

```text
preflight -> source ingest -> provider readiness -> answering Agent -> judge
```

Preflight must execute a real write and recall for the providers that will be
scored. Fail the run before invoking paid Agents if it cannot. `trial_timeout`
must cover ingest, readiness, and answer generation; do not turn a slow
readiness stage into a silent empty-context answer.

For a new provider, add its ingest, readiness, and consumer behavior behind
the shared Eval v2 command contract. Keep `RecallNotes` and the normal
`PlanRecall` seam for Team Note arms; provider-specific lifecycle code belongs
in the eval adapter, not in a new production recall API.

## Judge and compare answers

Use the configured independent judge for semantic correctness. It receives the
question, gold answer, and final Agent answer, but not the arm name or
retrieval trace. Record its raw output and session/cost metadata.

Report, at minimum:

- completed and failed trials;
- judge accuracy by arm and category;
- paired judge wins, losses, and ties against the baseline;
- mean Token F1, explicitly labelled diagnostic;
- ingest/readiness/consumer and total duration;
- reported model cost scope and any provider billing that is not included;
- recall activity and failure reasons; and
- identity, temporal, abstention, and superseded-fact slices relevant to the
  hypothesis.

Never promote a higher Token F1 over a lower judge accuracy. A near answer with
the wrong owner, date, or current state is still incorrect.

## Inspect and hand off artifacts

Eval v2 writes these durable artifacts under `run.output_dir`:

| Artifact | Review purpose |
| --- | --- |
| `report.html` | Fast overall and per-case review. |
| `trials.jsonl` | Lossless source for any later analysis. |
| `trials.csv` and `summary.csv` | Flat comparison and aggregate metrics. |
| `pairwise.csv` | Judge and lexical paired deltas against the configured baseline. |
| `artifacts.json` and `config.resolved.json` when present | Provenance, configuration hash, runtime values, and output paths. |
| `trials/<case-id>/<arm>/` | Raw ingest, readiness, recall, consumer, and judge logs. |

When reporting a run, link the report and `trials.jsonl`, name the exact
manifest and config, state whether the stack was clean and isolated, and call
out every incomplete or manually recovered trial. Do not silently replace a
failed trial with a later success.

## Common invalid experiments

- Directly calling a memory search API and treating its text as an Agent
  answer.
- Letting the candidate arm see a different source transcript, Answering
  Agent, prompt, token budget, or judge than the baseline.
- Reusing an old baseline after changing the model, manifest, runtime
  configuration, or provider isolation behavior.
- Inferring a recall regression from an answer without checking that extraction
  produced the required fact.
- Measuring only final context and omitting candidates, relation expansion,
  selection, and budget-drop diagnostics.
- Reporting only Token F1, a hand-picked case, or a provider API success as an
  end-to-end win.
- Dropping author identity or timestamps from source events, then claiming
  agent-aware or temporal correctness.

## Before opening a PR

For code changes, add table-driven tests for the new provider or lifecycle
behavior and run:

```bash
make lint test
```

For an experiment-only contribution, preserve the manifest, config, raw
artifacts, result note, and the negative cases. A useful evaluation makes a
future regression reproducible, not merely a current screenshot persuasive.
