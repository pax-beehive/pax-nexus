# Public Session workspace experiment

Date: 2026-07-24

This experiment replaced private native Session data with the pinned,
answer-blind public corpora under `.build/datasets/llmwiki`.

## Correct isolation unit

The train split is not one Wiki.

| Dataset | Train structure | Correct Wiki boundary |
| --- | --- | --- |
| LoCoMo | 3 conversations; 19–30 Sessions and 419–681 turns each | One Wiki per conversation |
| LongMemEval-S | 40 distinct case haystacks; 43–55 Sessions each | One Wiki per case |
| LongMemEval-V2 | 28 questions over 2 shared 100-trajectory haystacks | One Wiki per environment/haystack |

LoCoMo conversations are intentionally multi-topic. That is useful: a Wiki
should turn one continuous participant world into several linked topic pages.
Combining different conversations, users, tenants, cases, or environments would
create cross-world contamination and invalid aggregate metrics.

The public adapter accepts only one explicit `case_id` and reads only
`maintainer/ingest.jsonl`. Its strict decoder rejects evaluator-only fields such
as `answer`.

## LoCoMo `conv-26`

The shortest complete LoCoMo train world was selected:

- 2 participants;
- 19 Sessions from May through October 2023;
- 419 dialogue messages;
- phase 1: Sessions 1–10, 215 messages;
- phase 2: Sessions 11–19, 204 messages.

No reader questions, gold answers, generated summaries, or evaluator evidence
were placed in the maintainer workspace.

The evaluator contains 152 scored questions for this world. Eighty-one have
all annotated evidence inside phase 1 and can form a phase-1 reader slice
without relying on unseen later Sessions. Questions and gold must still be
joined only after maintenance; they must not be merged into the Wiki workspace.

### Phase 1

`deepseek-v4-pro` produced a valid Wiki:

| Metric | Value |
| --- | ---: |
| Model calls | 22 |
| Tool calls | 32 |
| Duration | 205,366 ms |
| Input tokens | 693,077 |
| Output tokens | 17,256 |
| Wiki Markdown files | 10 |
| Citations | 159 |

The topic tree contained two person pages and seven topic pages: LGBTQ+
advocacy, mental health and counseling, adoption, creative arts, family and
parenting, self-care, and transition and personal growth. The result was
organized by durable subjects rather than by Session. All Source hashes were
unchanged. Snapshot `638ea7685523fed499cef3404579ce316fbcefb5` was published
over base `6416f1d789572a21c70d51ea530ed06de2b5625f`.

### Phase 2

The incremental run modified every existing person and topic page instead of
creating a second Wiki. It expanded existing subjects with later developments,
including adoption progress, additional creative practices, family activities,
advocacy, and personal growth.

However, the run exhausted 30 rounds with 18 malformed citation anchors:

| Metric | Value |
| --- | ---: |
| Model calls | 30 |
| Tool calls | 68 |
| Duration | 384,129 ms |
| Input tokens | 2,216,315 |
| Output tokens | 38,309 |
| Candidate citations | 248 |
| Validator errors | 18 |

The deterministic validator rejected the snapshot, so canonical HEAD remained
on phase 1. A citation-only DeepSeek repair run used another 10 model calls and
40 tool calls. It fixed one error but still left 17, so it was also rejected.
Codex did not repair the Markdown manually.

### Guarded retry

The experiment exposed two missing deterministic boundaries:

1. malformed citations were accepted by `write_file` and discovered only at
   final validation;
2. the validator checked links and citations but did not reject severe content
   deletion relative to the Git base.

Citation preflight was added so a malformed or unknown Source reference cannot
replace the last valid page. A clean phase-2 retry then reached link/citation
validity:

| Metric | Value |
| --- | ---: |
| Model calls | 30 |
| Tool calls | 50 |
| Duration | 318,019 ms |
| Input tokens | 1,861,564 |
| Output tokens | 32,786 |
| Syntactically valid citations | 128 |

Human diff inspection still found unacceptable degeneration: `caroline.md`
became a 105-byte test placeholder and `melanie.md` became a 267-byte A/B/C
placeholder. They had been 3,456 and 4,003 bytes at the published base.

A Git-base destructive-change gate was therefore added. It rejects major-page
shrink greater than two thirds and bulk deletion of existing major pages. With
that gate, the guarded retry is invalid and remains unpublished. All 19 public
Source files retained their original hashes throughout.

## Conclusion

The concern about train-wide mixing is correct. Evaluation must be aggregated
across isolated Wiki worlds, never implemented as one Wiki containing the
whole train split.

Multi-topic Sessions inside one world are appropriate. Phase 1 demonstrated
that the Agent can create a coherent topic tree from them. Phase 2 attempted
updates to the existing tree, but exposed both citation-writing failure and
content degeneration under a long edit loop. Citation preflight and
destructive-change gates now fail closed; no phase-2 candidate was published.

The next engineering slice should make incremental maintenance preserve page
content by construction, then run query-time LoCoMo/LongMemEval readers against
isolated holdout gold. A validator passing structural checks alone is not an
effectiveness result.

The follow-up article-first experiment, including a successful incremental
Phase 2, is recorded in
[`llmwiki-article-first-experiment-2026-07-25.md`](llmwiki-article-first-experiment-2026-07-25.md).
