# Team Note

The Team Note context owns short-lived, evidence-backed collaboration state for passive recall.

## Language

**Candidate**:
A model-proposed collaboration fact grounded in one or more Session Events.

**Team Note**:
The current admitted revision of a short-lived collaboration fact.
_Avoid_: Wiki page, raw memory

**Delivery**:
A recorded insertion of one Team Note revision into an agent session.

## Relationships

- A **Candidate** cites one or more Session Events.
- A **Candidate** creates, updates, or resolves one **Team Note**.
- A **Team Note** may produce one **Delivery** per revision and recipient session.

## Example dialogue

> **Dev:** "Should this long project history become a **Team Note**?"
> **Domain expert:** "Only the current handoff should; durable history belongs in LLM Wiki."

## Flagged ambiguities

- "note" means **Team Note** only inside this context; LLM Wiki stores pages.
