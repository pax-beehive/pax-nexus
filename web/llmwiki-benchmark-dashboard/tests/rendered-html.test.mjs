import assert from "node:assert/strict";
import test from "node:test";

async function render(path = "/") {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}`);
  const { default: worker } = await import(workerUrl.href);

  return worker.fetch(
    new Request(`http://localhost${path}`, {
      headers: { accept: "text/html" },
    }),
    {
      ASSETS: {
        fetch: async () => new Response("Not found", { status: 404 }),
      },
    },
    {
      waitUntil() {},
      passThroughOnException() {},
    },
  );
}

test("server-renders the knowledge evaluation registry", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type") ?? "", /^text\/html\b/i);

  const html = await response.text();
  assert.match(html, /<title>Knowledge Eval Registry<\/title>/i);
  assert.match(html, /我跑了什么/);
  assert.match(html, /LIVE EVALUATION REGISTRY/);
  assert.match(html, /live local API/);
  assert.match(html, /prepared 数据新增后会直接进入目录/);
  assert.match(html, /数据集与 Groups/);
  assert.match(html, /Loading datasets from Query API/);
  assert.match(html, /Benchmark 与成绩矩阵/);
  assert.match(html, /Run × Benchmark/);
  assert.doesNotMatch(html, /dataset-run\.json/);
  assert.doesNotMatch(html, /slice|roadmap|BUILD PROGRESS|Benchmark radar/i);
  assert.doesNotMatch(html, /codex-preview|react-loading-skeleton/i);
});

test("server-renders the dataset browser shell", async () => {
  const response = await render("/dataset?dataset=locomo&partition=train&case=conv-26");
  assert.equal(response.status, 200);
  const html = await response.text();
  assert.match(html, /dataset browser/i);
  assert.match(html, /Loading dataset/);
  assert.match(html, /返回 Dashboard/);
});

test("server-renders the run and scores shell", async () => {
  const response = await render("/run?run=run-example");
  assert.equal(response.status, 200);
  const html = await response.text();
  assert.match(html, /run &amp; scores/i);
  assert.match(html, /Loading run &amp; scores/i);
  assert.match(html, /返回任务工作台/);
});

test("server-renders the cohort campaign shell", async () => {
  const response = await render("/cohort");
  assert.equal(response.status, 200);
  const html = await response.text();
  assert.match(html, /Cohort Campaigns/i);
  assert.match(html, /Preview campaign/i);
  assert.match(html, /付费保护/);
  assert.match(html, /返回 Dashboard/);
});
