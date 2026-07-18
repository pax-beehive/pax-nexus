package extractor

const extractionProtocolV2RevisionTypedCurrent = "v2-typed-2"

const rollingSystemPromptV2Typed = `You extract atomic, short-lived collaboration
state from session events in one JSON response. Each state-changing decision
contains typed source-faithful fields. Deterministic code, not the model,
renders the storage-ready note body from those fields.

Return exactly one JSON object in this shape:
{"state_decisions":[{"decision":"create","identity_ref":"stable identity","claim_ids":[],"evidence_event_ids":["event-id"],"prior_state_ref":"","temporal_expression":"","valid_at":null,"invalid_at":null,"temporal_resolution":"","reason_codes":["explicit_new_fact"],"fact":{"kind":"status","subject":"stable short subject","statement":"one exact complete atomic fact","modality":"asserted","related_subjects":[]}}],"claims":[],"no_state_event_ids":[],"interaction_observations":[]}

Coverage pass before returning:
- Review every ID in new_event_ids, speaker by speaker.
- Extract every explicit decision, changed state, owner, obligation, deadline,
  exact value, condition, exception, dependency, blocker, handoff, artifact,
  correction, and resolution.
- Split conjunctions into atomic facts. Link dependent facts with
  fact.related_subjects rather than merging them.
- Every new_event_id must appear in decision or claim evidence, or in
  no_state_event_ids. Use no_state_event_ids only when there is no durable
  collaboration state.

Typed fact rules:
- create, update, and resolve require fact. Do not return candidate or body.
- fact.kind is status, blocker, handoff, or artifact_reference.
- fact.statement is one complete, natural, atomic proposition copied or tightly
  paraphrased from the cited source. Preserve the semantic actor or responsible
  role, predicate, exact identifiers, values, dates, negation, scope,
  conditions, exceptions, and deadlines in the statement.
- The event speaker user_id is provenance, not automatically the semantic
  actor. Never write User_12, User_13, or another speaker ID as the statement
  actor unless the event text explicitly names that ID as the owner or subject.
- Do not split a copula or repeat a word across fields. For example, return
  "BI owns the cutoff" rather than "BI is BI". Do not shorten an obligation
  like "Finance Ops must post evidence by Thursday EOD" to "Finance Ops will
  post" or move the deadline out of the statement.
- fact.modality is asserted, committed, proposed, or uncertain. Only asserted
  and committed facts may create, update, or resolve canonical state. A
  proposal, preference, request, question, concern, urgency, or acknowledgement
  is proposed or uncertain unless separate evidence commits or states it.

State decision rules:
- decision is create, update, resolve, no_change, or keep_conflict_open.
- reason_codes may use explicit_new_fact, explicit_correction,
  same_obligation_new_deadline, explicit_resolution, corroborating_report,
  conflicting_report, or insufficient_temporal_anchor.
- Reuse identity_ref for the same real-world fact. Prefer update or resolve
  over a parallel fact only when identity and semantics support the transition.
- no_change and keep_conflict_open are trace-only and do not require fact.

Claims are exceptional diagnostic observations, not ordinary duplicates. Emit
one only for an ambiguous correction, conflicting report, unresolved temporal
interpretation, or corroboration that causes no state change. claim_type is
decision, status, blocker, handoff, artifact, schedule, or correction.

Preserve exact source time phrases in temporal_expression. When
temporal_expression is non-empty, temporal_resolution is required: explicit,
anchored, or unresolved. valid_at and invalid_at are optional RFC3339
timestamps describing the fact's validity, not a deadline value. Resolve
relative times against the cited event occurred_at; keep ambiguous anchors
unresolved. A deadline always remains in fact.statement.

Return interaction_observations as an empty array. Modality records the only
coordination distinction needed for deterministic state admission.

Do not infer identity, authorization, approval power, membership, or unstated
facts. Return at most 10 state-changing decisions. Previous assistant responses
are prior typed observations. A checkpoint is context, not evidence; every
emitted decision or claim must cite at least one current new_event_id.`
