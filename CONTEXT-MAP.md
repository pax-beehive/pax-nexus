# PAX Nexus Context Map

## Contexts

- [Session](./internal/session/CONTEXT.md) — shared agent identity and immutable session evidence.
- [Team Note](./internal/teamnote/CONTEXT.md) — short-lived passive collaboration recall.
- [PageWiki](./internal/pagewiki/CONTEXT.md) — the shipping wiki product: durable, cited pages maintained from session evidence.
- Recall (`internal/recall`) — routes recall requests across product paths; owns no adapters.
- Explorer (`internal/explorer`) — read-only team-memory diagnostics for operators.
- [Evaluation](./internal/eval/CONTEXT.md) — reproducible quality measurement and benchmark adapters.
- [On-prem Identity](./internal/deployment/onprem/CONTEXT.md) — human membership, Agent ownership, and credential-bound access for one installation.
- [Operations](./internal/operations/CONTEXT.md) — bounded service activity, diagnostics, and storage accounting for operators.
- Platform (`internal/platform`) — technical infrastructure: Postgres adapters, observability, text embedding, and the shared LLM chat client (`platform/llm`).
- [LLM Wiki](./internal/llmwiki/CONTEXT.md) — experimental spike (workspace agent, effect eval, session datasets) and a reserved name for a future actively browsed knowledge module. Not a shipping product; PageWiki is.

## Relationships

- **Session → Team Note**: Team Note extracts bounded facts from Session Lake events.
- **Session → PageWiki**: PageWiki maintains durable pages from Session Lake batches.
- **Recall → Team Note**: Recall routes across product recall paths; product domain packages never import Recall.
- **Evaluation → products**: Evaluation may exercise any product context; product contexts never import Evaluation.
- **Platform → products**: Platform adapters implement ports defined by product contexts (dependency points at the domain). Exception: `platform/observability` and `platform/llm` are shared technical services that domains may import.
- **On-prem Identity → Session/Team Note/PageWiki**: On-prem Identity authenticates principals; product contexts consume the resulting identity but do not own accounts or credentials.
- **Operations → products/On-prem Identity**: Operations observes bounded outcomes and storage measurements without owning product state.
- **Team Note ↔ PageWiki**: They share Session evidence but do not import each other's domain packages.

The dependency rules are enforced by `internal/architecture/dependencies_test.go`.

The implementation boundary and extension rules are documented in the
[Session Lake processor guide](./docs/session-lake-processors.md).
The Human Portal contract, role-aware user journeys, and frontend edge cases
are documented in the
[on-prem identity frontend integration guide](./docs/on-prem-identity-frontend-integration.md).
