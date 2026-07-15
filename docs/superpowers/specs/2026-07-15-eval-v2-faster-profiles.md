# Eval v2 faster execution and profiles

Status: implemented
Date: 2026-07-15

## Confirmed scope

Implement the agreed speed items without changing model thinking mode or
adding provider-internal cost accounting:

1. Fail-fast provider preflight and no blind failed-trial retry.
2. One shared producer transcript per case for Team Note and Mem0.
3. A 3-case smoke profile and the existing 30-case acceptance profile.

## Requirements

- Before runnable paid trials, exercise real health, add, and recall paths for
  Team Note and Mem0; exercise delete where the provider supports it.
- A failed preflight must leave the matrix pending and must not invoke producer
  or consumer agents.
- Failed trials are not retried by default. Opt-in retry must declare a finite
  maximum attempt count, enforced atomically by PostgreSQL.
- For each case, call the producer model at most once, persist its exact plain
  text output, and ingest that same text into both memory providers.
- A second memory arm waiting on the same producer must reuse its success or
  failure; a later bounded retry must reuse a completed on-disk producer
  artifact.
- Do not charge the shared producer output to both arms. Until run-level cost
  accounting is implemented, retain its raw OpenCode JSONL and document that
  arm cost totals exclude it.
- The smoke manifest must contain one multi-hop, one knowledge-update, and one
  abstention case. The acceptance manifest remains 30 balanced cases.
- DeepSeek thinking configuration and Team Note/Mem0 internal cost collection
  are unchanged.
