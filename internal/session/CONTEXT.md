# Session

The Session context owns evidence shared by every PAX Nexus knowledge product.

## Language

**Actor**:
A stable user, agent, and session identity attached to an event.
_Avoid_: Team Note author, Wiki author

**Session Event**:
An immutable observed message or action with ordered identity and occurrence time.
_Avoid_: Note, memory

**Evidence Lake**:
The durable ordered collection of Session Events and consumer cursors.
_Avoid_: Team Note database, Wiki database

**Stream**:
The generalized identity `(source, stream_id)` that every Evidence Lake event
belongs to. `agent-session` streams (`source="agent-session"`) carry legacy
Session Events; other registered sources such as `im-channel` carry
`StreamEvent`s pushed by external connectors through the same ingest contract.
_Avoid_: conversation, channel (those are source-specific concepts; the Lake
only knows the generalized stream identity)

## Relationships

- An **Actor** produces many **Session Events**.
- An **Evidence Lake** stores many ordered **Session Events** and **Stream Events**, keyed by **Stream**.
- Team Note and LLM Wiki consume the same **Evidence Lake** through independent cursors.
- External connectors (Slack bridges, IM adapters, etc.) are separate programs outside this repository; they push events through the generic ingest contract. This repository owns only the ingest contract and storage, not any specific connector.

## Example dialogue

> **Dev:** "Should the **Evidence Lake** know whether an event becomes a Team Note or Wiki page?"
> **Domain expert:** "No. It preserves **Session Events** and **Stream Events**; each product decides how to derive knowledge."

## Flagged ambiguities

- "memory" must not be used for raw Session Events; it refers to a derived product.
- Stream-keyed extraction, identity resolution (`source + native_id → user_id`), and media/blob storage are explicitly future plans, not implemented by the current Evidence Lake ingest contract.
