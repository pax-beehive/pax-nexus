# LLM Wiki workspace spike: live results

Date: 2026-07-24

This report records one live, local-first end-to-end run. It is evidence for
the spike mechanics, not a claim that the current editorial prompt is ready for
production.

## Inputs and artifacts

- Native Session: `019f8db1-cc4d-7f71-8770-640fda70d0a4`
- Session slice 1: turns `[0,30)`, 60 messages, 130,107 bytes
- Session slice 2: turns `[30,60)`, 60 messages, 151,163 bytes
- Local artifact root: `/tmp/pax-llmwiki-spike.xoeAcm`
- Bare Wiki snapshot store: `/tmp/pax-llmwiki-spike.xoeAcm/wiki.git`
- Published checkout: `/tmp/pax-llmwiki-spike.xoeAcm/published`
- Local viewer: `http://127.0.0.1:18090/`

The first Source SHA-256 remained
`a2843626cf6a1579d2a7f95ebfa3b9952cbfe3d573b1b948171d9316cb5cefea`
in both workspaces. The second Source SHA-256 was
`e3e6169e92872c6f7a545c207e98ea67ad6fc01d8326256ca184c2ac7f9d84a0`.
The deterministic validator re-hashed every Source against the manifest after
each run.

## Live DeepSeek runs

All Wiki prose below was written by `deepseek-v4-pro` through the bounded
filesystem tools. Codex selected the Session, ran the harness, and inspected
the results; it did not author the Wiki pages.

| Run | Calls | Tool calls | Duration | Input tokens | Output tokens | Validation |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| `native-part-1` | 10 | 28 | 242,389 ms | 477,506 | 19,697 | 9 Markdown files, 84 citations |
| `native-part-2` | 23 | 40 | 364,963 ms | 2,574,317 | 29,037 | 14 Markdown files, 168 citations |
| `pax-gold-v1` | 5 | 21 | 54,629 ms | 22,683 | 5,182 | 8 Markdown files, 20 citations |

No run recorded a failure reason. Audit JSON is stored under each workspace's
`.pax/runs/` directory.

## Incremental behavior and snapshots

The canonical revisions are:

1. `64926323b03655816cc95c4032746944f53aca29` — initial Wiki
2. `341238cc645b65bca243ae2a5eed819bd756a94b` — Session turns 1–30
3. `9329791ae1445f8bcff5c3f76e1bd14dc8736911` — Session turns 31–60

The second run updated `wiki/index.md`, `wiki/log.md`,
`live-e2e-pipeline.md`, and `maintainer-automation-boundaries.md`. It also
added five pages covering retrieval architecture, evaluation, the local-first
direction, benchmark datasets, and architecture decoupling. The Wiki-only
incremental diff is 838 lines. This is an update and expansion of the existing
topic tree, not a second Session transcript.

The stale checkout based on the initial revision was rejected after run 1 had
published:

```text
stale base revision: expected 64926323b03655816cc95c4032746944f53aca29,
current 341238cc645b65bca243ae2a5eed819bd756a94b
```

The publisher was then rolled back from run 2 to run 1 and published forward
to run 2 again. Final canonical HEAD is
`9329791ae1445f8bcff5c3f76e1bd14dc8736911`.

Inspect the complete Wiki diff with:

```bash
/tmp/pax-llmwiki-spike.xoeAcm/llmwiki-spike diff \
  --repo /tmp/pax-llmwiki-spike.xoeAcm/published \
  --base 341238cc645b65bca243ae2a5eed819bd756a94b \
  --revision 9329791ae1445f8bcff5c3f76e1bd14dc8736911
```

## Human viewer acceptance

The browser rendered a topic tree grouped into Architecture, Product, Agent
Retrieval, Evaluation, and Delivery. A topic link opened its Markdown page.
A citation on `local-first-wiki-direction.md` navigated to:

```text
/sources/019f8db1-cc4d-7f71-8770-640fda70d0a4-turns-0031-0060.md
  #msg-a0212991ffc0c790
```

The target anchor existed in the rendered Source page. The final workspace
validator reported 14 Markdown files, 168 citations, no broken links, no
missing citation anchors, and no unreachable major pages.

## Isolated effect comparison

`eval-prepare` copied only seven immutable Source files plus maintainer rules
into the Agent workspace. Questions, expected answers, and evaluator fields
remained in the fixture outside that workspace. `eval-score` read them only
after maintenance completed.

The five-case result was:

| Arm | Hits | Notes |
| --- | ---: | --- |
| Raw Source search | 5/5 | 3,319 source bytes |
| Generated Wiki | 3/5 | 4,988 Wiki bytes |
| Generated Wiki with a resolving citation | 3/5 | Every Wiki hit was grounded |

The Wiki passed the single-session, preference, and multi-session categories.
It missed knowledge-update and temporal-reasoning. The current editorial
instruction therefore loses recall on this tiny fixture even though its
successful answers are grounded. The next iteration should improve treatment
of superseded facts and time-dependent statements before expanding the
platform.

## Verification

- `make lint`: zero issues.
- New-package coverage:
  - `internal/llmwiki/workspace`: 80.0%
  - `internal/llmwiki/effecteval`: 82.9%
  - `cmd/llmwiki-spike`: 80.5%
- Repository coverage run: 80.2%.
- Script tests and explicit PostgreSQL integration packages passed.
- The existing `make integration-test` target does not honor the overridden
  PostgreSQL port because its shell expression is single-quoted; the equivalent
  explicit package command passed against the isolated database.

