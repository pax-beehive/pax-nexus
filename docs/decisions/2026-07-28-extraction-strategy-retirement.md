# Extraction candidate strategy retirement

Status: accepted

Date: 2026-07-28

## Context

The extractor kept ten candidate strategies permanently alive. One ships
(selected at link time); the rest carried permanent test and lint cost with
no retirement mechanism, and the strategy count only grew.

## Decision

Keep five strategies: `source-clause-v1` (build default), `interaction-slim`,
`source-span-v1`, `source-span-v2` (active evals), and `claim-card-v2`
(newest experiment). Delete `current`, `evidence-fidelity-v1`,
`source-clause-implicit-state-v1`, `typed-2`, and `claim-card-v1` with their
strategy-specific prompts, decoders, and tests.

Standing policy: a strategy enters `candidate_strategies` with a stated
experiment goal and an eval exit condition. When the experiment concludes,
the strategy is either promoted to the build default or deleted in the same
change that records the conclusion. Git history is the archive; the table
holds no dormant entries.

## Consequences

- Retired strategy names now fail `make build` validation and runtime
  strategy resolution.
- Rolling episodes stored under retired protocol versions lose their warm
  prefix and are re-extracted on the next slice (verified: episode
  compatibility is an exact protocol-version match; saved content decoding
  only runs for compatible episodes).
- `rollingSystemPromptV2` and `rollingSystemPromptClaimCardV1` remain in the
  code as building blocks of `claim-card-v2`'s prompt bytes.
