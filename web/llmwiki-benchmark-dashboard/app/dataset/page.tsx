"use client";

/* eslint-disable @next/next/no-html-link-for-pages -- vinext's next/link shim loads a duplicate React runtime in dev. */

import { useEffect, useMemo, useState } from "react";

type Dataset = {
  id: string;
  dataset: string;
  partition: string;
  case_id: string;
  status: string;
  sessions: number;
  turns: number;
  sources: number;
  questions: number;
  experiment_count: number;
  updated_at?: string;
  group_kind: string;
  source_kind: string;
  trajectories: number;
  evaluation_cases: number;
  run_count: number;
  artifact_count: number;
  case_ids: string[];
};

type DatasetDetail = {
  dataset: Dataset;
  source_artifact_id: string;
  run_ids: string[];
};

type DatasetSession = {
  id: string;
  source_path: string;
  turns: number;
};

type SessionsResponse = {
  items: DatasetSession[];
  page: { limit: number; next_cursor?: string };
};

const API = "/v1/knowledge-eval";

async function getJSON<T>(path: string): Promise<T> {
  const response = await fetch(`${API}${path}`, {
    headers: { accept: "application/json" },
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`);
  }
  return response.json() as Promise<T>;
}

function identityPath(dataset: string, partition: string, caseID: string): string {
  return [dataset, partition, caseID].map(encodeURIComponent).join("/");
}

export default function DatasetPage() {
  const [detail, setDetail] = useState<DatasetDetail | null>(null);
  const [sessions, setSessions] = useState<DatasetSession[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const dataset = params.get("dataset") ?? "";
    const partition = params.get("partition") ?? "";
    const caseID = params.get("case") ?? "";
    if (!dataset || !partition || !caseID) {
      const timer = window.setTimeout(
        () => setError("Dataset identity is incomplete."),
        0,
      );
      return () => window.clearTimeout(timer);
    }
    const path = identityPath(dataset, partition, caseID);
    void getJSON<{ detail: DatasetDetail }>(`/datasets/${path}`)
      .then(async (detailResponse) => {
        setDetail(detailResponse.detail);
        if (!detailResponse.detail.source_artifact_id) {
          setSessions([]);
          setSelectedID("");
          return;
        }
        const sessionsResponse = await getJSON<SessionsResponse>(
          `/datasets/${path}/sessions?limit=200`,
        );
        setSessions(sessionsResponse.items);
        setSelectedID(sessionsResponse.items[0]?.id ?? "");
      })
      .catch((loadError: unknown) => {
        setError(loadError instanceof Error ? loadError.message : "Unknown API error");
      });
  }, []);

  const selected = useMemo(
    () => sessions.find((session) => session.id === selectedID),
    [selectedID, sessions],
  );
  const sessionView = detail && selected
    ? `${API}/datasets/${identityPath(
        detail.dataset.dataset,
        detail.dataset.partition,
        detail.dataset.case_id,
      )}/sessions/${encodeURIComponent(selected.id)}/view`
    : "";

  return (
    <main className="dataset-browser-page">
      <header className="topbar">
        <a className="brand" href="/">
          <span className="brand-mark">KE</span>
          <span>
            <strong>Knowledge Eval Registry</strong>
            <small>dataset browser</small>
          </span>
        </a>
        <a className="button secondary" href="/">返回 Dashboard</a>
      </header>

      {error && (
        <section className="dataset-browser-error" role="alert">
          <strong>Dataset unavailable</strong>
          <span>{error}</span>
        </section>
      )}

      {!detail && !error && <div className="loading-panel dataset-browser-loading">Loading dataset…</div>}

      {detail && (
        <>
          <section className="dataset-browser-hero">
            <div>
              <p className="eyebrow">{detail.dataset.dataset} · {detail.dataset.partition}</p>
              <h1>{detail.dataset.case_id}</h1>
              <p>{detail.dataset.group_kind} Group · {detail.dataset.status}</p>
              <code>{detail.dataset.id}</code>
            </div>
            <dl className="dataset-browser-stats">
              <div>
                <dt>{detail.dataset.trajectories > 0 ? "Trajectories" : "Sessions"}</dt>
                <dd>{detail.dataset.trajectories || detail.dataset.sessions}</dd>
              </div>
              <div><dt>Turns</dt><dd>{detail.dataset.turns}</dd></div>
              <div><dt>Evaluation cases</dt><dd>{detail.dataset.evaluation_cases}</dd></div>
              <div><dt>Runs</dt><dd>{detail.dataset.run_count}</dd></div>
            </dl>
          </section>

          {detail.source_artifact_id ? (
            <section className="dataset-browser-content">
              <aside className="session-index">
                <div className="session-index-head">
                  <p className="eyebrow">MATERIALIZED BUILD SOURCES</p>
                  <h2>Sessions</h2>
                  <span>{sessions.length} source files</span>
                </div>
                <div className="session-list">
                  {sessions.map((session) => (
                    <button
                      className={session.id === selectedID ? "active" : ""}
                      key={session.id}
                      onClick={() => setSelectedID(session.id)}
                      type="button"
                    >
                      <strong>{session.id}</strong>
                      <span>{session.turns} turns</span>
                    </button>
                  ))}
                </div>
              </aside>

              <div className="session-preview">
                <div className="session-preview-head">
                  <div>
                    <p className="eyebrow">SOURCE VIEW</p>
                    <h2>{selected?.id ?? "Select a session"}</h2>
                  </div>
                  <code>{selected?.source_path ?? "—"}</code>
                </div>
                {sessionView && (
                  <iframe
                    key={sessionView}
                    src={sessionView}
                    title={`Dataset session ${selected?.id ?? ""}`}
                  />
                )}
              </div>
            </section>
          ) : (
            <section className="unrun-group-panel">
              <p className="eyebrow">NOT MATERIALIZED YET</p>
              <h2>这个 Group 已在 prepared dataset 中，但还没有 Run 或 Artifact。</h2>
              <p>
                Build input 类型为 <code>{detail.dataset.source_kind}</code>；
                启动 Run 后，这里会出现不可变 sources、Wiki 制品和 benchmark 结果。
              </p>
            </section>
          )}

          <section className="dataset-browser-context">
            <article>
              <p className="eyebrow">QUESTIONS</p>
              <h2>{detail.dataset.evaluation_cases} evaluation cases</h2>
              <p>
                当前 bundle 保存了 case ID、gold、actual 和 failure stage，但没有保存原始
                question 文本。完整 question manifest 会作为下一版 answer-blind
                dataset schema 写入；它不会进入 maintainer build input。
              </p>
            </article>
            <article>
              <p className="eyebrow">RELATED RUNS</p>
              <h2>Runs using this dataset</h2>
              <div className="related-run-list">
                {detail.run_ids.map((runID) => (
                  <a href={`/run?run=${encodeURIComponent(runID)}`} key={runID}>
                    {runID} ↗
                  </a>
                ))}
              </div>
            </article>
          </section>
        </>
      )}
    </main>
  );
}
