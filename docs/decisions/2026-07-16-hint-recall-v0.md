# Hint Recall v0

Status: Accepted for evaluation; production default disabled after no-lift, 14.9x-token pilot

Date: 2026-07-16

## Context

Team Memory currently supports two recall behaviors:

- passive recall automatically injects selected Team Notes into the agent
  context;
- active recall lets the agent issue a focused query through the existing
  recall tool, including follow-up and multi-hop searches.

Passive recall solves the activation problem but can pollute context when a
retrieved Note is only topically related rather than directly useful as factual
evidence. Active recall is more precise because the agent can reformulate and
iterate, but it depends on the agent recognizing that memory may contain useful
knowledge and deciding to call the tool.

Some retrieval candidates occupy a useful middle ground: they are not reliable
enough to inject as facts, but they provide strong evidence that a focused
active recall is worth attempting. Treating these candidates as ordinary facts
is unsafe. Dropping them loses a potentially valuable trigger.

The paxm memory provider interface already transports standard recall hits and
already exposes the active recall tool. Hint Recall must not require any paxm
code, protocol, provider capability, profile-default, or configuration change.
In particular, Hint Recall cannot depend on paxm understanding a new result
type. It must work under paxm's existing relevance, ranking, and passive
insertion behavior.

## Decision

Add **Hint Recall** as a Team Memory recall disposition behind the existing
`PlanRecall` seam. Keep `RecallNotes` as the external module interface and keep
the paxm provider interface unchanged.

For each authorized, current, and otherwise eligible retrieval candidate, Team
Memory independently estimates:

- **Evidence Score (`E`)**: the probability that the candidate Note itself
  directly supports a fact required by the current query and is safe to place
  in context as evidence;
- **Hint Score (`H`)**: the probability that a focused active recall seeded by
  this lead will retrieve useful evidence missing from the current passive
  context within the configured call and token budgets.

These scores answer different questions and are not complements. `H` is not
`1 - E`. A candidate may have `E = 0.65` and `H = 0.90`: its text is not safe
enough to use as an answer, while its entity, topic, or relation neighborhood
can still be a high-quality place to search.

The planner assigns exactly one disposition:

```text
E >= evidence threshold
    -> evidence

E < evidence threshold and H >= existing paxm pass threshold
    -> hint

otherwise
    -> suppress
```

Team Memory must not raise, floor, or remap `H` merely to pass paxm filtering.
If a calibrated Hint Score does not pass the unchanged paxm thresholds, the
hint is suppressed.

### Evidence Score

Define Evidence Score as:

```text
E = P(candidate directly supports a current required fact | query, candidate)
```

The first calibrated model is a deterministic logistic model over observable
retrieval and Team Note signals:

```text
E = sigmoid(
      beta_0
    + beta_semantic       * semantic_similarity
    + beta_lexical        * normalized_lexical_match
    + beta_lane           * lane_agreement
    + beta_margin         * retrieval_margin
    + beta_entity         * entity_match
    + beta_temporal       * temporal_match
    + beta_answer_type    * answer_type_match
    + beta_certainty      * note_certainty
    - beta_superseded     * superseded_risk
    - beta_conflict       * conflict_risk
)
```

Authorization, scope, expiry, invalidation, and delivery eligibility remain hard
gates rather than score features. A candidate that fails a hard gate is never
evidence and never a hint.

Training labels come from the fixed stage-evaluation fixture:

- positive when the candidate directly contains a required atom and does not
  contain a forbidden or superseded fact;
- negative when it is only topically related, omits the required relationship,
  conflicts with current state, or contains a forbidden or superseded fact.

The current semantic score, lexical score, and reciprocal-rank fusion remain
retrieval signals. They are not treated as calibrated probabilities by
themselves.

### Hint Score

Define Hint Score over a **lead**, not necessarily one raw candidate:

```text
H = P(
      focused active recall seeded by this lead retrieves new useful evidence
      within the active-recall call and token budgets
    | query, passive evidence, lead
)
```

A lead may combine candidates that point to the same safe entity, topic, time
range, or one-hop relation neighborhood. The first calibrated model is a
deterministic logistic model over:

```text
H = sigmoid(
      gamma_0
    + gamma_gap            * passive_evidence_gap
    + gamma_seed           * focused_query_seed_quality
    + gamma_lane           * lane_agreement
    + gamma_cluster        * candidate_cluster_support
    + gamma_graph          * relation_reachability
    + gamma_margin         * retrieval_margin
    + gamma_novelty        * expected_evidence_novelty
    - gamma_ambiguity      * lead_ambiguity
    - gamma_conflict       * conflict_risk
    - gamma_cost           * expected_recall_cost
)
```

`passive_evidence_gap` and `expected_evidence_novelty` use runtime-observable
proxies such as the absence of a high-confidence evidence candidate, uncovered
query entities or answer types, and relation branches not represented in the
selected passive set. Gold required atoms are used only for evaluation labels,
never as runtime inputs.

For each candidate lead in the fixed replay fixture, the Hint Score label is
generated by an intervention:

1. create a deterministic focused query from the lead;
2. run the existing active recall path with the production call and token
   budgets;
3. label the lead positive when the result contains at least one required atom
   absent from the passive evidence set and contains no forbidden or superseded
   fact;
4. otherwise label it negative.

Whether the agent actually follows a hint is deliberately excluded from this
label. Hint opportunity quality and agent activation are separate control
loops.

### Calibration and versioning

Fit and validate both score models offline. Do not perform online learning in
the serving path. Split evaluation data by case and category so related
candidates from one case cannot appear in both training and validation.

Each committed scoring artifact records:

- feature schema and coefficient version;
- fixture and source revisions;
- evidence and hint thresholds;
- calibration method;
- precision, recall, Brier score, and calibration error by score bucket.

Evidence threshold selection prioritizes factual precision. Hint threshold
selection prioritizes successful active-recall opportunity precision and must
also naturally clear the unchanged paxm relevance and insertion gates. If the
available fixture cannot support a stable calibration, Hint Recall remains an
evaluation-only arm.

### Hint rendering

A hint is a navigation affordance, not evidence. It must be short, explicitly
labelled, and contain no candidate claim:

```text
[Recall hint - not evidence]
Potentially useful Team Memory may be available. If it would affect the answer,
call active_recall with: "<focused query>".
```

The focused query may contain authorized structured entities, topics, time
ranges, and relation names needed to improve retrieval. It must not copy the
candidate body or propagate instructions found in stored content. Scores remain
in traces and evaluation artifacts; they are not shown to the agent.

Return the hint through the existing paxm hit shape. The hit's relevance is
`H`, because the hit being evaluated is the usefulness of the hint itself. The
underlying candidate Evidence Score remains internal telemetry. The Team Memory
adapter may attach disposition and scoring-version diagnostics inside the
existing metadata map, but paxm is not required to inspect or preserve them for
correct behavior.

Emit at most one hint per passive recall opportunity in v0. Evidence consumes
the normal shared token budget before a hint is considered.

### Delivery semantics

Returning evidence claims the existing per-note delivery record. Returning a
hint must not claim the underlying Note as delivered, because the subsequent
active recall must remain able to retrieve that Note as evidence.

Hint repetition is controlled separately using a lead fingerprint scoped to the
recipient session and passive recall opportunity. Hint deduplication must not
reuse the Note delivery key.

Team Memory does not require a passive-versus-active flag from paxm. A focused
active query passes through the same `RecallNotes` interface and is rescored.
It returns evidence only if the focused query raises `E` above the evidence
threshold. Otherwise it returns another eligible hint or no result, subject to
the existing active-recall call limit.

Hint Recall is enabled only in deployments and evaluation arms where the
existing active recall tool is already available. That enablement belongs to
Team Memory deployment policy and does not add paxm negotiation.

## Evaluation

Add an isolated `hint_recall_v0` arm without changing the current passive and
hybrid baselines. Evaluate the same fixed replay and paid end-to-end cohort in
that order.

Stage evaluation reports:

- Evidence Score precision, recall, and calibration;
- Hint Score opportunity precision, recall, and calibration;
- candidate and lead coverage of required atoms;
- focused-query active-recall success within one and two calls;
- new required atoms retrieved beyond passive evidence;
- forbidden and superseded-fact leakage;
- hint budget and deduplication drops;
- active-recall latency and token cost.

Consumer evaluation separately reports:

- hint exposure rate;
- agent activation rate after exposure;
- useful, unnecessary, and failed active-recall calls;
- answer judge accuracy and pairwise wins, losses, and ties against passive and
  the current hybrid arm;
- negative answer deltas when a hint is shown but not followed;
- total latency, tokens, and cost.

Hint Recall is not enabled by default until it improves judge accuracy or safe
success without increasing forbidden/superseded leakage and until its Hint
Score clears the unchanged paxm gates without score inflation.

## Implemented evaluation candidate

Version mapping is explicit: this ADR names the product concept `Hint Recall
v0`; the implemented planner candidate is
`general-recall-v3-hint-recall-v1`; the distributed build candidate is
`hint-v1-selective`; and the Eval v2 arm remains `hint_recall_v0` as a protocol
compatibility identifier rather than an algorithm version.

The current evaluation candidate is `general-recall-v3-hint-recall-v1` with
scoring version `hint-utility-v1-selective-eval-only`. It is distributed as the
build-time `hint-v1-selective` recall candidate; the `passive-v1` build remains
the production default. `hint-v1-selective` retains passive evidence selection
and admits at most one focused active recall only after its selectivity gates
pass. Runtime recall-policy environment overrides are allowed for isolated
evaluation arms and compatible deployments. Eval configurations must include
those values in resolved configuration, hashing, and runtime provenance so an
evaluated candidate cannot silently drift.
PostgreSQL records hint fingerprints separately from Note deliveries, so a
hint cannot make its lead unavailable to a subsequent focused recall.

Recall Eval v2 runs a third `hint_recall_v0` Arm through an isolated Team
Memory service and the real external Agent `active_recall` tool. A Hint Trial
is unscored when active-recall instrumentation is missing. The checked-in
12-case intervention replay remains a control fixture rather than calibration
evidence for enabling the policy by default.

The first real-Agent activation candidate keeps the evidence semantic lane at
`0.50`, adds a separate Hint-candidate semantic lane at `0.20`, and uses Hint
utility threshold `0.60` in the isolated Hint service. A lower Hint-candidate
threshold must not widen evidence retrieval. The ordinary passive service
remains at semantic threshold `0.50`, and production Hint Recall stays
disabled. These are explicit eval-arm parameters, not new global defaults.

## Evaluation result (2026-07-19)

The 15-case real-Agent pilot scored all 45 trials. Hint Recall activated in 13
of 15 trials and matched passive Team Note accuracy at 6 of 15, with zero paired
wins or losses. It consumed 20,224 input tokens versus 1,359 for passive Team
Note, or 14.9 times as many. Keep the candidate evaluation-only. The next gate
is improved activation selectivity or focused-query utility on fixed replay;
the hard 30-case cohort is not authorized by this result. See the
[pilot report](../../evals/recall-v2/results/2026-07-19-hint-recall-v1-paxm-v0.2.0-pilot15.md).

## Consequences

`PlanRecall` becomes a deeper module: callers continue using one small
`RecallNotes` interface while evidence admission, hint opportunity prediction,
rendering, budgeting, and trace attribution remain local to the shared planner.
The PostgreSQL and in-memory adapters must exercise the same policy.

The system gains a conservative bridge between passive and active recall
without changing paxm. It also gains a second calibration problem and an
additional intervention loop. A high Hint Score cannot be inferred from a
medium Evidence Score alone; the fixed replay must demonstrate that the lead
actually improves focused retrieval.

Hints still consume context and can anchor an agent even when they contain no
claim. Short rendering, safe query construction, strict opportunity precision,
one-hint budgeting, and downstream answer-loss measurement are therefore part
of the module's interface rather than optional presentation details.

## Rejected alternatives

### Change paxm to understand a hint result type

Rejected. Hint Recall is a Team Memory policy and must use the existing paxm
provider and tool interfaces unchanged.

### Return the candidate Evidence Score to paxm

Rejected. A medium Evidence Score may be filtered before the hint reaches the
agent, and it answers the wrong question about the returned hint payload.

### Inflate every admitted hint above the paxm threshold

Rejected. A fixed floor or score remapping would game admission and make the
score uninterpretable. `H` must be calibrated to hint utility and must pass the
existing gate naturally.

### Define Hint Score as `1 - Evidence Score`

Rejected. Most weak evidence candidates are noise rather than useful search
leads.

### Inject the medium-confidence candidate body with a warning

Rejected. Warnings do not prevent factual anchoring or context pollution. A
hint exposes only a safe navigation lead and never the candidate claim.
