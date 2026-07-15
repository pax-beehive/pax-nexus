# Evaluation

The Evaluation context owns reproducible experiments that measure Nexus product quality.

## Language

**Case**:
A fixed input, question, expected answer, and identity scope used in an evaluation.

**Arm**:
One product strategy or control executed for the same Case.

**Run**:
A versioned collection of Cases, Arms, configuration, and artifacts.

## Relationships

- A **Run** contains many **Cases**.
- Each **Case** executes one or more **Arms**.
- Evaluation may call Team Note or LLM Wiki through their public seams.

## Example dialogue

> **Dev:** "Can a benchmark helper be imported by Team Note?"
> **Domain expert:** "No. An **Arm** depends on the product under test, never the reverse."

## Flagged ambiguities

- "test" means a Go verification unless it is explicitly called an evaluation **Run**.
