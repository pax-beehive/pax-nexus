# Team Ontology — Design

Date: 2026-08-01
Status: approved (sections reviewed interactively with the owner)
Scope: long-term direction + Phase 1 detailed design. Phases 2 and 3 get
their own specs when they start.

## Goal

Evolve PAX Nexus into a substrate that accumulates a team ontology: typed
entities, typed relations, and evidence-grounded provenance, extracted
automatically from session evidence with zero manual entry.

## Settled decisions

1. **Asset first.** The ontology is built as a durable data asset before any
   consumer (agent grounding, human browsing, query API) is designed for it.
2. **Small fixed core + emergent extension.** A hand-picked seed schema
   ships first; the LLM may later nominate candidate types that are promoted
   through a Curator-style consolidation gate (Phase 2).
3. **Type the existing substrate.** PageWiki is the ontology: `Page` gains
   `EntityType`, Xanadu links gain `RelationType`. No parallel entity store,
   no second source of truth, reset-rebuild semantics unchanged.
4. **Phase 1 is typed entities + typed relations only.** No claims /
   property assertions until real type distribution has been observed.

## Why PageWiki is already a proto-ontology

Concept-noun page titles (2026-07-31 concept-titles spec), exact-text Xanadu
links, the topic tree hierarchy, citation-level provenance, and the Curator's
merge/retire/contradiction lanes are respectively: concepts, edges,
hierarchy, grounding, and ontology maintenance. The missing pieces are types
and, later, queryability. This design adds types.

## Phase 1 — type skeleton

### Seed schema

Entity types (5):

| type | covers |
|---|---|
| `person` | teammates, external collaborators |
| `system` | services, repos, tools, infrastructure |
| `decision` | a made choice and its rationale (ADR-shaped) |
| `convention` | agreed practices, rules, processes |
| `concept` | domain terms, designs, ideas — **fallback default** |

Relation types (6): `owns`, `depends-on`, `part-of`, `supersedes`,
`affects`, `relates-to` (**fallback default**; the semantics of today's
untyped links).

Design rules:

- **Fallback types make typing total.** An absent, unrecognized, or invalid
  type degrades to `concept` / `relates-to` with a log line. Typing must
  never fail a target or add a new partial_success source. Legacy rows with
  empty type columns read as the fallbacks; real types arrive through
  routine reset-rebuild, not a dedicated backfill pipeline.
- **Type registry is data from day one.** New table
  `pagewiki_type_registry` (kind: `entity`|`relation`, name, description,
  status: `seed`|`candidate`|`active`|`retired`). Phase 1 writes seed rows
  and reads them for validation; Phase 2's emergence adds rows instead of
  migrating constants.
- **Domain/range is prompt guidance, not enforcement.** "owns usually runs
  person → system" lives in the planner prompt. Violations are not blocked
  at write time; the Phase 2 Curator lane may flag them.

### Write path

- **Planner assigns entity types.** `PageBrief.EntityType`, chosen where
  raw evidence and the catalog are both visible. The planner prompt carries
  one-line criteria per seed type. Output is validated against the
  registry; unknown → `concept` + log. `PageCatalog` exposes existing
  pages' types so prior typing informs new planning.
- **Planner assigns relation types.** The existing control structure stays:
  planner emits related slugs → deterministic editor code renders the
  Related knowledge section. The planner adds `relation_type` per related
  slug; `LinkDraft`/`PageLink` gain `RelationType`; deterministic code
  persists it. The editor LLM never touches types — it writes prose only.
- **Storage.** `Page.EntityType`, `PageLink.RelationType`, the registry
  table; memory + postgres via the existing hydration pattern.
- **Curator guard.** Deterministic rule: pages of different entity types
  never form a merge candidate pair. First real consumer of the type asset
  and its quality probe.
- **UI (minimal visibility).** Entity-type badge on the page header; typed
  edge labels in the Related knowledge / links panel. No type-based
  browsing or filtering (Phase 3).

### Acceptance

1. Coverage: after a reset-rebuild, >90% of pages carry a non-fallback
   entity type.
2. Rebuild stability: two rebuilds over the same corpus differ in type
   distribution by <10%.
3. Curator merge precision does not regress (the type guard should only
   raise it).

## Phase 2 — emergence and governance (direction only)

The LLM may nominate candidate entity/relation types with evidence;
candidates accumulate in the registry. Promotion requires appearing on ≥N
pages across ≥M sessions plus a skeptic-verify pass; shrinking types are
merged or retired by the Curator. Deterministic code controls promotion,
the LLM judges, a skeptic verifies — the established curation philosophy.

## Phase 3 — query projection (direction only)

A derived, rebuildable graph projection over typed pages/links with a query
surface (neighbors, paths, by-type listing), wired into recall for agent
grounding, possibly exported (e.g. JSON-LD). Consumers arrive here and
retroactively validate the asset.

## Out of scope

Claims/property assertions, type-based UI navigation, external APIs,
enforced domain/range constraints, any new extraction pipeline.
