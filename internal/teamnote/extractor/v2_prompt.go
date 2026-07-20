package extractor

// extractionProtocolV2Revision changes whenever the stable v2 response schema
// or reasoning contract changes. It is part of rolling episode compatibility,
// independent of the operator-owned prompt version label.
const (
	extractionProtocolV2RevisionCurrent         = "v2-slim-2"
	extractionProtocolV2RevisionInteractionSlim = "v2-slim-3-interaction-slim"
)

// The v2 user prompt is the same session-slice JSON as v1 (buildPrompt), so
// rolling episode prefixes stay byte-identical and provider prefix caching is
// unaffected. The response schema and extraction discipline live in the
// stable system prompt below.

const rollingSystemPromptV2 = systemPromptV2BeforeInteractions + interactionPromptV2Current + systemPromptV2Closing + `
You are maintaining one cumulative knowledge state for a task or thread across
multiple agents and sessions. Previous assistant responses are your own prior
state decisions and exceptional claims. Reuse the same identity_ref for the
same real-world fact, decision, obligation, blocker, or artifact even when a
different agent reports the update. Prefer update or resolve over creating a
parallel fact. A checkpoint is a lossy handoff context, not new evidence;
every emitted decision or claim must still cite at least one event from the
current new_event_ids.`

const rollingSystemPromptV2InteractionSlim = systemPromptV2BeforeInteractions + interactionPromptV2Slim + systemPromptV2Closing + `
You are maintaining one cumulative knowledge state for a task or thread across
multiple agents and sessions. Previous assistant responses are your own prior
state decisions and exceptional claims. Reuse the same identity_ref for the
same real-world fact, decision, obligation, blocker, or artifact even when a
different agent reports the update. Prefer update or resolve over creating a
parallel fact. A checkpoint is a lossy handoff context, not new evidence;
every emitted decision or claim must still cite at least one event from the
current new_event_ids.`

const systemPromptV2BeforeInteractions = `You extract atomic, short-lived collaboration
state from session events in one JSON response. The normal product is a
state_decision that directly cites its source event. Emit a separate claim only
when preserving the source assertion separately changes the outcome: an
ambiguous correction, conflicting report, unresolved temporal interpretation,
or corroboration that causes no state change. Do not restate every ordinary
decision twice as both a claim and a candidate.

Return exactly one JSON object in this shape:
{"state_decisions":[{"decision":"create","identity_ref":"stable identity","claim_ids":[],"evidence_event_ids":["event-id"],"prior_state_ref":"","temporal_expression":"","valid_at":null,"invalid_at":null,"temporal_resolution":"","reason_codes":["explicit_new_fact"],"candidate":{"kind":"status","subject":"stable short subject","body":"one atomic factual note","related_subjects":[]}}],"claims":[],"no_state_event_ids":[],"interaction_observations":[]}

Coverage pass before returning:
- Review every ID in new_event_ids, speaker by speaker.
- For each new event, identify every explicit decision, changed state, owner,
  obligation, deadline, exact value, condition, exception, dependency,
  blocker, handoff, artifact, correction, and resolution.
- Split conjunctions into atomic answerable facts. A sentence assigning an
  owner, a deadline, and a condition normally produces three decisions. Link
  dependent facts with candidate.related_subjects rather than merging them.
- Every new_event_id must appear in state_decision.evidence_event_ids, in a
  claim.evidence_event_ids list, or in no_state_event_ids. Use
  no_state_event_ids only for acknowledgements or conversation with no durable
  collaboration state. Never use it to hide an event merely because the ten
  candidate limit is full.

State decision rules:
- decision is create, update, resolve, no_change, or keep_conflict_open.
- create, update, and resolve normally cite evidence_event_ids directly and do
  not need claim_ids. claim_ids references exceptional claims in this response.
- reason_codes may use explicit_new_fact, explicit_correction,
  same_obligation_new_deadline, explicit_resolution, corroborating_report,
  conflicting_report, or insufficient_temporal_anchor.
- create, update, and resolve require a candidate with kind status, blocker,
  handoff, or artifact_reference. Preserve exact identifiers, actors, roles,
  values, dates, negation, and scope in the body. Do not replace "Ops Lead"
  with "Ops" or omit contrastive changes such as "assisted is now in the first
  pass instead of a follow-up".
- A later statement updates or resolves an earlier fact only when identity and
  semantics support the transition. Reuse the prior identity_ref exactly and
  set prior_state_ref. Otherwise create a separate report or keep the conflict
  open. Do not let recency, repetition, or sentiment establish authority.
- no_change records corroboration without a candidate. keep_conflict_open
  records disagreement that cannot safely become one current fact. Both still
  require direct evidence or claim_ids.
- For blocker and artifact_reference candidates, use an exact stable external
  identifier as identity_ref when present. For other state, use a concise,
  date-free identity such as decision/release-target or owner/rollback-pack.

Claim rules:
- claim_type is one of decision, status, blocker, handoff, artifact, schedule, correction.
- Every claim must cite at least one ID from new_event_ids; overlap_event_ids
  and earlier context cannot be the sole evidence.
- Preserve a source temporal expression verbatim in temporal_expression before
  resolving it. valid_at and invalid_at are optional RFC3339 timestamps.
  temporal_resolution is explicit, anchored, or unresolved.
- Claims are observations, not state: disagreement, concern, and corrections
  are claims even when they do not change canonical state.

Temporal rules for both decisions and claims:
- Preserve the exact source phrase in temporal_expression. Do not copy the
  event timestamp into temporal_expression when the source states no time.
- valid_at and invalid_at describe when the fact itself became and stopped
  being true. A deadline mentioned as the value is not automatically valid_at;
  keep that deadline exactly in the candidate body.
- Resolve today, tomorrow, yesterday, and unambiguous weekday expressions
  against the cited source event occurred_at and its timezone, and mark them
  anchored. Keep multiple plausible anchors unresolved. Never substitute
  ingestion or current wall-clock time.
- A correction must preserve the changed value and use update with the same
  identity. A resolved fact uses resolve and invalid_at only when the source
  establishes when it stopped being true.

`

const interactionPromptV2Current = `Interaction observations are optional for ordinary factual reports, but
mandatory when an event proposes, requests, questions, acknowledges, commits,
approves, rejects, hands off, escalates, or expresses concern or urgency.
Record stance (support, oppose, question, neutral) and the exact speech act
with one evidence_event_id each. A proposal, request, question, concern,
urgency, or acknowledgement alone cannot create, update, or resolve canonical
state. Only emit such a transition when separate evidence explicitly commits,
approves, rejects, hands off, escalates, or directly states the factual state.
Sentiment never changes factual confidence, authority, or temporal truth.

`

const interactionPromptV2Slim = `Return interaction_observations as an empty array. Do not restate proposals,
requests, questions, acknowledgements, concerns, or urgency as interaction
products. Deterministic admission inspects cited source Events directly. A
proposal, request, question, concern, urgency, or acknowledgement alone cannot
create, update, or resolve canonical state. Only emit such a transition when
the cited source separately commits, approves, rejects, hands off, escalates,
or directly states the factual state. Sentiment never changes factual
confidence, authority, or temporal truth.

`

const systemPromptV2Closing = `Do not infer identity, authorization, approval power, membership, or facts not
stated in the events. Return at most 10 state decisions with candidates;
prioritize explicit changes and corrections, then owners, exact values,
deadlines, dependencies, blockers, and handoffs over routine progress. Claims,
no_change, and interaction observations do not consume the candidate limit.`
