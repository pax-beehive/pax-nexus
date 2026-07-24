# Local-first LLM Wiki workspace spike

This spike tests one narrow claim: a filesystem-capable DeepSeek maintainer can
turn immutable native Session evidence into a coherent, cited Markdown Wiki,
then update that Wiki from later evidence without introducing a database editing
model.

It intentionally does not restore the old PageRevision, Draft, Proposal,
structured patching, or embedding abstractions.

## Runtime shape

```text
paxm native Session export
        |
        v
private workspace
  AGENTS.md
  sources/*.md        immutable, stable message anchors
  wiki/*.md           the only Agent-writable files
  .pax/base.json      private expected base
  .pax/manifest.json  source hashes and anchor audit
  .pax/runs/*.json    model calls, latency, tokens, failure
        |
        v
deterministic validator
        |
        v
Git commit -> atomic update-ref CAS -> canonical HEAD
        |
        v
local HTML viewer
```

The bare Git store and its `main` ref are the Wiki authority. A private
workspace can spend minutes in a model run without holding a canonical lock.
Publication changes the canonical ref only with:

```text
git update-ref refs/heads/main <new> <expected-base>
```

Git rejects a stale expected value atomically. Rollback uses the same operation
to move HEAD to a verified ancestor; the displaced commit remains inspectable.

## Old checkpoint reuse boundary

The checkpoint `abdba93` was inspected read-only. Two narrow assets were useful:

- the `paxm backfill export` native completed-turn schema and its stable native
  turn IDs informed the Source importer;
- its pinned public Session dataset preparation and role-separated
  `maintainer/reader/evaluator` contract informed the public dataset adapter.

None of the old PageRevision, maintenance proposal, typed page, PostgreSQL
editing, organizer, embedding, or per-page publication code was migrated.
The public adapter reads only `maintainer/ingest.jsonl`, rejects unknown fields
such as evaluator answers, and requires one explicit dataset `case_id` per
workspace.

## Commands

Build the CLI:

```bash
go build -o /tmp/llmwiki-spike ./cmd/llmwiki-spike
```

Export the first part of a native Session into a seed workspace:

```bash
/tmp/llmwiki-spike build \
  --workspace /tmp/wiki-seed \
  --session <native-session-id> \
  --start 0 \
  --end 30
```

For repeatable experiments, prefer one isolated world from the prepared public
Session datasets:

```bash
/tmp/llmwiki-spike dataset-build \
  --ingest .build/datasets/llmwiki/prepared/train/locomo/maintainer/ingest.jsonl \
  --workspace /tmp/wiki-locomo-conv-26 \
  --case conv-26 \
  --start-session 0 \
  --end-session 10
```

Do not combine different LoCoMo conversations or different LongMemEval cases
into one Wiki. Multiple topics inside one continuous conversation world are
expected and are precisely what the Wiki should organize.

Create the canonical store and a private checkout:

```bash
/tmp/llmwiki-spike init-store \
  --workspace /tmp/wiki-seed \
  --store /tmp/wiki.git

/tmp/llmwiki-spike checkout \
  --store /tmp/wiki.git \
  --workspace /tmp/wiki-run-1
```

Run the actual maintainer:

```bash
export DEEPSEEK_API_KEY='<local secret>'

/tmp/llmwiki-spike run \
  --workspace /tmp/wiki-run-1 \
  --run-id native-part-1 \
  --model deepseek-v4-pro
```

Validate, snapshot, inspect the complete Wiki diff, and publish with CAS:

```bash
/tmp/llmwiki-spike validate --workspace /tmp/wiki-run-1

REVISION=$(/tmp/llmwiki-spike commit \
  --workspace /tmp/wiki-run-1 \
  --message 'Maintain Wiki from native Session turns 1-30')

BASE=$(jq -r .revision /tmp/wiki-run-1/.pax/base.json)

/tmp/llmwiki-spike diff \
  --repo /tmp/wiki-run-1 \
  --base "${BASE}" \
  --revision "${REVISION}"

/tmp/llmwiki-spike publish \
  --store /tmp/wiki.git \
  --workspace /tmp/wiki-run-1 \
  --base "${BASE}" \
  --revision "${REVISION}"
```

For the second maintenance pass, check out canonical HEAD, import the subsequent
turn range into that checkout, and run the same maintenance command. The Agent
sees both the existing Wiki and the new immutable Source:

```bash
/tmp/llmwiki-spike checkout \
  --store /tmp/wiki.git \
  --workspace /tmp/wiki-run-2

/tmp/llmwiki-spike build \
  --workspace /tmp/wiki-run-2 \
  --session <native-session-id> \
  --start 30 \
  --end 60
```

Serve a published checkout for direct human acceptance:

```bash
/tmp/llmwiki-spike serve \
  --workspace /tmp/wiki-run-2 \
  --addr 127.0.0.1:8090
```

The root page always shows the topic tree. Wiki citations link to rendered
immutable Source pages and jump to the exact native message anchor.

## Deterministic gates

`validate` fails closed when:

- a Source byte hash or read-only mode changed;
- an internal Markdown link does not resolve;
- a Source citation omits an anchor or points to an unknown anchor;
- a major Markdown page is not reachable from `wiki/index.md`;
- the topic tree itself is missing.

The Agent tool sandbox independently forbids writes outside `wiki/*.md`,
absolute paths, path traversal, and symlink escapes. The validator then checks
the completed filesystem state rather than trusting the model's final message.

## Evaluation boundary

The maintainer receives only public `maintainer/ingest.jsonl`. Query-time
readers receive `reader/query.jsonl` after Wiki maintenance. Only the evaluator
process can read `evaluator/gold.jsonl`.

The old five-pattern local scorer is retained only as unit-tested plumbing. It
is not an effect benchmark. Product claims must come from the prepared LoCoMo,
LongMemEval, or LongMemEval-V2 train/holdout protocols and their official
reader/evaluator semantics.
