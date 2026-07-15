# Dockerized OpenCode evaluation

This suite evaluates producer-to-consumer transfer using two isolated OpenCode
containers connected only through the Team Note runtime.

## Layout

```text
evals/opencode/
  compose.yaml              Two agent containers plus runtime and PostgreSQL
  docker/opencode/          Pinned OpenCode and paxm image
  workspaces/producer/      Producer-only fixture material
  workspaces/consumer/      Consumer-only fixture material
```

Generated run artifacts live under the ignored `runs/<run-id>` directory.

The image builds paxm from a local source context and exports its production
OpenCode plugin. paxm uses its existing stdio JSON-RPC provider adapter to call
`paxm-team-memory-provider`, which maps paxm memory items and search queries to
the Team Memory HTTP API. This keeps the eval on the same OpenCode lifecycle
path used by paxm rather than using an eval-only OpenCode plugin.

## Agent identities

The eval runner injects identity metadata. Both agents may share a user but
must have stable, distinct agent IDs.

```text
producer: user_id=eval-owner agent_id=opencode-producer session_id=<runtime>
consumer: user_id=eval-owner agent_id=opencode-consumer session_id=<runtime>
```

Collaboration scope is resolved by the credential/provider configuration.

## MVP arms

- `control`: no cross-agent context
- `extracted_notes`: MVP extraction and admission

The first scenario asks the producer to read a release code that is absent from
the consumer workspace. The scorer accepts only the exact code, so elaboration,
guessing, and unknown responses fail Safe Success.

## Run

Copy or edit the repository-root `.env` file. It is ignored by Git and is
loaded automatically by `make docker-eval`. Set a local paxm checkout, one
OpenCode model, and an OpenAI-compatible Team Note extractor. Provider
credentials for the selected OpenCode model are passed through when present.

```bash
make docker-eval
```

The checked-in local placeholder selects `deepseek/deepseek-v4-flash` for
OpenCode and `deepseek-v4-flash` through DeepSeek's OpenAI-compatible endpoint
for Team Note extraction. Set `PAXM_SOURCE_DIR` and fill `DEEPSEEK_API_KEY`;
`TEAM_MEMORY_EXTRACTOR_API_KEY` reuses that key automatically.

The image pins OpenCode `1.17.20`. Override `OPENCODE_VERSION` deliberately when
refreshing the eval runtime, and keep the selected version in each run
manifest. The script produces raw producer and consumer JSON streams, scores
for both arms, and a manifest under `runs/<run-id>`.

To validate container wiring without spending model tokens, build the stack and
invoke the bridge's `paxm.health` method. The automated unit suite separately
tests put, putBatch, search, identity mapping, and validation behavior.
