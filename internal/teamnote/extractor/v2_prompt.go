package extractor

// extractionProtocolV2Revision changes whenever the stable v2 response schema
// or reasoning contract changes. It is part of rolling episode compatibility,
// independent of the operator-owned prompt version label.
const (
	extractionProtocolV2RevisionCurrent          = "v2-slim-5-temporal-deterministic"
	extractionProtocolV2RevisionInteractionSlim  = "v2-slim-4-interaction-slim-temporal-deterministic"
	extractionProtocolV2RevisionEvidenceFidelity = "v2-slim-7-evidence-fidelity-v1-temporal-deterministic"
	extractionProtocolV2RevisionSourceClause     = "v2-slim-7-source-clause-v1"
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

// rollingSystemPromptV2EvidenceFidelity keeps the semantic Candidate schema
// and deterministic admission of interaction-slim while giving source fidelity
// one explicit, candidate-local pass.
const rollingSystemPromptV2EvidenceFidelity = systemPromptV2BeforeInteractions + interactionPromptV2Slim + evidenceFidelityPromptV2 + systemPromptV2Closing + `
You are maintaining one cumulative knowledge state for a task or thread across
multiple agents and sessions. Previous assistant responses are your own prior
state decisions and exceptional claims. Reuse the same identity_ref for the
same real-world fact, decision, obligation, blocker, or artifact even when a
different agent reports the update. Prefer update or resolve over creating a
parallel fact. A checkpoint is a lossy handoff context, not new evidence;
every emitted decision or claim must still cite at least one event from the
current new_event_ids.`

const rollingSystemPromptV2SourceClause = systemPromptV2BeforeInteractions + interactionPromptV2Slim + sourceClausePromptV2 + systemPromptV2Closing + `
You are maintaining one cumulative knowledge state for a task or thread across
multiple agents and sessions. Previous assistant responses are your own prior
state decisions and exceptional claims. Reuse the same identity_ref for the
same real-world fact. Every emitted decision or claim must still cite at least
one event from the current new_event_ids.`

// rollingSystemPromptClaimCardV1 keeps the v2 reasoning products, but makes
// claims the only model-written source for persisted candidate content.
const rollingSystemPromptClaimCardV1 = rollingSystemPromptV2 + `

Claim-card strategy override:
- This override takes precedence over earlier guidance that ordinary decisions
  do not need claims. Every create, update, and resolve decision MUST reference
  exactly one primary claim in claim_ids. Split compound facts into separate
  atomic claim-card decisions.
- Put every answerable fact in that claim's subject, predicate, value, speaker,
  evidence_event_ids, and temporal fields. Preserve exact values, identities,
  negation, and source temporal language there.
- candidate remains a required schema placeholder for create, update, and
  resolve. Its kind, subject, body, related_subjects, and identity_ref are not
  persisted by this strategy. Do not put facts only in candidate.body.
- A proposal, request, question, concern, or acknowledgement remains a claim
  or no-change observation unless separate source evidence establishes a
  committed state transition.`

// rollingSystemPromptClaimCardV2 adds a source-faithfulness constraint after
// the v1 canary showed that abstract claim values lose answerable qualifiers.
const rollingSystemPromptClaimCardV2 = rollingSystemPromptClaimCardV1 + `

Exact-value override:
- claim.value MUST be the shortest contiguous source phrase that contains the
  answerable value or condition. Copy it exactly from one cited Event; do not
  summarize, generalize, reorder, or replace it with a category label.
- If an event states multiple independently answerable conditions, emit one
  Claim and one StateDecision for each condition. Keep each value focused on
  one condition, including qualifiers such as targeted, impacted, primary,
  backup, before, after, and negation.
- Never rely on candidate.body to preserve omitted wording. The persisted card
  renders claim.value verbatim.`

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
Emit each observation as
{"actor":"user id of the cited event's speaker","target":"user id addressed, or empty","stance":"support, oppose, question, or neutral","speech_act":"propose, request, commit, approve, reject, handoff, escalate, acknowledge, question, express_concern, or express_urgency","evidence_event_id":"one id from new_event_ids"}.
actor is required and always names the speaker of the cited event; an
observation without actor is discarded deterministically and its speech act
cannot inform state admission. A proposal, request, question, concern,
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

const evidenceFidelityPromptV2 = `Evidence fidelity pass before returning:
- Re-read the exact source clause cited by every create, update, and resolve
  decision, and compare it with candidate.subject and candidate.body.
- Preserve every source detail that changes the answer: actor or role, exact
  identifier or value, negation, temporal phrase, condition, exception,
  ordering, scope, and contrastive qualifier.
- Preserve concrete actions as actions. If the source requires both a targeted
  check and a rerun of an impacted workflow, do not replace them with a broad
  category such as validation coverage.
- Prefer durable current state, changes, owners, blockers, deadlines, and
  conditions over routine progress or adjacent discussion. Use no_state for
  conversational material that contains none of those facts.
- This pass does not relax modality. A proposal, desired owner, request, or
  question still cannot become committed state without separate source
  evidence that establishes the transition.

`

const sourceClausePromptV2 = `Source-clause admission rules:
- Every create, update, and resolve decision MUST include evidence_clauses.
- Each evidence clause has exactly this shape: {"event_id":"event-id","quote":"exact source text"}.
- quote MUST be the shortest exact contiguous text copied from that Event that
  establishes the candidate state. Do not paraphrase, normalize whitespace,
  join text across Events, or cite an adjacent sentence.
- A proposal, request, question, conditional preference, or desired owner is
  not committed state. Cite a separate exact clause that explicitly states,
  commits, assigns, approves, or resolves the candidate, or do not emit a
  state-changing decision.
- evidence_event_ids remains the Event-level provenance list. Source clauses
  narrow admission authority and do not replace Event evidence.

`

const systemPromptV2Closing = `Do not infer identity, authorization, approval power, membership, or facts not
stated in the events. Return at most 10 state decisions with candidates;
prioritize explicit changes and corrections, then owners, exact values,
deadlines, dependencies, blockers, and handoffs over routine progress. Claims,
no_change, and interaction observations do not consume the candidate limit.`
