# Article-first LLM Wiki experiment

Date: 2026-07-25

This experiment tests whether a filesystem-capable DeepSeek maintainer can
produce a more recognizably human Wiki than the earlier person-profile and broad
topic-dossier layout.

It uses the same answer-blind public LoCoMo `conv-26` world as the previous
experiment. Evaluator questions and answers were not placed in the maintenance
workspace.

## Editorial change

New workspaces opt into the `article-first-v1` editorial profile. Markdown and
Git remain authoritative; the profile does not introduce Page, Proposal, or
Draft database aggregates.

The generated `AGENTS.md` now requires:

- one stable reader subject per article;
- typed person, journey, topic, concept, event, timeline, and portal pages;
- a prose lead before the first section;
- summary-style overview and subarticle boundaries;
- current state separated from historical development;
- contextual internal links;
- timelines instead of Session-shaped article bodies;
- a curated home page rather than a flat file catalog;
- a page-creation threshold to avoid one-paragraph fragments;
- `Related pages` as the final H2 when present.

Established pages cannot be replaced by `write_file` once they exceed the small
scaffold limit. The agent must use an exact `replace_text` operation, which
checks the expected occurrence count and citation validity before preserving the
new bytes. Git-base shrink and bulk-deletion gates remain in force.

The deterministic validator additionally rejects:

- missing or unsupported article types;
- missing or multiple H1 headings;
- pages without a prose lead;
- Session chronology headings outside timeline views;
- evidence articles without an exact Source citation;
- evidence articles without a contextual Wiki link;
- substantial exactly duplicated prose across articles;
- pages more than three links from `wiki/index.md`;
- article content appended after `Related pages`.

The human viewer hides frontmatter and joins normal Markdown soft wraps so the
rendered home page and list descriptions read as articles rather than source
files.

## Phase 1: Sessions 1 through 10

DeepSeek created an article-oriented Wiki with:

- person pages for Caroline and Melanie;
- journey pages for Caroline's adoption, counseling career, and transition;
- one cross-person LGBTQ advocacy topic;
- one chronological reference page;
- one home page organized into People, Journeys, Topics, and Reference.

The adoption material is no longer mixed with generic family-building prose and
repeated encouragement. It has a stable journey page with motivation, agency
selection, council meeting, preparation, and related-page navigation.

| Metric | Earlier topic-dossier Phase 1 | Article-first Phase 1 |
| --- | ---: | ---: |
| Model calls | 22 | 17 |
| Tool calls | 32 | 39 |
| Duration | 205,366 ms | 176,391 ms |
| Input tokens | 693,077 | 593,896 |
| Output tokens | 17,256 | 14,162 |
| Validator Markdown files, excluding log | 10 | 8 |
| Exact Source citations | 159 | 96 |

The lower page and citation count reflects synthesis rather than loss of Source
coverage: repeated conversational reinforcement is summarized instead of copied
into several broad topic buckets.

The immutable seed revision was
`db7922f2791ba21db1018c9eb6c3a52aadf48732`. The valid Phase 1 snapshot is
`14c0f18b020be1915f2e40cf7a167a8d0dc683e3`.

## Phase 2: Sessions 11 through 19

The same Wiki then received nine later Sessions containing 204 messages.
DeepSeek updated every existing evidence article rather than creating a second
Wiki:

- the adoption lead changed from research-stage to passed agency interviews;
- the adoption journey gained application, advice-group, mentor, and interview
  milestones;
- the counseling journey gained ongoing youth-center volunteering;
- transition and advocacy pages integrated later art, poetry, youth work,
  public encounters, and relationship changes;
- both person pages integrated later supported facts;
- the timeline extended from July through October;
- the home page advanced its coverage window from July to October.

The primary incremental run used:

| Metric | Value |
| --- | ---: |
| Model calls | 29 |
| Tool calls | 54 |
| Duration | 228,138 ms |
| Input tokens | 1,890,887 |
| Output tokens | 20,412 |

Human diff inspection found two pages with new sections appended after their
`Related pages` section. A new deterministic rule rejected that layout. A
DeepSeek-only repair used 8 calls, 22 tools, 36,428 ms, 158,274 input tokens,
and 3,266 output tokens to move both navigation sections to the end without
changing the supported facts.

The final Phase 2 validation has:

- 8 Markdown pages excluding the maintenance log;
- 193 exact Source citations;
- zero broken internal links;
- zero unknown Source anchors;
- zero unreachable articles;
- zero article-contract errors;
- all 19 Source hashes unchanged.

The Wiki-only Phase 1 to Phase 2 diff contains 397 inserted and 44 deleted lines.
All eight published pages changed; no new Wiki page was created. The complete
Phase 2 snapshot is
`bb4081eb536e30535a42898d73f458cb68eae91d`.

## Publication and reader checks

Both snapshots were published with expected-base CAS. An attempt to publish the
Phase 1 workspace again against the obsolete seed base was rejected because the
store HEAD was already Phase 2.

Rollback from Phase 2 to Phase 1 succeeded, after which the Phase 2 snapshot was
republished against the restored Phase 1 base. This left canonical HEAD on the
valid Phase 2 snapshot.

The local browser walkthrough verified:

1. the home page shows curated People, Journeys, Topics, and Reference paths;
2. the adoption page exposes the complete evolving journey;
3. the October interview milestone is visible in the existing adoption page;
4. clicking its citation opens the immutable Session 19 Source;
5. the browser lands on exact anchor `msg-63f1bd42f0f7a2ed`;
6. the anchored Source message contains the cited interview result.

Full Wiki diffs are generated as:

- `/tmp/pax-llmwiki-article-phase1.diff`;
- `/tmp/pax-llmwiki-article-phase2.diff`.

The local Phase 2 workspace is
`/tmp/pax-llmwiki-article-phase2.VlkcBz`, and its viewer is served at
<http://127.0.0.1:18093/>.

## Remaining limits

This is more article-like and incrementally stable, but not a complete Wiki
platform:

- the home page is curated but has no derived recent-changes or backlink panel;
- Melanie's creative work, injury, and recovery remain sections of her person
  page rather than a separate journey;
- exact citation resolution is deterministic, while full claim-to-message
  semantic entailment still requires reader evaluation;
- the long timeline is useful as a reference but should not become the default
  reading surface as the corpus grows;
- lexical search and derived backlinks remain future, rebuildable indexes.

The next quality decision should be based on blind reader tasks and navigation
behavior, not on a single opaque “Wiki score.”
