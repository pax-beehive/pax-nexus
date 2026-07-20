# Source Clause v1 implementation validation

Date: 2026-07-19 (America/Los_Angeles)

`source-clause-v1` is implemented as the successor to the rejected
`evidence-fidelity-v1` candidate. It keeps `Extractor.Extract`, Runtime,
`ApplyExtractionRun`, and the semantic Candidate schema unchanged.

The deterministic validation covers:

- exact, contiguous source-clause citations and rejection of missing,
  fabricated, proposal-only, request-only, and question-only citations;
- temporal admission against the maximum source time of new Events, with no
  wall-clock dependency;
- provider attempt deadlines, bounded transient retry, output and response
  budgets, stable failure classification, and invalid-response telemetry;
- one extraction first-loss ledger entry per required Atom.

Focused Go tests pass for the extractor, extraction shadow/eval packages, the
extraction command, and service configuration.

The zero-cost `finance-micro3-quick` preflight could not be completed against
the current local eval database. The historical source scope
`groupmembench-finance-v3-micro6-general-recall-v3-20260717-r3-groupmembench-Finance`
contains no persisted Events in that database. This is a missing test fixture,
not a provider or extraction failure. No paid provider call was made and no
quality comparison is claimed. Promotion remains blocked until the fixed
source is restored or regenerated and paired against `interaction-slim`.

Superseded on 2026-07-20: the fixed source was restored and the first valid
`source-clause-v1` run was selected as the Extraction Evaluation Baseline. See
the [baseline decision](./2026-07-20-source-clause-v1-baseline.md).
