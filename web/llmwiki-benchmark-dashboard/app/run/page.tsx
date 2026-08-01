"use client";

/* eslint-disable @next/next/no-html-link-for-pages -- vinext's next/link shim loads a duplicate React runtime in dev. */

import { useEffect, useState } from "react";

type Metric = {
  name: string;
  value: number;
  unit: string;
};

type Trial = {
  id: string;
  benchmark_id: string;
  status: string;
  result_status?: string;
  ineligible_reason?: string;
  metrics?: Metric[];
};

type Run = {
  id: string;
  dataset: string;
  partition: string;
  case_id: string;
  solution_version_id: string;
  artifact_id: string;
  status: string;
  created_at: string;
  completed_at?: string;
  metadata?: Record<string, string>;
};

type Artifact = {
  artifact_id: string;
  product: string;
  kind: string;
  role: string;
  views: Record<string, string>;
};

type RunEvent = {
  id: string;
  stage: string;
  message: string;
  created_at: string;
};

type RunDetail = {
  run: Run;
  trials: Trial[];
  events: RunEvent[];
  artifact?: Artifact;
};

const API = "/v1/knowledge-eval";

async function getRun(runID: string): Promise<RunDetail> {
  const response = await fetch(`${API}/runs/${encodeURIComponent(runID)}`, {
    headers: { accept: "application/json" },
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`);
  }
  return response.json() as Promise<RunDetail>;
}

function formatDate(value: string | undefined): string {
  if (!value) return "—";
  return new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "medium",
    timeStyle: "medium",
  }).format(new Date(value));
}

function formatMetric(metric: Metric): string {
  if (metric.unit === "ratio") {
    return `${Math.round(metric.value * 100)}%`;
  }
  return Number.isInteger(metric.value)
    ? String(metric.value)
    : metric.value.toFixed(3).replace(/0+$/, "").replace(/\.$/, "");
}

function primaryMetric(trial: Trial): Metric | undefined {
  const preferred = trial.benchmark_id === "knowledge-search-get-qa"
    ? "answer_accuracy"
    : trial.benchmark_id === "wiki-artifact-quality"
      ? "artifact_quality_score"
      : "task_success_rate";
  return trial.metrics?.find((metric) => metric.name === preferred) ?? trial.metrics?.[0];
}

export default function RunPage() {
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const runID = new URLSearchParams(window.location.search).get("run") ?? "";
    if (!runID) {
      const timer = window.setTimeout(() => setError("Run ID is missing."), 0);
      return () => window.clearTimeout(timer);
    }
    void getRun(runID)
      .then(setDetail)
      .catch((loadError: unknown) => {
        setError(loadError instanceof Error ? loadError.message : "Unknown API error");
      });
  }, []);

  return (
    <main className="run-detail-page">
      <header className="topbar">
        <a className="brand" href="/">
          <span className="brand-mark">KE</span>
          <span>
            <strong>Knowledge Eval Registry</strong>
            <small>run &amp; scores</small>
          </span>
        </a>
        <a className="button secondary" href="/#tasks">返回任务工作台</a>
      </header>

      {error && (
        <section className="dataset-browser-error run-detail-message" role="alert">
          <strong>Run unavailable</strong>
          <span>{error}</span>
        </section>
      )}

      {!detail && !error && <div className="loading-panel run-detail-message">Loading run &amp; scores…</div>}

      {detail && (
        <>
          <section className="run-detail-hero">
            <div>
              <p className="eyebrow">{detail.run.dataset} · {detail.run.partition} / {detail.run.case_id}</p>
              <h1>{detail.run.id}</h1>
              <p>{detail.run.solution_version_id}</p>
            </div>
            <dl className="run-detail-summary">
              <div><dt>Status</dt><dd>{detail.run.status}</dd></div>
              <div><dt>Model</dt><dd>{detail.run.metadata?.model ?? "—"}</dd></div>
              <div><dt>Created</dt><dd>{formatDate(detail.run.created_at)}</dd></div>
              <div><dt>Completed</dt><dd>{formatDate(detail.run.completed_at)}</dd></div>
            </dl>
          </section>

          <section className="run-detail-section">
            <div className="section-heading compact">
              <div>
                <p className="eyebrow">BENCHMARK RESULTS</p>
                <h2>分数与诊断指标</h2>
              </div>
              <span>{detail.trials.length} benchmarks</span>
            </div>
            <div className="run-score-grid">
              {detail.trials.map((trial) => {
                const primary = primaryMetric(trial);
                return (
                  <article className="run-score-card" key={trial.id}>
                    <div className="run-score-head">
                      <div>
                        <code>{trial.benchmark_id}</code>
                        <h3>{primary ? formatMetric(primary) : "Not scored"}</h3>
                      </div>
                      <span className={`status ${trial.status === "completed" ? "success" : "warning"}`}>
                        {trial.status}
                      </span>
                    </div>
                    {trial.ineligible_reason && <p className="task-warning">{trial.ineligible_reason}</p>}
                    <dl className="run-metric-list">
                      {(trial.metrics ?? []).map((metric) => (
                        <div key={metric.name}>
                          <dt>{metric.name.replaceAll("_", " ")}</dt>
                          <dd>{formatMetric(metric)}</dd>
                        </div>
                      ))}
                    </dl>
                  </article>
                );
              })}
            </div>
          </section>

          <section className="run-detail-lower">
            <article className="run-artifact-panel">
              <p className="eyebrow">ARTIFACT</p>
              <h2>{detail.artifact?.product ?? "No artifact"}</h2>
              <p>{detail.artifact?.artifact_id ?? detail.run.artifact_id}</p>
              <div className="artifact-actions">
                {Object.entries(detail.artifact?.views ?? {}).map(([kind, href]) => (
                  <a className={`button ${kind === "native" ? "primary" : "secondary"}`} href={href} key={kind} target="_blank" rel="noreferrer">
                    {kind === "native" ? "打开制品 ↗" : `${kind} ↗`}
                  </a>
                ))}
                <a className="button secondary" href={`${API}/runs/${encodeURIComponent(detail.run.id)}`} target="_blank" rel="noreferrer">
                  Raw JSON ↗
                </a>
              </div>
            </article>

            <article className="run-event-panel">
              <p className="eyebrow">RUN EVENTS</p>
              <h2>执行时间线</h2>
              <ol>
                {detail.events.map((event) => (
                  <li key={event.id}>
                    <span>{event.stage}</span>
                    <strong>{event.message}</strong>
                    <time>{formatDate(event.created_at)}</time>
                  </li>
                ))}
              </ol>
              {detail.events.length === 0 && <p className="task-empty">这个 Run 没有记录事件。</p>}
            </article>
          </section>
        </>
      )}
    </main>
  );
}
