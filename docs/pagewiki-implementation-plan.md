# Page-centric LLM Wiki implementation plan

## Handoff

Repository:

```text
/Users/gengcongkai/pax_workspace/pax-nexus
```

Branch:

```text
agent/llm-wiki-workspace-spike
```

Current committed HEAD:

```text
1fa4d88 feat(pagewiki): inject a cited page in memory
```

Completed commits:

```text
65f3426 feat(pagewiki): define page-centric domain and ports
1fa4d88 feat(pagewiki): inject a cited page in memory
```

Current uncommitted state:

```text
?? internal/pagewiki/multi_target_acceptance_test.go
```

That file is the intentionally red BDD acceptance test for Slice 3. Do not
delete it. No Slice 3 implementation patch was applied before the previous
session was interrupted.

Reproduce the current red state with:

```bash
GOCACHE=/private/tmp/paxd-go-cache \
  go test ./internal/pagewiki/... -run MultiTargetAcceptance
```

The expected compile failures are missing `pagewiki.Navigation` and
`memory.Repository.Navigation`.

## Product and authority decisions

This is a new implementation under `internal/pagewiki`. It does not preserve
compatibility with `internal/llmwiki/workspace` and must not introduce
Statement, Knowledge, Entity graph, SemanticBinding, Inference, conflict graph,
Wiki node tree, or independently revisioned Section/Block models.

The canonical content of a page is the complete normalized Markdown stored in
an immutable `PageRevision`. Citation, link, and Section records belong to that
specific revision.

Database repositories will own Page identity, revision CAS, current revision,
citations, links, topics, placements, run audit, and derived search chunks. A
Git snapshot can later be exported for human inspection and rollback audit, but
it must not become a second independently editable authority.

Source byte offsets are UTF-8 byte offsets. A citation has two deterministic
ends:

1. unique `ExactText` inside one PageRevision Section;
2. unique evidence quote inside an allowed Source event.

The program validates both ends and materializes an absolute `SourceAnchor`.
This proves structural grounding. It does not claim to deterministically prove
semantic entailment.

## TDD rules

Each Slice follows:

```text
write observable Given/When/Then acceptance
→ run and record red
→ implement the smallest vertical behavior
→ add table-driven boundary tests
→ refactor
→ run coverage, vet, and the full repository test suite
→ commit the independently verifiable Slice
```

Tests use Go `testing`, `github.com/stretchr/testify/suite`, and
`require`/`assert`. Avoid tests coupled to private fields.

The quality gate for new backend code is at least 80 percent aggregate unit
coverage.

Useful commands:

```bash
GOCACHE=/private/tmp/paxd-go-cache \
  go test ./internal/pagewiki/...

GOCACHE=/private/tmp/paxd-go-cache \
  go test -coverpkg=./internal/pagewiki/... \
  -coverprofile=/private/tmp/pagewiki.cover \
  ./internal/pagewiki/...

GOCACHE=/private/tmp/paxd-go-cache \
  go tool cover -func=/private/tmp/pagewiki.cover

GOCACHE=/private/tmp/paxd-go-cache \
  go vet ./internal/pagewiki/...

GOCACHE=/private/tmp/paxd-go-cache \
  go test ./...
```

The last known green aggregate coverage before the Slice 3 red test was 82.9
percent. The full repository test suite and `go vet` passed.

## Existing implementation

The current package tree is:

```text
internal/pagewiki/
├── contracts.go
├── contracts_test.go
├── inject_acceptance_test.go
├── memory/
│   ├── repository.go
│   └── repository_test.go
├── multi_target_acceptance_test.go
├── ports.go
├── scripted.go
├── service.go
├── service_validation_test.go
└── types.go
```

The committed implementation already supports:

- SourceRevision identity from Source ID and SHA-256;
- immutable Source bytes and stable event byte ranges;
- planner and editor ports;
- scripted deterministic planner/editor;
- PageBrief validation for create and update identity;
- one to eight planned targets;
- full PageDraft rendering into immutable Markdown;
- stable Section keys inside PageRevision;
- unique Page citation text validation;
- allowed Event ID validation;
- unique exact Source quote validation;
- materialized SourceAnchor byte ranges;
- atomic in-memory Page and PageRevision publication;
- target failure without publishing invalid content;
- MaintenanceRun/Target results;
- repository immutability and clone-on-read/write.

## Slice 3: multiple targets, topics, and failure isolation

### Observable result

One Session can create multiple pages. A broken target does not roll back a
valid sibling. Every created page is reachable through a topic path of at most
two levels. No Inbox or fallback topic exists.

### Current red acceptance

The existing untracked test covers:

- two valid PageBriefs publish two navigable pages;
- a forged citation in target B leaves target A published;
- the failed target leaves no empty Topic;
- an ambiguous brief stays pending without creating Inbox;
- source-only is a successful terminal action without a Page.

### Smallest implementation

1. Add to `types.go`:

```go
type PagePublication struct {
    Page      Page
    Revision  PageRevision
    Topics    []Topic
    Placement *PagePlacement
}

type Navigation struct {
    Roots []NavigationTopic
}

type NavigationTopic struct {
    ID       string
    Slug     string
    Title    string
    Children []NavigationTopic
    Pages    []NavigationPage
}
```

2. Change the repository port from:

```go
PublishPage(context.Context, Page, PageRevision) error
```

to:

```go
PublishPage(context.Context, PagePublication) error
Navigation(context.Context) (Navigation, error)
```

3. Make create briefs require a non-empty topic path. Keep the two-level
maximum already enforced by `ValidatePageBrief`.

4. In the service, deterministically build Topic IDs from normalized path and
parent identity. Publish Page, PageRevision, newly required Topics, and
PagePlacement through one repository call.

5. In the memory repository, validate all publication input before mutating
maps. Store topics and placements only after PageRevision validation succeeds.
This is what prevents a failed target from leaving empty Topic records.

6. Treat:

```text
source-only → succeeded target, no Page
ambiguous   → pending target, no Page, no placement
```

7. Implement deterministic sorted navigation from Topic and PagePlacement.

8. Adapt memory repository tests to the `PagePublication` input and add tests
for invalid placement, missing parent Topic, Topic immutability, and
clone/sorted navigation behavior.

Expected commit:

```text
feat(pagewiki): publish independent targets into topics
```

## Slice 4: immutable updates, history, idempotency, and CAS

### Acceptance to write first

```gherkin
Scenario: a later Session updates an existing Page
  Given Session one created Page "sqlite" at revision one
  And Session two contains a later decision
  And its planner chooses the Page from the supplied catalog
  When Session two is injected
  Then the Page ID is unchanged
  And revision two is current
  And revision one remains byte-for-byte unchanged
  And the existing placement is retained

Scenario: stale base cannot overwrite a current PageRevision
  Given two updates read revision one
  And the first publishes revision two
  When the second publishes against revision one
  Then it fails with revision conflict
  And revision two remains current

Scenario: retrying the same injection is idempotent
  Given an injection completed successfully
  When the same SourceRevision and idempotency key are retried
  Then no duplicate SourceRevision, Page, or PageRevision is created
```

### Implementation notes

- Add `IdempotencyKey` to the Inject request.
- Do not use SourceRevision ID alone as MaintenanceRun identity. The current
  implementation does this and must be replaced before retry semantics are
  considered complete.
- An update brief must target a Page and expected base from the exact planner
  catalog input.
- The editor receives a clone of the complete current PageRevision.
- The repository performs CAS inside the target publication transaction.
- A nil placement on update means preserve the current placement.
- Add revision history query sorted by revision lineage.
- Canonical PageDraft + Citation + Link equality should produce a no-op rather
  than a duplicate PageRevision.

Expected commit:

```text
feat(pagewiki): add immutable page updates and revision CAS
```

## Slice 5: page links, backlinks, and lexical search

### Acceptance to write first

- injected knowledge is immediately searchable;
- the result identifies Page, current revision, Section, passage, score,
  overlapping citations, exact Source quotes, and links;
- outgoing and incoming Page links are both visible;
- unknown target Page ID rejects publication;
- link ExactText must occur exactly once in its Section;
- old revisions are excluded from current search by default;
- Source backlinks resolve current citing PageRevisions;
- deleting SearchChunk rows and rebuilding gives equivalent search results.

### Implementation notes

- Keep `PageRevision --links_to--> Page` and
  `PageRevision --cites--> SourceRevision` as the only graph relations.
- Generate SearchChunks synchronously in the target transaction for the
  in-memory Slice.
- Index current PageRevisions only.
- Use deterministic lexical token scoring with stable tie breaking in memory.
- SQLite will later replace this implementation with FTS5/BM25.
- Default backlinks should use current citing revisions. Historical backlinks
  can be exposed through an explicit option later.

Expected commit:

```text
feat(pagewiki): derive search and xanadu links
```

## Slice 6: HTTP API and local reader

### Interfaces

```text
POST /sessions/inject
POST /files/inject
GET  /maintenance/runs/:id
GET  /pages/:slug
GET  /pages/:slug/revisions
GET  /pages/:slug/revisions/:revision
GET  /pages/:slug/backlinks
GET  /search
GET  /sources/:revision
GET  /sources/:revision/backlinks
GET  /navigation
```

Follow the repository rule that Hertz interfaces originate from Thrift IDL.
Generated transport code must contain no domain logic.

The local reader must consume the same PageRevision query path as agents. It
must show topic navigation, article Markdown, exact evidence, revision history,
outgoing links, backlinks, search, and explicit failed/pending run targets.

Write `httptest` contract acceptance before handlers, then browser-level smoke
acceptance for the reader.

Expected commits:

```text
feat(pagewiki): expose page wiki HTTP APIs
feat(pagewiki): add a local page wiki reader
```

## Slice 7: SQLite, then PostgreSQL

Define one repository contract suite and run it against memory, SQLite, and
PostgreSQL adapters. PostgreSQL tests must use PostgreSQL, never SQLite as a
substitute.

SQLite must cover:

- migrations and foreign keys;
- immutable SourceRevision and PageRevision;
- per-target transaction and CAS;
- topic placement;
- citation and link integrity;
- FTS5 current-revision search;
- restart persistence;
- complete SearchChunk rebuild.

Do not begin PostgreSQL until the SQLite adapter passes the same contract suite.

Expected commits:

```text
feat(pagewiki): persist page revisions in sqlite
feat(pagewiki): add postgres repository adapter
```

## Slice 8: DeepSeek planner/editor and blind evaluation

DeepSeek remains behind the existing Planner and Editor ports. Add:

- strict structured output decoding;
- Page/Event ID allowlists;
- input and output token metrics;
- call count and duration;
- retry count, transport/model/validation failure categories;
- raw model output in MaintenanceTarget audit;
- timeouts and deterministic validation after every call.

Use train Sessions as isolated subject worlds unless dataset metadata explicitly
places them in one continuing world. Inject later Sessions from the same world
to test updates. Never put evaluator questions, evaluator answers, or gold
artifacts in planner/editor input.

The live integration gate must verify deterministic properties rather than
exact prose:

- planner returns one to eight briefs;
- every published target validates;
- all citations resolve to exact Source quotes;
- later Sessions update or extend existing Pages;
- no duplicate chronology-only Wiki is created;
- complete call/token/duration/failure metrics are recorded.

Expected commit:

```text
feat(pagewiki): connect deepseek planner and editor
```

## Final acceptance matrix

| Story | Slice |
| --- | ---: |
| One Session creates one coherent cited Page | 2 |
| Citation resolves to exact Source quote | 2 |
| One Session creates multiple Pages | 3 |
| Topic navigation has no Inbox | 3 |
| Failed target preserves successful siblings | 3 |
| Later Session updates an existing Page | 4 |
| Old PageRevision remains immutable | 4 |
| Same injection does not duplicate revisions | 4 |
| Stale base cannot overwrite current revision | 4 |
| Search returns current knowledge and evidence | 5 |
| Page links and backlinks are bidirectional | 5 |
| Forged Event, Page, or exact text is rejected | 2 and 5 |
| Agent, API, and human reader use one revision | 6 |
| Persistence survives restart | 7 |
| Search is rebuildable | 7 |
| Real DeepSeek generates and updates Pages | 8 |
| Calls, duration, tokens, and failures are audited | 8 |

## Reuse boundary

Do not import old LLM Wiki domain code into `internal/pagewiki`.

Potential later reuse is limited to generic infrastructure:

- DeepSeek transport/client behavior;
- local Markdown rendering;
- Git diff/snapshot export;
- session fixture loading;
- experiment reporting.

The old filesystem agent, Workspace page identity, whole-Wiki transaction,
Draft/Proposal models, and any Statement/Knowledge structures are out of scope.
