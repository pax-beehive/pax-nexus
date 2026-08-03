# Session Audit Producer Contract

This document is the producer contract for tool-related events on the
agent-session ingest path (`POST /v1/session-batches`). It requires **no IDL
change**: session events already carry a free-form `Type` string and a
`map<string,string>` `Metadata` passthrough. The canonical constants live in
`internal/audit/contract.go`; this document describes them for out-of-repo
producers (paxm / paxl hooks).

The consumer (`internal/audit`, processor `session_audit`) is read-only and
tolerant: legacy streams without any of these event types are processed
normally and still produce work-pattern activity projections.

## Event types

### `type="tool_call"`

An agent invoked (executed) a tool.

| Metadata key          | Required | Meaning                                   |
| --------------------- | -------- | ----------------------------------------- |
| `tool.call_id`        | yes      | Stable identifier correlating call/result/approval. |
| `tool.name`           | yes      | Tool name, e.g. `Bash`, `Read`, `Write`.  |
| `tool.input_summary`  | yes      | Truncated summary of the tool input. Producers SHOULD truncate; the audit classifier matches on substrings. |
| `approval.decision`   | no       | Inline decision, see below.               |

### `type="tool_result"`

A tool call finished.

| Metadata key   | Required | Meaning                              |
| -------------- | -------- | ------------------------------------ |
| `tool.call_id` | yes      | Correlates with the `tool_call`.     |
| `tool.status`  | yes      | Outcome string, e.g. `ok`, `error`.  |

`tool_result` events are recorded as evidence but produce no audit rows in
v1; they are reserved for future latency/outcome projections.

### `type="tool_approval"`

A human or policy engine approved or denied a tool call.

| Metadata key        | Required | Meaning                                |
| ------------------- | -------- | -------------------------------------- |
| `tool.call_id`      | yes      | Correlates with the `tool_call`.       |
| `approval.decision` | yes      | One of `approved`, `denied`, `auto`.   |
| `approval.actor`    | no       | Who/what decided (user id or `policy`).|

`auto` means the call was auto-approved by local policy; the consumer treats
it as approved.

## Approval correlation

- `approval.decision` MAY ride on the `tool_call` event itself instead of a
  separate `tool_approval` event. The consumer accepts both.
- Correlation is by `tool.call_id` within one consumed batch. Approval states
  merge monotonically at storage time: `unknown` may become `approved` or
  `denied`, never the reverse, and a recorded `approved`/`denied` is never
  overwritten.
- Approvals for calls whose `tool_call` event lives in an earlier or later
  batch are still applied to the stored tool-call row when it exists (state
  upgrade from `unknown` only).

## Legacy tolerance

Streams ingested before this contract existed contain none of these event
types and may carry arbitrary metadata. The consumer must keep projecting
activity for them and must not fail on missing metadata keys; a `tool_call`
event without `tool.call_id` is still audited, but cannot be correlated with
approvals.
