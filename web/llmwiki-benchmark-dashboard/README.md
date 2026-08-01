# PAX Knowledge Eval Lab

This repository is the local Dashboard for the PAX Knowledge Eval Platform.
It fetches datasets, runs, solution versions, benchmarks, result matrices, and
artifact views from the local Knowledge Eval API. Its Experiment Tasks panel
can also preview, queue, inspect, and cancel bounded dataset experiments.
The Dataset panel can queue one supported source for pinned download,
validation, and answer-blind preparation under the API's configured
`-dataset-root`.

## Local development

Requires Node.js `>=22.13.0`.

```bash
# From the parent pax-nexus repository, in terminal 1:
go run ./cmd/knowledge-eval-api

# In terminal 2:
cd web/llmwiki-benchmark-dashboard
npm install
npm run dev
```

Use `npm test` for the production build plus server-rendered acceptance test,
and `npm run build` for the deployment build.

The API defaults to `http://localhost:58081`. Override it with
`VITE_KNOWLEDGE_EVAL_API_ORIGIN`.

## Data flow

The Dashboard does not import a run snapshot at build time. The browser calls
the paginated `/v1/knowledge-eval/*` API and refreshes every ten seconds. The
local API merges `.build/datasets/llmwiki/prepared` with `public/acceptance/`
on every request. Prepared groups appear before they are run; completed runs
then add status, artifacts, and benchmark results without rebuilding the
frontend.

Experiment tasks are persisted under `.build/knowledge-eval/tasks`; their
result bundles are written below `public/acceptance/tasks/<task-id>`. The
backend executes one task at a time. A source-only baseline uses no paid LLM.
A maintainer task requires both backend `DEEPSEEK_API_KEY` configuration and an
explicit confirmation in the UI. Credentials are never sent to or stored by
the browser.

The browser presents the catalog as Dataset → Partition → Group. LoCoMo groups
are conversations, LongMemEval-S groups are case haystacks, and
LongMemEval-V2 question cases are deduplicated into their shared trajectory
environments.

Dataset installation tasks are persisted separately under
`.build/knowledge-eval/dataset-install-tasks`. The browser cannot submit an
arbitrary server path; choose another disk by starting the API with
`-dataset-root /path/to/data`.

Portable snapshots remain the V1 storage and audit format:

- `public/acceptance/eval-lab-demo.json` is the deterministic V1 acceptance
  bundle.
- `public/acceptance/dataset/dataset-run.json` is the current real-dataset run.
- `public/acceptance/**/artifacts/` contains content-addressed payloads.
- `public/acceptance/**/views/` contains native, canonical, raw, and diff views.

Regenerate the deterministic bundle from the parent repository:

```bash
GOCACHE=/private/tmp/paxd-go-cache \
  go run ./cmd/knowledge-eval-demo \
  -output web/llmwiki-benchmark-dashboard/public/acceptance
```

The LoCoMo train `conv-26` section currently compares a retained source-only
baseline with the real LLM Wiki maintainer arm. Builder-visible ingest,
reader-visible questions, and evaluator-only gold must remain separated.

## Repository boundary

This directory is an independent Git repository. Commit Dashboard code and
generated acceptance snapshots here. Commit the Go platform, commands, and
design documents in the parent `pax-nexus` repository.

Keep development and previews local/LAN-only. Do not deploy this Dashboard or
its artifacts to ChatGPT Sites.
