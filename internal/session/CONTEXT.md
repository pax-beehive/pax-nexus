# Session

The Session context owns evidence shared by every PAX Nexus knowledge product.

## Language

**Actor**:
A stable user, agent, and session identity attached to an event.
_Avoid_: Team Note author, Wiki author

**Session Event**:
An immutable observed message or action with ordered identity and occurrence time.
_Avoid_: Note, memory

**Session Lake**:
The durable ordered collection of Session Events and consumer cursors.
_Avoid_: Team Note database, Wiki database

## Relationships

- An **Actor** produces many **Session Events**.
- A **Session Lake** stores many ordered **Session Events**.
- Team Note and LLM Wiki consume the same **Session Lake** through independent cursors.

## Example dialogue

> **Dev:** "Should the **Session Lake** know whether an event becomes a Team Note or Wiki page?"
> **Domain expert:** "No. It preserves **Session Events**; each product decides how to derive knowledge."

## Flagged ambiguities

- "memory" must not be used for raw Session Events; it refers to a derived product.
