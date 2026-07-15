# LLM Wiki

The LLM Wiki context will own durable, actively browsed knowledge derived from Session Lake evidence.

## Language

**Wiki Page**:
A durable file-like knowledge unit maintained from multiple Session Events.
_Avoid_: Team Note

**Maintenance Batch**:
A larger Session Lake range processed to create or revise Wiki Pages.

**Active Recall**:
Agent-directed browsing or search over Wiki Pages.
_Avoid_: Passive Team Note delivery

## Relationships

- A **Maintenance Batch** consumes many Session Events.
- A **Maintenance Batch** creates or revises one or more **Wiki Pages**.
- **Active Recall** reads Wiki Pages without depending on Team Note.

## Example dialogue

> **Dev:** "Should **Active Recall** query Team Notes before reading a page?"
> **Domain expert:** "No. Both products share Session evidence but expose independent recall paths."

## Flagged ambiguities

- LLM Wiki is a future product Module, not a second persistence adapter for Team Note.
