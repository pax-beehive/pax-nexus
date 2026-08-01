"use client";

/* eslint-disable @next/next/no-html-link-for-pages -- vinext's next/link shim loads a duplicate React runtime in dev. */

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  createExperimentIdempotencyKey,
  postExperimentJSON,
} from "../experiment-api";

type Partition = {
  name: string;
  group_count: number;
  run_group_count: number;
};

type Dataset = {
  id: string;
  name: string;
  partitions: Partition[];
};

type Model = {
  id: string;
  name: string;
};

type Selection = {
  dataset: string;
  partition: string;
};

type Issue = Selection & {
  group_id?: string;
  reason: string;
};

type Preview = {
  eligible: boolean;
  paid: boolean;
  total_groups: number;
  eligible_groups: number;
  ineligible_groups: number;
  total_questions: number;
  planned_questions: number;
  planned_tasks: number;
  max_llm_calls: number;
  issues: Issue[];
};

type Summary = {
  total_groups: number;
  eligible_groups: number;
  evaluated_groups: number;
  failed_groups: number;
  total_questions: number;
  evaluated_questions: number;
  correct_questions: number;
  micro_accuracy: number;
  macro_accuracy: number;
  group_coverage: number;
  question_coverage: number;
};

type Execution = Selection & {
  group_id: string;
  questions: number;
  status: string;
  ineligible_reason?: string;
  task_id?: string;
  run_id?: string;
  error?: string;
  evaluated_questions: number;
  correct_questions: number;
  accuracy: number;
};

type Campaign = {
  id: string;
  request: {
    name: string;
    selections: Selection[];
    recipe: {
      mode: string;
      model?: string;
      reader_model?: string;
      max_rounds?: number;
    };
  };
  preview: Preview;
  status: string;
  summary: Summary;
  executions: Execution[];
  created_at: string;
  updated_at: string;
};

type ListResponse<T> = {
  items: T[];
};

const API = "/v1/knowledge-eval";
const REFRESH_MS = 5_000;

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

function selectionKey(selection: Selection): string {
  return `${selection.dataset}/${selection.partition}`;
}

function formatPercent(value: number): string {
  return `${Math.round(value * 1_000) / 10}%`;
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "medium",
    timeStyle: "medium",
  }).format(new Date(value));
}

export default function CohortPage() {
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [selectedKeys, setSelectedKeys] = useState<string[]>([]);
  const [name, setName] = useState("All holdout candidate");
  const [model, setModel] = useState("deepseek-v4-flash");
  const [readerModel, setReaderModel] = useState("deepseek-v4-flash");
  const [maxRounds, setMaxRounds] = useState(30);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [callLimit, setCallLimit] = useState(0);
  const [confirmed, setConfirmed] = useState(false);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [detail, setDetail] = useState<Campaign | null>(null);

  const loadCatalog = useCallback(async () => {
    const [datasetResponse, modelResponse] = await Promise.all([
      getJSON<ListResponse<Dataset>>("/datasets?limit=50"),
      getJSON<ListResponse<Model>>("/experiment-models?limit=50"),
    ]);
    setDatasets(datasetResponse.items);
    setModels(modelResponse.items);
    setSelectedKeys((current) => {
      if (current.length > 0) return current;
      return datasetResponse.items.flatMap((dataset) =>
        dataset.partitions
          .filter((partition) => partition.name === "holdout")
          .map((partition) => selectionKey({ dataset: dataset.id, partition: partition.name })),
      );
    });
  }, []);

  const loadCampaigns = useCallback(async () => {
    const response = await getJSON<ListResponse<Campaign>>("/cohort-campaigns?limit=50");
    setCampaigns(response.items);
  }, []);

  useEffect(() => {
    const startup = window.setTimeout(() => {
      void Promise.all([loadCatalog(), loadCampaigns()]).catch((error: unknown) => {
        setMessage(error instanceof Error ? error.message : "Unable to load cohort API");
      });
    }, 0);
    const timer = window.setInterval(() => {
      void loadCampaigns().catch(() => undefined);
    }, REFRESH_MS);
    return () => {
      window.clearTimeout(startup);
      window.clearInterval(timer);
    };
  }, [loadCampaigns, loadCatalog]);

  const selections = useMemo<Selection[]>(() => selectedKeys.map((key) => {
    const separator = key.indexOf("/");
    return { dataset: key.slice(0, separator), partition: key.slice(separator + 1) };
  }), [selectedKeys]);

  const requestBody = useCallback((confirmPaid: boolean) => ({
    name,
    selections,
    recipe: {
      mode: "maintainer",
      model,
      reader_model: readerModel,
      max_rounds: maxRounds,
    },
    confirm_paid: confirmPaid,
    llm_call_limit: confirmPaid ? callLimit : 0,
  }), [callLimit, maxRounds, model, name, readerModel, selections]);

  const previewCampaign = useCallback(async () => {
    setBusy(true);
    setMessage("");
    try {
      const response = await postExperimentJSON<{ preview: Preview }>(
        "/cohort-campaigns/preview",
        requestBody(false),
      );
      setPreview(response.preview);
      setCallLimit(response.preview.max_llm_calls);
      setConfirmed(false);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Cohort preview failed");
    } finally {
      setBusy(false);
    }
  }, [requestBody]);

  const createCampaign = useCallback(async () => {
    if (!preview || !confirmed) return;
    setBusy(true);
    setMessage("");
    try {
      const response = await postExperimentJSON<{ campaign: Campaign }>(
        "/cohort-campaigns",
        requestBody(true),
        createExperimentIdempotencyKey(),
      );
      setMessage(`Cohort ${response.campaign.id} 已创建。`);
      setPreview(null);
      setConfirmed(false);
      await loadCampaigns();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Cohort creation failed");
    } finally {
      setBusy(false);
    }
  }, [confirmed, loadCampaigns, preview, requestBody]);

  const loadDetail = useCallback(async (campaignID: string) => {
    setBusy(true);
    try {
      const response = await getJSON<{ campaign: Campaign }>(
        `/cohort-campaigns/${encodeURIComponent(campaignID)}`,
      );
      setDetail(response.campaign);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Unable to load cohort groups");
    } finally {
      setBusy(false);
    }
  }, []);

  const cancelCampaign = useCallback(async (campaignID: string) => {
    setBusy(true);
    try {
      await postExperimentJSON(`/cohort-campaigns/${encodeURIComponent(campaignID)}/cancel`, {});
      await loadCampaigns();
      if (detail?.id === campaignID) setDetail(null);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Unable to cancel cohort");
    } finally {
      setBusy(false);
    }
  }, [detail, loadCampaigns]);

  return (
    <main className="cohort-page">
      <header className="topbar">
        <a className="brand" href="/">
          <span className="brand-mark">KE</span>
          <span><strong>Knowledge Eval Registry</strong><small>cohort campaigns</small></span>
        </a>
        <a className="button secondary" href="/">返回 Dashboard</a>
      </header>

      <section className="cohort-hero">
        <div>
          <p className="eyebrow">FULL DATASET EVALUATION</p>
          <h1>Cohort Campaigns</h1>
          <p>冻结 Dataset/Partition 和方案组合，按 Group 顺序执行全部问题，并汇总 Micro、Macro 与 Coverage。</p>
        </div>
        <div className="cohort-guardrail">
          <strong>付费保护</strong>
          <span>必须先 Preview，再以相同或更高的 LLM call ceiling 明确确认。</span>
        </div>
      </section>

      <section className="content-section cohort-builder">
        <div className="section-heading compact">
          <div><p className="eyebrow">PLAN</p><h2>生成完整 Cohort</h2></div>
          <span>{selections.length} dataset partitions selected</span>
        </div>
        <div className="cohort-form-grid">
          <label><span>Campaign name</span><input value={name} onChange={(event) => { setName(event.target.value); setPreview(null); }} /></label>
          <label><span>Artifact model</span><select value={model} onChange={(event) => { setModel(event.target.value); setPreview(null); }}>{models.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
          <label><span>Reader model</span><select value={readerModel} onChange={(event) => { setReaderModel(event.target.value); setPreview(null); }}>{models.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
          <label><span>Max builder rounds / Group</span><input type="number" min={1} max={200} value={maxRounds} onChange={(event) => { setMaxRounds(Number(event.target.value)); setPreview(null); }} /></label>
        </div>
        <fieldset className="cohort-datasets">
          <legend>Dataset partitions</legend>
          {datasets.flatMap((dataset) => dataset.partitions.map((partition) => {
            const key = selectionKey({ dataset: dataset.id, partition: partition.name });
            return (
              <label key={key}>
                <input
                  type="checkbox"
                  checked={selectedKeys.includes(key)}
                  onChange={(event) => {
                    setSelectedKeys((current) => event.target.checked
                      ? [...current, key]
                      : current.filter((item) => item !== key));
                    setPreview(null);
                  }}
                />
                <span><strong>{dataset.name} / {partition.name}</strong><small>{partition.group_count} groups · {partition.run_group_count} previously run</small></span>
              </label>
            );
          }))}
        </fieldset>
        <div className="cohort-actions">
          <button className="button primary" type="button" disabled={busy || selections.length === 0} onClick={() => void previewCampaign()}>{busy ? "Working…" : "Preview campaign"}</button>
          {message && <span className="task-note">{message}</span>}
        </div>

        {preview && (
          <div className="cohort-preview">
            <div className="cohort-stat-grid">
              <div><span>Groups</span><strong>{preview.eligible_groups} / {preview.total_groups}</strong><small>{preview.ineligible_groups} ineligible</small></div>
              <div><span>Questions</span><strong>{preview.planned_questions.toLocaleString()}</strong><small>{preview.total_questions.toLocaleString()} total</small></div>
              <div><span>Child tasks</span><strong>{preview.planned_tasks.toLocaleString()}</strong><small>sequential and resumable</small></div>
              <div><span>Maximum LLM calls</span><strong>{preview.max_llm_calls.toLocaleString()}</strong><small>hard ceiling required</small></div>
            </div>
            {preview.issues.length > 0 && <div className="cohort-issues"><strong>Coverage gaps</strong>{preview.issues.slice(0, 8).map((issue) => <p key={`${issue.dataset}/${issue.partition}/${issue.group_id ?? issue.reason}`}><code>{issue.dataset}/{issue.partition}{issue.group_id ? `/${issue.group_id}` : ""}</code> {issue.reason}</p>)}</div>}
            <div className="cohort-confirm">
              <label><span>LLM call ceiling</span><input type="number" min={preview.max_llm_calls} value={callLimit} onChange={(event) => setCallLimit(Number(event.target.value))} /></label>
              <label className="cohort-check"><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} /><span>我确认这是付费全量任务，并接受以上最大调用数。</span></label>
              <button className="button primary" type="button" disabled={busy || !confirmed || callLimit < preview.max_llm_calls} onClick={() => void createCampaign()}>Create Cohort</button>
            </div>
          </div>
        )}
      </section>

      <section className="content-section cohort-history">
        <div className="section-heading compact"><div><p className="eyebrow">RESULTS</p><h2>Campaign 汇总</h2></div><span>{campaigns.length} campaigns</span></div>
        <div className="cohort-list">
          {campaigns.map((campaign) => (
            <article key={campaign.id}>
              <header><div><span className={`status ${campaign.status === "completed" ? "success" : campaign.status === "running" ? "warning" : "neutral"}`}>{campaign.status}</span><h3>{campaign.request.name}</h3><code>{campaign.id}</code></div><small>{formatDate(campaign.updated_at)}</small></header>
              <div className="cohort-stat-grid compact">
                <div><span>Micro</span><strong>{formatPercent(campaign.summary.micro_accuracy)}</strong></div>
                <div><span>Macro</span><strong>{formatPercent(campaign.summary.macro_accuracy)}</strong></div>
                <div><span>Group coverage</span><strong>{campaign.summary.evaluated_groups}/{campaign.summary.total_groups}</strong></div>
                <div><span>Questions</span><strong>{campaign.summary.correct_questions}/{campaign.summary.evaluated_questions}</strong></div>
              </div>
              <footer>
                <button className="button secondary" type="button" disabled={busy} onClick={() => void loadDetail(campaign.id)}>查看 Groups</button>
                {(campaign.status === "queued" || campaign.status === "running") && <button className="button secondary" type="button" disabled={busy} onClick={() => void cancelCampaign(campaign.id)}>Cancel</button>}
              </footer>
            </article>
          ))}
          {campaigns.length === 0 && <div className="loading-panel">No Cohort Campaign yet. Preview does not start paid work.</div>}
        </div>
      </section>

      {detail && (
        <section className="content-section cohort-detail">
          <div className="section-heading compact"><div><p className="eyebrow">GROUP BREAKDOWN</p><h2>{detail.request.name}</h2></div><button className="button secondary" type="button" onClick={() => setDetail(null)}>Close</button></div>
          <div className="cohort-table" role="table" aria-label="Cohort group results">
            <div className="cohort-table-row cohort-table-head" role="row"><span>Group</span><span>Status</span><span>Questions</span><span>Accuracy</span><span>Run</span></div>
            {detail.executions.map((execution) => (
              <div className="cohort-table-row" role="row" key={`${execution.dataset}/${execution.partition}/${execution.group_id}`}>
                <span><strong>{execution.group_id}</strong><small>{execution.dataset}/{execution.partition}</small></span>
                <span>{execution.status}<small>{execution.ineligible_reason ?? execution.error ?? ""}</small></span>
                <span>{execution.correct_questions}/{execution.evaluated_questions || execution.questions}</span>
                <span>{execution.evaluated_questions > 0 ? formatPercent(execution.accuracy) : "—"}</span>
                <span>{execution.run_id ? <a href={`/run?run=${encodeURIComponent(execution.run_id)}`}>Open run</a> : execution.task_id ?? "—"}</span>
              </div>
            ))}
          </div>
        </section>
      )}
    </main>
  );
}
