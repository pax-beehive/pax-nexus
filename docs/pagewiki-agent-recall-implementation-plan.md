# PageWiki Agent recall implementation plan

## Goal

Expose PageWiki through the credential-bound Agent Memory API without retaining
the old LLM Wiki contract.

Agents continue to use:

```text
POST /v1/memory/search
POST /v1/memory/get
```

An active PageWiki search uses `intent=active` and `source=pagewiki`. A passive
search keeps Team Note recall as the primary path. PageWiki search may run
speculatively, but a sufficient Team Note result returns immediately without
waiting for PageWiki.

## Product decisions

- Agent identity comes only from the authenticated Bearer credential.
- `search` and `get` permissions remain the authorization boundary.
- PageWiki adapters call the scoped repository directly. They do not call the
  Human Portal HTTP endpoints.
- Search returns current PageRevision passages. Every returned reference names
  the exact immutable PageRevision that was searched.
- Get resolves that immutable PageRevision and never silently upgrades the
  reference to the current revision.
- PageWiki citations and links use typed fields in the Agent Memory response.
  They are not encoded as JSON inside the generic metadata map.
- Passive recall returns Team Note evidence immediately when
  `evidence_sufficient=true`. The PageWiki context is cancelled and the HTTP
  response does not wait for cleanup.
- Passive PageWiki results are references, not a replacement for Team Note
  evidence sufficiency.
- Active PageWiki search is explicit and does not invoke Team Note recall.
- `llm_wiki`, `TEAM_MEMORY_WIKI_HINT_ENABLED`, legacy Wiki hydration, and the
  old hint-shaped recall trace are removed rather than aliased.

## Stable reference

Search results use:

```text
pagewiki:revision/<page-revision-id>
```

The matching section is carried in typed PageWiki context. Multiple passages
may therefore refer to the same immutable document without inventing separate
document identities.

## TDD loop

Every Slice follows:

```text
write Given/When/Then acceptance
→ run the focused test and record the expected red result
→ implement the smallest behavior
→ add table-driven boundary tests
→ refactor without changing behavior
→ run focused coverage and package tests
→ commit the independently verifiable Slice
```

New and changed backend packages must remain at or above 80 percent unit-test
coverage.

## Slice A: active PageWiki search

### BDD acceptance

```gherkin
Scenario: an Agent actively searches PageWiki
  Given an authenticated Agent with the search permission
  And PageWiki contains a current revision with an exact citation
  When the Agent searches memory with intent "active" and source "pagewiki"
  Then the response contains the matching current-revision passage
  And the hit references the immutable PageRevision
  And Team Note recall is skipped
  And the PageWiki trace is completed

Scenario: old LLM Wiki source is rejected
  Given an authenticated Agent with the search permission
  When it searches with source "llm_wiki"
  Then the request is rejected as an unsupported source
```

### Smallest implementation

1. Replace `SourceLLMWiki` with `SourcePageWiki`.
2. Replace the old Wiki hint/search port with a PageWiki search/get port.
3. Add a PageWiki recall adapter over the existing PageWiki reader.
4. Map `pagewiki.SearchResult` to budgeted `recall.MemoryHit` values.
5. Wire the adapter into `recall.NewRouter`.

Expected commit:

```text
feat(recall): add active PageWiki search
```

## Slice B: immutable PageWiki get and typed provenance

### BDD acceptance

```gherkin
Scenario: an Agent gets the exact revision returned by search
  Given search returned a PageWiki revision reference
  And the Page later advances to another revision
  When the Agent gets the original reference
  Then the original immutable Markdown is returned
  And its Page identity, revision, citations, source anchors, and links are
      returned as typed provenance

Scenario: malformed or unknown PageWiki refs fail closed
  Given an authenticated Agent with the get permission
  When it gets a malformed or missing revision reference
  Then no current Page is substituted
  And the request returns a stable client or not-found error
```

### Smallest implementation

1. Extend the Thrift contract with optional PageWiki context, citations, source
   anchors, and links.
2. Add a strict PageWiki revision-ref parser.
3. Resolve PageRevision, Page, and link context through the repository.
4. Map typed context through the handwritten handler bridge.
5. Regenerate Hertz models and routers.

Expected commit:

```text
feat(recall): get immutable PageWiki revisions
```

## Slice C: passive early return and PageWiki fallback

### BDD acceptance

```gherkin
Scenario: sufficient Team Note evidence does not wait for PageWiki
  Given Team Note recall returns sufficient evidence
  And PageWiki search is still blocked
  When an Agent performs passive recall
  Then the response returns the Team Note evidence immediately
  And PageWiki receives context cancellation
  And the trace records early_return=true
  And the PageWiki trace records cancellation

Scenario: insufficient Team Note evidence is supplemented by PageWiki
  Given Team Note recall completes with insufficient evidence
  And PageWiki contains relevant current knowledge
  When an Agent performs passive recall
  Then Team Note evidence and PageWiki references share one token budget
  And max_items applies to the combined response
  And budget drops are visible in the PageWiki trace

Scenario: Team Note failure is not hidden by PageWiki
  Given Team Note recall fails
  And PageWiki search succeeds
  When an Agent performs passive recall
  Then the recall request fails
  And PageWiki does not mask the primary-path failure
```

### Smallest implementation

1. Start PageWiki lexical search speculatively beside Team Note recall.
2. Decide only from the Team Note `evidence_sufficient` result.
3. Cancel PageWiki and return immediately on sufficient evidence.
4. Otherwise combine already completed or awaited PageWiki results.
5. Pack PageWiki references into the remaining token and item budgets.

Expected commit:

```text
feat(recall): fall back to PageWiki after insufficient notes
```

## Slice D: remove old LLM Wiki compatibility

### BDD acceptance

```gherkin
Scenario: startup no longer reads legacy Wiki state
  Given legacy LLM Wiki tables contain rows
  And PageWiki tables are empty
  When the PageWiki repository starts
  Then no PageWiki page is hydrated from legacy rows

Scenario: old hint configuration is unsupported
  Given TEAM_MEMORY_WIKI_HINT_ENABLED is set
  When configuration is loaded
  Then the old setting has no effect because it is no longer part of the
       application configuration
```

### Smallest implementation

1. Remove legacy hydration from the PageWiki PostgreSQL repository.
2. Delete the legacy hydration implementation and tests.
3. Remove `TEAM_MEMORY_WIKI_HINT_ENABLED` and related config.
4. Replace old Wiki hint/search trace fields with one PageWiki trace.
5. Update README and accepted decision documentation to the PageWiki contract.

Expected commit:

```text
refactor(recall): remove legacy LLM Wiki paths
```

## Slice E: end-to-end acceptance and quality gate

### PostgreSQL acceptance

```gherkin
Scenario: real Session evidence is recalled through the Agent API
  Given a Session is persisted in Session Lake
  And the PageWiki consumer publishes a cited PageRevision
  When an authenticated Agent calls memory/search and memory/get
  Then search returns the current PageWiki passage
  And get returns the exact immutable revision
  And every citation resolves to the original Source event quote
```

### Verification

```bash
GOCACHE=/private/tmp/paxd-go-cache \
  go test ./internal/recall/... ./internal/pagewiki/... \
  ./internal/teamnote/transport/httpapi/handler/...

GOCACHE=/private/tmp/paxd-go-cache \
  go test -coverprofile=/private/tmp/pagewiki-agent-recall.cover \
  ./internal/recall/... ./internal/pagewiki/recalladapter/...

GOCACHE=/private/tmp/paxd-go-cache go test ./...

make generate
make format-check
make lint
make build
```

PostgreSQL contract and integration tests must run against PostgreSQL, never an
SQLite substitute.

