# Eval v2 extraction timing instrumentation

Status: implemented
Date: 2026-07-15

## Problem

Eval v2 measures a producer-to-memory-readiness interval, but the service did
not persist when an ingested session event was captured or when extraction
covered it. That made slow readiness trials difficult to separate into ingest
delay and extraction delay after the run.

## Scope

- Expose the existing `session_events.captured_at` timestamp on replayed
  `SessionEvent` values.
- Add a nullable `session_events.extracted_at` timestamp.
- When the extraction cursor advances, atomically mark events through that
  cursor as extracted without changing an existing timestamp.
- Keep cursor advancement and extraction timestamps in one PostgreSQL
  transaction.
- Verify observable lifecycle behavior against PostgreSQL: capture time is
  populated, events above the cursor remain unmarked, and later advancement
  marks them.

## Non-goals

- No new report metric is derived from these timestamps yet.
- No change to extraction scheduling or retry policy.
- No backfill of historical extraction timestamps.

## Result

Migration `004_extraction_latency.sql`, the PostgreSQL session store, and the
session contract now provide the evidence needed to diagnose eval readiness
latency independently of the HTML renderer.
