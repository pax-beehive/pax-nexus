# Team Ontology Phase 1 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Type the PageWiki substrate — `Page.EntityType`, `PageLink.RelationType`, a seeded type registry — assigned by the planner at generation time, guarded in the curator, visible in API and UI.

**Architecture:** The wiki IS the ontology: no new extraction pipeline, no second source of truth. Types are chosen by the LLM planner (the only spot that sees raw evidence + catalog), normalized against a data-backed registry with total fallbacks (`concept` / `relates-to`), persisted through the existing publication payloads (JSON — no page/link column migrations), and consumed first by a deterministic curator merge guard.

**Tech Stack:** Go (internal/pagewiki, internal/platform/postgres), thrift IDL codegen (`make generate`), React+vitest (web/).

**Spec:** `docs/superpowers/specs/2026-08-01-team-ontology-design.md`

## Global Constraints

- Branch: `feat/wiki-ontology-phase1` (stacked on `chore/pagewiki-llm-maintainability`).
- Typing must never fail a target: absent/unknown types degrade to `concept` / `relates-to`, never error.
- The editor LLM never sees or emits types; only the planner does.
- Seed entity types: `person`, `system`, `decision`, `convention`, `concept` (fallback). Seed relation types: `owns`, `depends-on`, `part-of`, `supersedes`, `affects`, `relates-to` (fallback).
- Domain/range is prompt guidance only — never enforced in code.
- Every task: gofmt style of surrounding code, `golangci-lint run ./internal/pagewiki/... ./internal/platform/...` clean, package tests green before commit.
- Commit messages end with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

---

### Task 1: Domain types + registry + memory repository seeding

**Files:**
- Modify: `internal/pagewiki/types.go` (Page, PageCatalogEntry, PageLink, PageBrief, RelatedPage, LinkDraft)
- Create: `internal/pagewiki/type_registry.go`
- Create: `internal/pagewiki/type_registry_test.go`
- Modify: `internal/pagewiki/ports.go` (Repository interface)
- Modify: `internal/pagewiki/memory/repository.go`
- Test: `internal/pagewiki/memory/repository_test.go`

**Interfaces (Produces):**
```go
// types.go additions
type EntityType string
type RelationType string
const (
    EntityTypePerson     EntityType = "person"
    EntityTypeSystem     EntityType = "system"
    EntityTypeDecision   EntityType = "decision"
    EntityTypeConvention EntityType = "convention"
    EntityTypeConcept    EntityType = "concept" // fallback
)
const (
    RelationTypeOwns      RelationType = "owns"
    RelationTypeDependsOn RelationType = "depends-on"
    RelationTypePartOf    RelationType = "part-of"
    RelationTypeSupersedes RelationType = "supersedes"
    RelationTypeAffects   RelationType = "affects"
    RelationTypeRelatesTo RelationType = "relates-to" // fallback
)
// Field additions:
//   Page.EntityType EntityType            PageCatalogEntry.EntityType EntityType
//   PageLink.RelationType RelationType    PageBrief.EntityType EntityType
//   RelatedPage.Relation RelationType     LinkDraft.RelationType RelationType

// type_registry.go
type TypeKind string   // "entity" | "relation"
const (
    TypeKindEntity   TypeKind = "entity"
    TypeKindRelation TypeKind = "relation"
)
type TypeStatus string // "seed" | "candidate" | "active" | "retired"
const (
    TypeStatusSeed      TypeStatus = "seed"
    TypeStatusCandidate TypeStatus = "candidate"
    TypeStatusActive    TypeStatus = "active"
    TypeStatusRetired   TypeStatus = "retired"
)
type TypeRegistryEntry struct {
    Kind        TypeKind
    Name        string
    Description string // one-line criterion, feeds the planner prompt
    Status      TypeStatus
}
func SeedTypeRegistryEntries() []TypeRegistryEntry // 5 entity + 6 relation rows, Status "seed", with descriptions
type TypeRegistry struct{ /* private maps */ }
func NewTypeRegistry(entries []TypeRegistryEntry) TypeRegistry
// Normalize: trim+lower; registered with status seed|active → typed value; anything else → fallback.
func (r TypeRegistry) NormalizeEntity(value string) EntityType
func (r TypeRegistry) NormalizeRelation(value string) RelationType
func (r TypeRegistry) Entities() []TypeRegistryEntry  // stable order, for prompts
func (r TypeRegistry) Relations() []TypeRegistryEntry

// ports.go Repository additions
TypeRegistry(context.Context) ([]TypeRegistryEntry, error)
SaveTypeRegistryEntry(context.Context, TypeRegistryEntry) error // upsert by (Kind, Name)
```
Memory repository: `NewRepository()` seeds `SeedTypeRegistryEntries()`; `SaveTypeRegistryEntry` upserts; `TypeRegistry` returns a copy sorted by (Kind, Name).

- [ ] **Step 1: failing tests** — in `type_registry_test.go`:
```go
func (s *TypeRegistrySuite) TestNormalizeFallsBackForUnknownValues() {
    registry := pagewiki.NewTypeRegistry(pagewiki.SeedTypeRegistryEntries())
    s.Equal(pagewiki.EntityTypeSystem, registry.NormalizeEntity(" System "))
    s.Equal(pagewiki.EntityTypeConcept, registry.NormalizeEntity("galaxy"))
    s.Equal(pagewiki.EntityTypeConcept, registry.NormalizeEntity(""))
    s.Equal(pagewiki.RelationTypeOwns, registry.NormalizeRelation("owns"))
    s.Equal(pagewiki.RelationTypeRelatesTo, registry.NormalizeRelation("bogus"))
}
func (s *TypeRegistrySuite) TestRetiredTypesFallBack() {
    entries := append(pagewiki.SeedTypeRegistryEntries(), pagewiki.TypeRegistryEntry{
        Kind: pagewiki.TypeKindEntity, Name: "incident", Status: pagewiki.TypeStatusRetired,
    })
    registry := pagewiki.NewTypeRegistry(entries)
    s.Equal(pagewiki.EntityTypeConcept, registry.NormalizeEntity("incident"))
}
```
   And in `memory/repository_test.go`: `TestTypeRegistrySeededAndUpsertable` — fresh repo returns 11 seed rows; SaveTypeRegistryEntry with same (kind,name) overwrites; new name adds.
- [ ] **Step 2: run, verify FAIL** (`go test ./internal/pagewiki/ ./internal/pagewiki/memory/ -run 'TypeRegistry'` — undefined symbols).
- [ ] **Step 3: implement** types.go fields + type_registry.go + ports.go + memory repo (seed in NewRepository; guard map with existing mu).
- [ ] **Step 4: run, verify PASS**; run full `go test ./internal/pagewiki/...` (field additions are zero-value compatible; nothing else should break).
- [ ] **Step 5: commit** `feat(pagewiki): entity/relation types and seeded type registry`

### Task 2: Postgres registry table + hydration

**Files:**
- Create: `internal/platform/postgres/migrations/027_pagewiki_type_registry.sql`
- Modify: `internal/pagewiki/postgres/repository.go` (hydrate + TypeRegistry + SaveTypeRegistryEntry)
- Test: `internal/pagewiki/postgres/repository_test.go`

**Interfaces (Consumes):** Task 1's port methods and memory upsert.

Migration:
```sql
CREATE TABLE IF NOT EXISTS pagewiki_type_registry (
    scope_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (scope_id, kind, name)
);
```
Postgres repo: `SaveTypeRegistryEntry` = memory upsert + `INSERT ... ON CONFLICT (scope_id, kind, name) DO UPDATE SET payload = EXCLUDED.payload`; hydrate loads rows after seeds (DB rows override same-name seeds — Phase 2 candidates/retirements survive restarts). `TypeRegistry` delegates to memory.

- [ ] **Step 1: failing test** — `TestTypeRegistryEntrySurvivesRehydration`: save `{entity,incident,candidate}` entry, reload repository (`NewRepository` again), assert 12 rows and the incident row present with status candidate.
- [ ] **Step 2: verify FAIL** with DSN (`make db-up`; `TEAM_MEMORY_TEST_POSTGRES_DSN='postgres://team_memory:team_memory@127.0.0.1:55432/team_memory?sslmode=disable' go test ./internal/pagewiki/postgres/ -run Rehydration`).
- [ ] **Step 3: implement** migration + repo methods + hydrate step (follow the `pagewiki_curation_runs` loadRows pattern; add the table to repository_test.go TearDownTest DELETE list).
- [ ] **Step 4: verify PASS** (postgres suite with DSN).
- [ ] **Step 5: commit** `feat(pagewiki): persist type registry in postgres`

### Task 3: Planner assigns entity and relation types

**Files:**
- Modify: `internal/pagewiki/llm_session_planner.go`
- Modify: `internal/pagewiki/types.go` (`PlanInput` gains `Types TypeRegistry`)
- Modify: `internal/pagewiki/session_document.go` (SessionDocumentPlanner briefs: `EntityType: EntityTypeConcept`)
- Test: `internal/pagewiki/llm_session_planner_test.go`

**Interfaces (Consumes):** `TypeRegistry.NormalizeEntity/NormalizeRelation/Entities/Relations` (Task 1). **(Produces):** briefs whose `EntityType` and `RelatedPages[].Relation` are always normalized (never empty).

Wire changes in `llm_session_planner.go`:
```go
type llmPlanRelated struct {
    Slug     string `json:"slug"`
    Relation string `json:"relation"`
}
type llmPlanBrief struct {
    // existing fields unchanged, plus:
    EntityType   string           `json:"entity_type"`
    Related      []llmPlanRelated `json:"related,omitempty"`
    RelatedSlugs []string         `json:"related_slugs,omitempty"` // legacy fallback, relation → relates-to
}
type llmPlanPage struct { // catalog view: planner sees existing pages' types
    Slug string `json:"slug"`; Title string `json:"title"`
    Summary string `json:"summary,omitempty"`; EntityType string `json:"entity_type,omitempty"`
}
```
- `acceptBrief` sets `brief.EntityType = input.Types.NormalizeEntity(candidate.EntityType)` for create AND update briefs; `sourceOnlyBrief` gets `EntityTypeConcept`.
- `plannedBrief.relatedSlugs []string` becomes `related []llmPlanRelated` (merge: `candidate.Related` first, then legacy `RelatedSlugs` appended with empty relation); `resolveRelatedPages` sets `RelatedPage.Relation = input.Types.NormalizeRelation(entry.Relation)`.
- Prompt: append a generated vocabulary block — `plannerTypeVocabulary(types TypeRegistry) string` rendering each entity/relation name + description from the registry, plus instructions: every brief carries `entity_type`; related pages use `related: [{"slug","relation"}]`; domain/range guidance lines (`owns usually runs person → system or convention`, etc.). Concatenate like `generationDirectivesPrompt`.

- [ ] **Step 1: failing tests** (extend existing planner suite fakes; construct `PlanInput{Types: NewTypeRegistry(SeedTypeRegistryEntries())}`):
```go
// TestPlannerAssignsNormalizedTypes: scripted LLM response brief with
// "entity_type":"System" and related [{"slug":"sqlite","relation":"depends-on"}]
// (catalog contains sqlite) → brief.EntityType == EntityTypeSystem,
// RelatedPages[0].Relation == RelationTypeDependsOn.
// TestPlannerFallsBackUnknownTypesAndLegacyRelatedSlugs: response with
// "entity_type":"galaxy" and legacy "related_slugs":["sqlite"] →
// EntityTypeConcept + RelationTypeRelatesTo. Prompt (Messages[0]) contains
// "person" and "depends-on" vocabulary lines.
```
- [ ] **Step 2: verify FAIL**.
- [ ] **Step 3: implement** wire types, normalization, vocabulary block, SessionDocumentPlanner concept default.
- [ ] **Step 4: verify PASS**; full pagewiki package green (tests constructing PlanInput without Types still pass — zero-value TypeRegistry normalizes everything to fallbacks; assert that in TestZeroRegistryFallsBackEverything).
- [ ] **Step 5: commit** `feat(pagewiki): planner assigns entity and relation types`

### Task 4: Service persists types through publication and catalog

**Files:**
- Modify: `internal/pagewiki/service.go` (InjectSession loads registry → PlanInput; buildPublication sets page type; buildLinks carries relation)
- Modify: `internal/pagewiki/llm_session_editor.go` (`relatedKnowledgeSection`: LinkDraft.RelationType from RelatedPage.Relation)
- Modify: `internal/pagewiki/memory/repository.go` (PageCatalog fills EntityType)
- Test: `internal/pagewiki/inject_acceptance_test.go`

**Interfaces (Consumes):** Task 1 fields, Task 3 normalized briefs. **(Produces):** persisted `Page.EntityType` / `PageLink.RelationType` for Tasks 5-7.

- `InjectSession`, after loading directives: `entries, err := s.repository.TypeRegistry(ctx)` (error → wrap `load type registry`); `types := NewTypeRegistry(entries)`; pass in `PlanInput{..., Types: types}`.
- `buildPublication` typing rule (explicit): `pageValue.EntityType = brief.EntityType`; if `brief.EntityType` is empty or `EntityTypeConcept` and `page.EntityType` is non-empty → keep `page.EntityType` (an update never downgrades an established type to the fallback); if still empty → `EntityTypeConcept`.
- `relatedKnowledgeSection` copies `page.Relation` into `LinkDraft.RelationType` (empty → `RelationTypeRelatesTo`); `buildLinks` passes `draft.RelationType` into the `PageLink` it constructs (empty → `RelationTypeRelatesTo`).
- Memory repo `PageCatalog` copies `page.EntityType` into `PageCatalogEntry.EntityType`.

- [ ] **Step 1: failing acceptance test** — `TestGivenTypedBriefsWhenInjectedThenTypesPersist`: ScriptedPlanner briefs with `EntityType: EntityTypeSystem` and `RelatedPages: []RelatedPage{{ID:..., Title:..., Relation: RelationTypeDependsOn}}` (two-page setup like multiPageBriefs); after inject assert `repository.PageBySlug(...).EntityType == EntityTypeSystem`, the published revision's link has `RelationType == RelationTypeDependsOn`, catalog entry carries the type, and an untyped ScriptedPlanner brief yields `EntityTypeConcept`/`RelationTypeRelatesTo`.
- [ ] **Step 2: verify FAIL**.
- [ ] **Step 3: implement** the four modification points.
- [ ] **Step 4: verify PASS**; full `go test ./internal/pagewiki/...`.
- [ ] **Step 5: commit** `feat(pagewiki): persist entity and relation types through publication`

### Task 5: Curator merge guard by entity type

**Files:**
- Modify: `internal/pagewiki/curation_candidates.go` (`duplicatePairs`)
- Test: `internal/pagewiki/curation_candidates_test.go`

**Interfaces (Consumes):** `PageCatalogEntry.EntityType` (Task 4).

In `duplicatePairs`, after merging both candidate lanes and before `dedupSortCapPairs`, drop incompatible pairs:
```go
typeByID := make(map[string]EntityType, len(catalog))
for _, entry := range catalog {
    typeByID[entry.ID] = entry.EntityType
}
for key, pair := range candidates {
    if !mergeCompatibleTypes(typeByID[pair.AID], typeByID[pair.BID]) {
        delete(candidates, key)
    }
}
// mergeCompatibleTypes: compatible unless both types are known,
// non-fallback, and different. "" and EntityTypeConcept are wildcards.
func mergeCompatibleTypes(a, b EntityType) bool {
    if a == "" || a == EntityTypeConcept || b == "" || b == EntityTypeConcept {
        return true
    }
    return a == b
}
```
- [ ] **Step 1: failing test** — catalog with a `person` page and a `system` page whose embeddings are identical (similarity 1.0) → `duplicatePairs` returns no pair; same setup with both `system` → pair returned; one side `concept` → pair returned.
- [ ] **Step 2: verify FAIL**. **Step 3: implement.** **Step 4: verify PASS** + curation suites green.
- [ ] **Step 5: commit** `feat(pagewiki): curator never pairs pages of different entity types`

### Task 6: API exposes types

**Files:**
- Modify: `idl/page_wiki.thrift` (Page struct: `8: optional string entity_type`; PageLink struct: next-id `optional string relation_type`; check each struct's current max field id before choosing)
- Regenerate: `make generate` (regenerates `internal/pagewiki/transport/httpapi/model/pagewiki/api/page_wiki.go`)
- Modify: `internal/pagewiki/transport/httpapi/mapping.go` (map `page.EntityType` / `link.RelationType` into the response structs; empty domain values map as the fallback strings so the API never emits "")
- Test: `internal/pagewiki/transport/httpapi/contract_acceptance_test.go`

**Interfaces (Produces):** JSON fields `entity_type` on page payloads, `relation_type` on link payloads — exactly the strings the web client (Task 7) reads.

- [ ] **Step 1: failing test** — extend the existing contract acceptance case that fetches a page with links: assert `entity_type` present and equal to the injected type, links carry `relation_type`.
- [ ] **Step 2: verify FAIL** (field absent). **Step 3:** IDL edit + `make generate` + mapping. **Step 4: verify PASS**; `go build ./...` (codegen churn compiles).
- [ ] **Step 5: commit** `feat(pagewiki): expose entity and relation types in the wiki API` (include regenerated files)

### Task 7: Web UI — type badge and relation labels

**Files:**
- Modify: `web/src/api/wiki.ts` (page type: `entity_type?: string`; link type: `relation_type?: string`)
- Modify: `web/src/pages/WikiBrowsePage.tsx` (badge next to the page title: `<span className="wiki-type-badge">{page.entity_type}</span>` when non-empty and not "concept"; link rows append a muted `({relation_type})` label when non-empty and not "relates-to")
- Modify: `web/src/styles.css` or the stylesheet WikiBrowsePage already uses (one `.wiki-type-badge` rule following existing badge/chip styling)
- Test: `web/tests/wiki-browse.dom.test.tsx`

**Interfaces (Consumes):** Task 6's `entity_type` / `relation_type` JSON fields.

Display rule: fallbacks are invisible — a `concept` page shows no badge, a `relates-to` link shows no label. The ontology surfaces only where it says something.

- [ ] **Step 1: failing DOM test** — fixture page payload with `entity_type:"system"` and a link with `relation_type:"depends-on"` → badge text "system" rendered near the title, link row shows "depends-on"; fixture with `concept`/`relates-to` renders neither.
- [ ] **Step 2: verify FAIL** (`cd web && npx vitest run tests/wiki-browse.dom.test.tsx`). **Step 3: implement.** **Step 4:** vitest file green, then full `npx vitest run`, `npx tsc --noEmit`, `npm run lint`.
- [ ] **Step 5: commit** `feat(web): show entity type badge and relation labels in wiki`

### Task 8: Full verification pass

- [ ] `golangci-lint run ./...` → 0 issues.
- [ ] `TEAM_MEMORY_TEST_POSTGRES_DSN=... go test ./... -count=1` → green.
- [ ] Web: `npx vitest run`, `npx tsc --noEmit`, `npm run lint` → green.
- [ ] Commit any stragglers; push branch; open PR stacked on `chore/pagewiki-llm-maintainability` noting merge order #57 → #58 → #59 → this.

**Operational acceptance (post-merge, on the workstation, not in this plan):** reset-rebuild the corpus; check >90% non-fallback entity-type coverage and <10% type-distribution drift across two rebuilds; watch curator merge behavior.
