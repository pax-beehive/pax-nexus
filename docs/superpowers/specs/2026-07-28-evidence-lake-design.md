# Evidence Lake: generalizing Session Lake into a source-agnostic evidence store

Date: 2026-07-28
Status: draft for review

## Goal

Generalize `internal/sessionlake` from an agent-session-only store into an
Evidence Lake: an immutable, ordered, source-agnostic evidence store that any
external connector (paxm today; IM and email connectors later) feeds through a
unified flat ingest contract. Team Note and PageWiki keep consuming bounded
slices exactly as they do now.

Connectors are separate programs in separate repositories, like paxm. This
repository owns only the ingest contract, storage, ordering, deduplication,
slice construction, and extraction cursors.

## Non-goals (this phase)

- No connector code in this repository.
- No participant-level ACL. Only `team`-visible streams are accepted; the
  contract reserves visibility so private sources (email, DMs) can be added
  later without rewriting stored data.
- No lake-side transcription or media understanding. Connectors deliver the
  text rendering of media; the lake stores the original bytes verbatim.
- No user-facing lake query product. The lake stays internal evidence storage;
  filterable fields exist to serve extraction, diagnostics, and future recall
  work, not a browsing UI.
- No automatic cross-source identity resolution. The mapping is
  administrator-maintained this phase.

## Event contract

One flat event shape for every source. Source-specific structure goes into
registered metadata keys, never into new top-level fields.

```
Stream {
  source    string   // registered source type: "agent-session", "im-channel", ...
  stream_id string   // source-scoped stream identifier (session id, channel id)
}

Author {
  kind      string   // "user" | "agent" | "system"
  native_id string   // required; platform-native identity, stored verbatim
  user_id   string   // optional; resolved installation identity
}

Event {
  id           string            // connector-assigned, dedup key within stream
  stream       Stream
  author       Author
  sequence     int64             // assigned by ingest, monotonic per stream
  kind         string            // modality: "text" | "audio" | "image" | "video" | "file"
  type         string            // registered semantic type: "message", "reply",
                                 // "reaction", "system", "checkpoint", "attachment"
  content      string            // text body, or connector-produced transcript /
                                 // caption / OCR for non-text kinds
  media        *MediaRef         // required when kind != "text"
  thread_ref   string
  visibility   string            // this phase: only "team" is accepted
  occurred_at  time.Time
  captured_at  time.Time
  metadata     map[string]string // non-filterable source-specific detail
}

MediaRef {
  blob_id   string  // returned by the blob upload endpoint
  mime_type string
  size      int64
  checksum  string  // sha256 of the original bytes
}
```

Contract rules:

- **Ordering.** `sequence` is assigned by the lake per stream in ingest order.
  Source ordering is never trusted; backfill and late arrival are normal.
  Deduplication stays keyed on event `id` within a stream.
- **Type registries.** `source`, `kind`, and `type` are closed vocabularies
  maintained in the contract documentation. Ingest rejects unregistered
  values, so filters keep stable semantics instead of degrading into free
  strings.
- **Filterable columns.** `source`, `stream_id`, `author.user_id`,
  `author.native_id`, `kind`, `type`, `thread_ref`, and `occurred_at` are
  indexed columns. `metadata` is an opaque bag and is never queried by the
  lake.
- **Visibility.** Connectors must declare `visibility` on every event. This
  phase ingest accepts only `team` and rejects anything else, so stored data
  never needs cleanup when private sources arrive.
- **Identity.** `author.native_id` is immutable evidence. `author.user_id` is
  resolved at ingest from an administrator-maintained mapping
  (`source + native_id -> user_id`) owned by the On-prem Identity context;
  unresolved authors stay empty and can be backfilled later without touching
  evidence rows. Extraction treats `user_id` as the person and falls back to
  the native id string when unresolved.

## Media handling

Connectors transcribe; the lake stores originals.

- The connector produces the text rendering (ASR transcript, caption, OCR)
  before pushing, and puts it in `content`. The lake and extractors stay free
  of ML dependencies and keep consuming text only.
- The lake exposes a blob upload endpoint. The connector uploads the original
  bytes first, receives a `blob_id`, and references it in `MediaRef`. Events
  with `kind != "text"` and no `media` are rejected, as is a checksum or size
  mismatch.
- Blob storage is a domain port (`BlobStore`) implemented by a platform
  adapter. First adapter is filesystem-backed (a mounted volume fits the
  single-node on-prem deployment); S3/MinIO is a later adapter behind the same
  port. Blobs are content-addressed by checksum so re-uploads deduplicate.
- Originals are kept verbatim so media can be re-transcribed later with better
  models without asking connectors to replay history.

## Ingest API

- The existing paxm session ingest endpoint stays byte-compatible. Its handler
  maps session batches onto `source = "agent-session"` streams internally, so
  paxm needs no change.
- A new generic stream ingest endpoint accepts event batches for any
  registered source. Receipt semantics are unchanged: accepted count,
  duplicate count, cursor.
- A new blob upload endpoint stores original media and returns `blob_id`.

## Downstream and naming

- Slice construction, extraction cursors, replay detection, and the Team Note
  / PageWiki consumption model are unchanged mechanically; cursor and slice
  keys move from actor to stream.
- Package `internal/sessionlake` becomes `internal/evidencelake`; the
  `internal/session` contracts generalize as above. CONTEXT-MAP.md, the
  Session context documentation, and `internal/architecture/dependencies_test.go`
  are updated to the new boundary. The processor guide
  (`docs/session-lake-processors.md`) is renamed and revised with the new
  extension rules.
- Existing rows migrate in one SQL migration to `source = "agent-session"`
  streams; the migration derives `Stream` and `Author` from the stored actor
  triple.

## Extraction quality: known evidence and acceptance

Measured on GroupMemBench (multi-author finance group chat — the closest proxy
for team chat):

- Bounded diagnostic (2026-07-14, `doc/groupmembench-eval.md`): +35.9%
  relative token-F1 lift from Team Note; strong on user-implicit facts,
  positive on knowledge update and term ambiguity, zero on temporal, weak on
  multi-hop, exact match zero in both arms.
- Full-domain eval v3 sweeps (2026-07-25/26) are all `status: invalid` on the
  `recall_observation` check, so the realistic full-domain setting has no
  valid numbers yet.

Acceptance for new sources is therefore bound to the eval pipeline:

1. Fix the v3 sweep recall-observation failures and establish a valid
   full-domain baseline before any new-source conclusions.
2. IM data must be evaluated through the v3 protocol after ingestion, with
   particular attention to whether the temporal and multi-hop gaps widen on
   real chat noise.
3. Email ingestion requires a dedicated extraction fixture set before rollout;
   no email-shaped evaluation exists today.

## Testing

- Contract tests: ingest-side sequence assignment under out-of-order and
  backfill batches; replay deduplication; rejection of unregistered
  `source`/`kind`/`type`, non-`team` visibility, media events without blobs,
  and checksum mismatches.
- Compatibility tests: the paxm session endpoint maps onto agent-session
  streams byte-compatibly.
- Identity tests: ingest-time resolution, unresolved-author fallback, and
  backfill without evidence mutation.
- Architecture tests continue to enforce dependency direction for the renamed
  context and the new `BlobStore` port.

## Future work (explicitly deferred)

- Participant-level ACL and private sources (email, DMs), including
  visibility inheritance in extracted notes and wiki pages.
- Lake-side re-transcription processors over stored originals.
- Automatic cross-source identity resolution.
- Direct lake query/browse product surface.
