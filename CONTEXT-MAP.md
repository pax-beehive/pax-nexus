# PAX Nexus Context Map

## Contexts

- [Session](./internal/session/CONTEXT.md) — shared agent identity and immutable session evidence.
- [Team Note](./internal/teamnote/CONTEXT.md) — short-lived passive collaboration recall.
- [Evaluation](./internal/eval/CONTEXT.md) — reproducible quality measurement and benchmark adapters.
- [LLM Wiki](./internal/llmwiki/CONTEXT.md) — durable, actively browsed knowledge maintained from session evidence.
- [On-prem Identity](./internal/deployment/onprem/CONTEXT.md) — human membership, Agent ownership, and credential-bound access for one installation.

## Relationships

- **Session → Team Note**: Team Note extracts bounded facts from Session Lake events.
- **Session → LLM Wiki**: LLM Wiki will maintain durable pages from larger Session Lake batches.
- **Evaluation → Team Note/LLM Wiki**: Evaluation may exercise product contexts; product contexts never import Evaluation.
- **Team Note ↔ LLM Wiki**: They share Session evidence but do not import each other.
- **On-prem Identity → Session/Team Note/LLM Wiki**: On-prem Identity authenticates human and Agent principals; product contexts consume the resulting identity but do not own accounts or credentials.

The implementation boundary and extension rules are documented in the
[Session Lake processor guide](./docs/session-lake-processors.md).
The Human Portal contract, role-aware user journeys, and frontend edge cases
are documented in the
[on-prem identity frontend integration guide](./docs/on-prem-identity-frontend-integration.md).
