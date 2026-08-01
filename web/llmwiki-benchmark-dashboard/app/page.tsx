"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  createExperimentIdempotencyKey,
  postExperimentJSON,
} from "./experiment-api";

type PageInfo = {
  limit: number;
  next_cursor?: string;
};

type Solution = {
  id: string;
  builder_id: string;
  builder_version: string;
  code_revision: string;
  model?: string;
  config_digest?: string;
};

type DatasetPartition = {
  name: string;
  group_count: number;
  run_group_count: number;
};

type DatasetFamily = {
  id: string;
  name: string;
  revision?: string;
  license?: string;
  group_kind: string;
  group_count: number;
  run_group_count: number;
  run_count: number;
  artifact_count: number;
  partitions: DatasetPartition[];
};

type DatasetSource = {
  id: string;
  name: string;
  provider: string;
  repository: string;
  revision: string;
  license: string;
  download_size: string;
  data_root: string;
  downloaded: boolean;
  prepared: boolean;
  install_status: string;
  note?: string;
};

type DatasetInstallTask = {
  id: string;
  dataset: string;
  status: string;
  data_root: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
  cancellation_requested: boolean;
  error?: string;
  events: Array<{ status: string; message: string; created_at: string }>;
};

type DatasetGroup = {
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

type Run = {
  id: string;
  dataset_id: string;
  dataset: string;
  partition: string;
  case_id: string;
  solution_version_id: string;
  artifact_id: string;
  status: string;
  created_at: string;
  completed_at?: string;
  benchmark_ids: string[];
  metadata?: Record<string, string>;
};

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
  metrics?: Metric[];
};

type Artifact = {
  artifact_id: string;
  product: string;
  kind: string;
  role: string;
  dataset_id: string;
  solution_version_id: string;
  sha256: string;
  created_at: string;
  views: Record<string, string>;
};

type RunDetail = {
  run: Run;
  trials: Trial[];
  artifact?: Artifact;
};

type Benchmark = {
  id: string;
  name: string;
  description: string;
  primary_metric: string;
  executed: boolean;
};

type MatrixCell = {
  benchmark_id: string;
  executed: boolean;
  score?: number;
  metrics?: Metric[];
};

type MatrixRow = {
  run_id: string;
  solution_version_id: string;
  artifact_id: string;
  cells: MatrixCell[];
};

type Matrix = {
  benchmarks: Benchmark[];
  rows: MatrixRow[];
};

type ExperimentTaskRequest = {
  dataset: string;
  partition: string;
  group_id: string;
  mode: "baseline" | "maintainer";
  model?: string;
  reader_model?: string;
  question_limit: number;
  question_offset?: number;
  max_rounds: number;
  confirm_paid?: boolean;
  reuse_artifact_from_task_id?: string;
};

type ExperimentTaskPreview = {
  eligible: boolean;
  ineligible_reason?: string;
  paid: boolean;
  llm_configured: boolean;
  dataset: string;
  partition: string;
  group_id: string;
  source_kind: string;
  available_questions: number;
  selected_questions: number;
  cumulative_questions: number;
  planned_runs: number;
  max_llm_calls: number;
  benchmarks: string[];
  includes_source_only: boolean;
  includes_maintainer: boolean;
};

type ExperimentTask = {
  id: string;
  request: ExperimentTaskRequest;
  preview: ExperimentTaskPreview;
  status: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
  cancellation_requested: boolean;
  error?: string;
  run_ids: string[];
  artifact_ids: string[];
  result_path?: string;
  events: Array<{ status: string; message: string; created_at: string }>;
  retry_of_task_id?: string;
  continued_from_task_id?: string;
};

type ExperimentModel = {
  id: string;
  name: string;
  provider: string;
};

type ListResponse<T> = {
  items: T[];
  page: PageInfo;
};

type DashboardState = {
  solutions: Solution[];
  datasetFamilies: DatasetFamily[];
  datasetSources: DatasetSource[];
  datasetInstallTasks: DatasetInstallTask[];
  datasetPage: PageInfo;
  runs: Run[];
  runPage: PageInfo;
  details: Record<string, RunDetail>;
  benchmarks: Benchmark[];
  matrix: Matrix;
  tasks: ExperimentTask[];
  experimentModels: ExperimentModel[];
  loadedAt: Date;
};

const API = "/v1/knowledge-eval";
const PAGE_LIMIT = 50;
const REFRESH_MS = 10_000;

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

function metricValue(trial: Trial | undefined, name: string): number | undefined {
  return trial?.metrics?.find((metric) => metric.name === name)?.value;
}

function formatMetric(value: number | undefined, metric: string): string {
  if (value === undefined) {
    return "Not run";
  }
  return metric.includes("count") ? String(Math.round(value)) : `${Math.round(value * 100)}%`;
}

function shortDigest(value: string | undefined): string {
  if (!value) {
    return "—";
  }
  return `${value.slice(0, 8)}…`;
}

function formatDate(value: string | Date): string {
  return new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(typeof value === "string" ? new Date(value) : value);
}

function solutionName(solution: Solution | undefined): string {
  if (!solution) {
    return "Unknown solution";
  }
  if (solution.builder_id === "llmwiki-directory") {
    return "Source-only baseline";
  }
  if (solution.builder_id === "llmwiki-maintainer") {
    return "LLM Wiki maintainer";
  }
  return solution.builder_id;
}

function taskRunScoreLabel(solution: Solution | undefined): string {
  if (solution?.builder_id === "llmwiki-directory") {
    return "Source-only baseline · scores ↗";
  }
  if (solution?.builder_id === "llmwiki-maintainer") {
    return "Maintained Wiki · scores ↗";
  }
  return solution ? `${solutionName(solution)} · scores ↗` : "Run & scores ↗";
}

export default function Home() {
  const [state, setState] = useState<DashboardState | null>(null);
  const [groups, setGroups] = useState<DatasetGroup[]>([]);
  const [groupPage, setGroupPage] = useState<PageInfo>({ limit: PAGE_LIMIT });
  const [selectedDataset, setSelectedDataset] = useState("");
  const [selectedPartition, setSelectedPartition] = useState("");
  const [groupsLoading, setGroupsLoading] = useState(false);
  const [taskDataset, setTaskDataset] = useState("");
  const [taskPartition, setTaskPartition] = useState("");
  const [taskGroups, setTaskGroups] = useState<DatasetGroup[]>([]);
  const [error, setError] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [taskGroupID, setTaskGroupID] = useState("");
  const [taskMode, setTaskMode] = useState<"baseline" | "maintainer">("baseline");
  const [taskModel, setTaskModel] = useState("deepseek-v4-pro");
  const [taskReaderModel, setTaskReaderModel] = useState("deepseek-v4-pro");
  const [taskQuestionLimit, setTaskQuestionLimit] = useState(5);
  const [taskMaxRounds, setTaskMaxRounds] = useState(30);
  const [taskConfirmPaid, setTaskConfirmPaid] = useState(false);
  const [taskPreview, setTaskPreview] = useState<ExperimentTaskPreview | null>(null);
  const [taskBusy, setTaskBusy] = useState(false);
  const [installBusy, setInstallBusy] = useState("");
  const [installMessage, setInstallMessage] = useState("");
  const [taskMessage, setTaskMessage] = useState("");
  const [continueRoundsByTask, setContinueRoundsByTask] = useState<Record<string, number>>({});
  const [additionalQuestionsByTask, setAdditionalQuestionsByTask] = useState<Record<string, number>>({});
  const taskFamily = state?.datasetFamilies.find(
    (family) => family.id === taskDataset,
  );
  const taskPartitionKey = taskFamily?.partitions
    .map((partition) => partition.name)
    .sort((left, right) => {
      if (left === "train") return -1;
      if (right === "train") return 1;
      return left.localeCompare(right);
    })
    .join("|") ?? "";

  const invalidateTaskPreview = () => {
    setTaskPreview(null);
    setTaskConfirmPaid(false);
    setTaskMessage("");
  };

  const load = useCallback(async () => {
    setRefreshing(true);
    try {
      const [
        solutions,
        datasets,
        datasetSources,
        datasetInstallTasks,
        runs,
        benchmarks,
        matrix,
        tasks,
        experimentModels,
      ] = await Promise.all([
        getJSON<ListResponse<Solution>>(`/solutions?limit=${PAGE_LIMIT}`),
        getJSON<ListResponse<DatasetFamily>>(`/datasets?limit=${PAGE_LIMIT}`),
        getJSON<ListResponse<DatasetSource>>(`/dataset-sources?limit=${PAGE_LIMIT}`),
        getJSON<ListResponse<DatasetInstallTask>>(`/dataset-install-tasks?limit=${PAGE_LIMIT}`),
        getJSON<ListResponse<Run>>(`/runs?limit=${PAGE_LIMIT}`),
        getJSON<ListResponse<Benchmark>>(`/benchmarks?limit=${PAGE_LIMIT}`),
        getJSON<Matrix>("/results/matrix"),
        getJSON<ListResponse<ExperimentTask>>(`/experiment-tasks?limit=${PAGE_LIMIT}`),
        getJSON<ListResponse<ExperimentModel>>(`/experiment-models?limit=${PAGE_LIMIT}`),
      ]);
      const details = await Promise.all(
        runs.items.map((run) => getJSON<RunDetail>(`/runs/${encodeURIComponent(run.id)}`)),
      );
      const defaultFamily = datasets.items.find((item) => item.id === "locomo")
        ?? datasets.items[0];
      if (defaultFamily) {
        const defaultPartition = defaultFamily.partitions.find(
          (item) => item.name === "train",
        )?.name ?? defaultFamily.partitions[0]?.name ?? "";
        setSelectedDataset((current) => current || defaultFamily.id);
        setSelectedPartition((current) => current || defaultPartition);
        setTaskDataset((current) => current || defaultFamily.id);
        setTaskPartition((current) => current || defaultPartition);
      }
      setState({
        solutions: solutions.items,
        datasetFamilies: datasets.items,
        datasetSources: datasetSources.items,
        datasetInstallTasks: datasetInstallTasks.items,
        datasetPage: datasets.page,
        runs: runs.items,
        runPage: runs.page,
        details: Object.fromEntries(details.map((detail) => [detail.run.id, detail])),
        benchmarks: benchmarks.items,
        matrix,
        tasks: tasks.items,
        experimentModels: experimentModels.items,
        loadedAt: new Date(),
      });
      setError("");
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Unknown API error");
    } finally {
      setRefreshing(false);
    }
  }, []);

  const loadGroups = useCallback(async (
    dataset: string,
    partition: string,
    cursor = "",
  ) => {
    if (!dataset || !partition) {
      return;
    }
    setGroupsLoading(true);
    try {
      const query = new URLSearchParams({
        partition,
        limit: String(PAGE_LIMIT),
      });
      if (cursor) {
        query.set("cursor", cursor);
      }
      const response = await getJSON<ListResponse<DatasetGroup>>(
        `/datasets/${encodeURIComponent(dataset)}/groups?${query}`,
      );
      setGroups((current) => cursor ? [...current, ...response.items] : response.items);
      setGroupPage(response.page);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Unknown API error");
    } finally {
      setGroupsLoading(false);
    }
  }, []);

  const loadTaskGroups = useCallback(async (dataset: string, partitions: string[]) => {
    if (!dataset || partitions.length === 0) {
      setTaskGroups([]);
      return;
    }
    try {
      const partitionGroups = await Promise.all(partitions.map(async (partition) => {
        const groupsForPartition: DatasetGroup[] = [];
        let cursor = "";
        do {
          const query = new URLSearchParams({
            partition,
            limit: String(PAGE_LIMIT),
          });
          if (cursor) {
            query.set("cursor", cursor);
          }
          const response = await getJSON<ListResponse<DatasetGroup>>(
            `/datasets/${encodeURIComponent(dataset)}/groups?${query}`,
          );
          groupsForPartition.push(...response.items);
          cursor = response.page.next_cursor ?? "";
        } while (cursor);
        return groupsForPartition;
      }));
      const allGroups = partitionGroups.flat();

      setTaskGroups(allGroups);
      setTaskGroupID(allGroups[0]?.case_id ?? "");
      setTaskPartition(allGroups[0]?.partition ?? "");
      const firstGroupQuestionCount = allGroups[0]?.evaluation_cases;
      if (firstGroupQuestionCount) {
        setTaskQuestionLimit((current) => Math.min(current, firstGroupQuestionCount));
      }
    } catch (loadError) {
      setTaskMessage(
        loadError instanceof Error ? loadError.message : "Unable to load task groups",
      );
    }
  }, []);

  useEffect(() => {
    const initial = window.setTimeout(
      () => void loadGroups(selectedDataset, selectedPartition),
      0,
    );
    const timer = window.setInterval(
      () => void loadGroups(selectedDataset, selectedPartition),
      REFRESH_MS,
    );
    return () => {
      window.clearTimeout(initial);
      window.clearInterval(timer);
    };
  }, [loadGroups, selectedDataset, selectedPartition]);

  useEffect(() => {
    const initial = window.setTimeout(
      () => void loadTaskGroups(
        taskDataset,
        taskPartitionKey ? taskPartitionKey.split("|") : [],
      ),
      0,
    );
    return () => window.clearTimeout(initial);
  }, [loadTaskGroups, taskDataset, taskPartitionKey]);

  useEffect(() => {
    const initial = window.setTimeout(() => void load(), 0);
    const timer = window.setInterval(() => void load(), REFRESH_MS);
    return () => {
      window.clearTimeout(initial);
      window.clearInterval(timer);
    };
  }, [load]);

  const solutionByID = useMemo(
    () => new Map(state?.solutions.map((solution) => [solution.id, solution]) ?? []),
    [state],
  );
  const runByID = useMemo(
    () => new Map(state?.runs.map((run) => [run.id, run]) ?? []),
    [state],
  );
  const maintained = state?.solutions.find(
    (solution) => solution.builder_id === "llmwiki-maintainer",
  );
  const selectedFamily = state?.datasetFamilies.find(
    (family) => family.id === selectedDataset,
  );
  const selectedTaskGroup = taskGroups.find(
    (group) => group.case_id === taskGroupID,
  );
  const taskQuestionTotal = selectedTaskGroup?.evaluation_cases;
  const taskRequest = useMemo<ExperimentTaskRequest>(() => ({
    dataset: taskDataset,
    partition: taskPartition,
    group_id: taskGroupID,
    mode: taskMode,
    model: taskMode === "maintainer" ? taskModel : undefined,
    reader_model: taskMode === "maintainer" ? taskReaderModel : undefined,
    question_limit: taskQuestionLimit,
    max_rounds: taskMaxRounds,
    confirm_paid: taskMode === "maintainer" ? taskConfirmPaid : undefined,
  }), [
    taskDataset,
    taskPartition,
    taskGroupID,
    taskMode,
    taskModel,
    taskReaderModel,
    taskQuestionLimit,
    taskMaxRounds,
    taskConfirmPaid,
  ]);

  const previewTask = useCallback(async () => {
    setTaskBusy(true);
    try {
      const response = await postExperimentJSON<{ preview: ExperimentTaskPreview }>(
        "/experiment-tasks/preview",
        taskRequest,
      );
      setTaskPreview(response.preview);
      setTaskMessage("");
    } catch (previewError) {
      setTaskMessage(previewError instanceof Error ? previewError.message : "Preview failed");
    } finally {
      setTaskBusy(false);
    }
  }, [taskRequest]);

  const createTask = useCallback(async () => {
    setTaskBusy(true);
    try {
      const response = await postExperimentJSON<{ task: ExperimentTask }>(
        "/experiment-tasks",
        taskRequest,
        createExperimentIdempotencyKey(),
      );
      setTaskMessage(`任务 ${response.task.id} 已进入队列。`);
      setTaskPreview(null);
      await load();
    } catch (createError) {
      setTaskMessage(createError instanceof Error ? createError.message : "Create task failed");
    } finally {
      setTaskBusy(false);
    }
  }, [load, taskRequest]);

  const cancelTask = useCallback(async (taskID: string) => {
    setTaskBusy(true);
    try {
      await postExperimentJSON(`/experiment-tasks/${encodeURIComponent(taskID)}/cancel`, {});
      setTaskMessage(`已请求取消 ${taskID}。`);
      await load();
    } catch (cancelError) {
      setTaskMessage(cancelError instanceof Error ? cancelError.message : "Cancel failed");
    } finally {
      setTaskBusy(false);
    }
  }, [load]);

  const retryTask = useCallback(async (task: ExperimentTask) => {
    if (
      task.request.mode === "maintainer"
      && !window.confirm("Retry 会按原配置再次调用付费 LLM。确认重新入队？")
    ) {
      return;
    }
    setTaskBusy(true);
    try {
      const response = await postExperimentJSON<{ task: ExperimentTask }>(
        `/experiment-tasks/${encodeURIComponent(task.id)}/retry`,
        {},
        createExperimentIdempotencyKey(),
      );
      setTaskMessage(`Retry 已创建新任务 ${response.task.id}，原任务 ${task.id} 保留。`);
      await load();
    } catch (retryError) {
      setTaskMessage(retryError instanceof Error ? retryError.message : "Retry failed");
    } finally {
      setTaskBusy(false);
    }
  }, [load]);

  const continueTask = useCallback(async (
    task: ExperimentTask,
    additionalRounds: number,
  ) => {
    if (!window.confirm(
      `Continue 会从现有 Maintainer 工作区接着运行，最多追加 ${additionalRounds} 轮付费 LLM 调用。确认入队？`,
    )) {
      return;
    }
    setTaskBusy(true);
    try {
      const response = await postExperimentJSON<{ task: ExperimentTask }>(
        `/experiment-tasks/${encodeURIComponent(task.id)}/continue`,
        { additional_rounds: additionalRounds },
        createExperimentIdempotencyKey(),
      );
      setTaskMessage(
        `已从 ${task.id} 继续为新任务 ${response.task.id}（最多追加 ${additionalRounds} 轮）。`,
      );
      await load();
    } catch (continueError) {
      setTaskMessage(
        continueError instanceof Error ? continueError.message : "Continue failed",
      );
    } finally {
      setTaskBusy(false);
    }
  }, [load]);

  const evaluateMoreQuestions = useCallback(async (
    task: ExperimentTask,
    additionalQuestions: number,
    artifactID: string,
  ) => {
    const evaluated = (task.request.question_offset ?? 0) + task.preview.selected_questions;
    if (!window.confirm(
      `将复用 artifact ${artifactID}，从第 ${evaluated + 1} 道开始追加 ${additionalQuestions} 道题。不会再次运行 Wiki maintainer，只会产生 reader 调用。确认入队？`,
    )) {
      return;
    }
    setTaskBusy(true);
    try {
      const response = await postExperimentJSON<{ task: ExperimentTask }>(
        `/experiment-tasks/${encodeURIComponent(task.id)}/continue`,
        { additional_questions: additionalQuestions },
        createExperimentIdempotencyKey(),
      );
      setTaskMessage(
        `已复用 ${artifactID} 创建 ${response.task.id}，追加 ${additionalQuestions} 道题。`,
      );
      await load();
    } catch (continueError) {
      setTaskMessage(
        continueError instanceof Error ? continueError.message : "Add questions failed",
      );
    } finally {
      setTaskBusy(false);
    }
  }, [load]);

  const installDataset = useCallback(async (source: DatasetSource) => {
    const action = source.prepared ? "重新下载、校验并生成 prepared dataset" : "下载并生成 prepared dataset";
    if (!window.confirm(
      `${action}：${source.name}\n预计下载 ${source.download_size}\n保存到 ${source.data_root}\n\n确认创建后台任务？`,
    )) {
      return;
    }
    setInstallBusy(source.id);
    try {
      const response = await postExperimentJSON<{ task: DatasetInstallTask }>(
        "/dataset-install-tasks",
        { dataset: source.id },
        createExperimentIdempotencyKey(),
      );
      setInstallMessage(`数据任务 ${response.task.id} 已进入队列。`);
      await load();
    } catch (installError) {
      setInstallMessage(
        installError instanceof Error ? installError.message : "Dataset install failed",
      );
    } finally {
      setInstallBusy("");
    }
  }, [load]);

  const cancelDatasetInstall = useCallback(async (taskID: string) => {
    setInstallBusy(taskID);
    try {
      await postExperimentJSON(`/dataset-install-tasks/${encodeURIComponent(taskID)}/cancel`, {});
      setInstallMessage(`已请求取消 ${taskID}。`);
      await load();
    } catch (cancelError) {
      setInstallMessage(cancelError instanceof Error ? cancelError.message : "Cancel failed");
    } finally {
      setInstallBusy("");
    }
  }, [load]);

  return (
    <main>
      <header className="topbar">
        <a className="brand" href="#top" aria-label="Knowledge Eval Registry home">
          <span className="brand-mark">KE</span>
          <span>
            <strong>Knowledge Eval Registry</strong>
            <small>live local API</small>
          </span>
        </a>
        <nav aria-label="Page sections">
          <a href="#datasets">Datasets</a>
          <a href="#tasks">Run experiments</a>
          <a href="/cohort">Cohorts</a>
          <a href="#artifacts">Experiments</a>
          <a href="#benchmarks">Benchmarks</a>
        </nav>
        <button
          className="refresh-button"
          type="button"
          onClick={() => {
            void load();
            void loadGroups(selectedDataset, selectedPartition);
          }}
        >
          {refreshing ? "Refreshing…" : "Refresh"}
        </button>
      </header>

      <section className="intro" id="top">
        <div>
          <p className="eyebrow">LIVE EVALUATION REGISTRY</p>
          <h1>我跑了什么，<br />结果和制品在哪里。</h1>
          <p className="intro-copy">
            页面通过本地 Query API 获取 Dataset、Group、Run 和 Benchmark。
            prepared 数据新增后会直接进入目录；跑完实验后，状态和制品在原位更新。
          </p>
          {error && (
            <div className="api-error" role="alert">
              <strong>Query API unavailable</strong>
              <span>{error}</span>
              <code>go run ./cmd/knowledge-eval-api</code>
            </div>
          )}
        </div>
        <dl className="snapshot-summary">
          <div>
            <dt>Datasets</dt>
            <dd>{state?.datasetFamilies.length ?? "—"}</dd>
          </div>
          <div>
            <dt>Groups</dt>
            <dd>{state?.datasetFamilies.reduce((sum, item) => sum + item.group_count, 0) ?? "—"}</dd>
          </div>
          <div>
            <dt>Benchmarks</dt>
            <dd>{state?.benchmarks.filter((item) => item.executed).length ?? "—"} / {state?.benchmarks.length ?? "—"}</dd>
          </div>
          <div>
            <dt>API status</dt>
            <dd className="status-value">{error ? "Offline" : state ? "Live" : "Loading"}</dd>
          </div>
        </dl>
      </section>

      <section className="version-strip" aria-labelledby="version-title">
        <div>
          <p className="eyebrow">CURRENT SOLUTION</p>
          <h2 id="version-title">{solutionName(maintained)}</h2>
        </div>
        <dl>
          <div>
            <dt>Builder</dt>
            <dd>{maintained?.builder_id ?? "—"} <span>@{maintained?.builder_version ?? "—"}</span></dd>
          </div>
          <div>
            <dt>Model</dt>
            <dd>{maintained?.model ?? "—"}</dd>
          </div>
          <div>
            <dt>Code revision</dt>
            <dd>{maintained?.code_revision ?? "—"}</dd>
          </div>
          <div>
            <dt>Config</dt>
            <dd>{shortDigest(maintained?.config_digest)}</dd>
          </div>
          <div>
            <dt>Last API refresh</dt>
            <dd>{state ? formatDate(state.loadedAt) : "Loading…"}</dd>
          </div>
        </dl>
      </section>

      <section className="content-section" id="datasets">
        <div className="section-heading">
          <div>
            <p className="eyebrow">DATASETS</p>
            <h2>数据集与 Groups</h2>
          </div>
          <p>先选择 Dataset 和 partition，再查看其中所有已运行与未运行的 Group。</p>
        </div>
        <div className="dataset-installer" aria-label="Dataset installation">
          <header>
            <div>
              <p className="eyebrow">LOCAL DATA</p>
              <h3>下载并准备数据</h3>
            </div>
            <div className="dataset-root">
              <span>存储目录</span>
              <code>{state?.datasetSources[0]?.data_root ?? "Loading…"}</code>
            </div>
          </header>
          <div className="dataset-source-grid">
            {state?.datasetSources.map((source) => {
              const activeTask = state.datasetInstallTasks.find(
                (task) => task.dataset === source.id
                  && (task.status === "queued" || task.status === "running"),
              );
              return (
                <article key={source.id}>
                  <div className="dataset-source-title">
                    <div>
                      <span>{source.provider}</span>
                      <h4>{source.name}</h4>
                    </div>
                    <span className={`status ${source.prepared ? "success" : source.downloaded ? "warning" : "neutral"}`}>
                      {activeTask
                        ? activeTask.status
                        : source.prepared
                          ? "Ready"
                          : source.downloaded
                            ? "Downloaded"
                            : "Not installed"}
                    </span>
                  </div>
                  <p>{source.repository}</p>
                  <dl>
                    <div><dt>Download</dt><dd>{source.download_size}</dd></div>
                    <div><dt>License</dt><dd>{source.license}</dd></div>
                  </dl>
                  {source.note && <small>{source.note}</small>}
                  <div className="dataset-source-action">
                    <code>{shortDigest(source.revision)}</code>
                    <button
                      className="button primary"
                      disabled={Boolean(activeTask) || Boolean(installBusy)}
                      onClick={() => void installDataset(source)}
                      type="button"
                    >
                      {activeTask
                        ? activeTask.status === "queued" ? "等待下载" : "正在下载和准备"
                        : source.prepared ? "重新安装" : "下载并准备"}
                    </button>
                  </div>
                </article>
              );
            })}
          </div>
          {installMessage && <p className="dataset-install-message">{installMessage}</p>}
          {state && state.datasetInstallTasks.length > 0 && (
            <div className="dataset-install-history">
              {state.datasetInstallTasks.slice(0, 5).map((task) => (
                <div key={task.id}>
                  <span className={`status ${task.status === "completed" ? "success" : task.status === "failed" ? "warning" : "neutral"}`}>
                    {task.status}
                  </span>
                  <strong>{task.dataset}</strong>
                  <code>{task.id}</code>
                  <small>{task.events.at(-1)?.message ?? formatDate(task.updated_at)}</small>
                  {(task.status === "queued" || task.status === "running") && (
                    <button
                      disabled={Boolean(installBusy)}
                      onClick={() => void cancelDatasetInstall(task.id)}
                      type="button"
                    >
                      停止
                    </button>
                  )}
                  {task.error && <p>{task.error}</p>}
                </div>
              ))}
            </div>
          )}
        </div>
        <div className="dataset-family-grid" aria-label="Dataset catalog">
          {state?.datasetFamilies.map((family) => (
            <article key={family.id}>
              <header>
                <span>Dataset</span>
                <code>{family.id}</code>
              </header>
              <h3>{family.name}</h3>
              <p>Group 类型：<strong>{family.group_kind}</strong></p>
              <dl>
                <div><dt>Groups</dt><dd>{family.group_count}</dd></div>
                <div><dt>Run groups</dt><dd>{family.run_group_count}</dd></div>
                <div><dt>Runs</dt><dd>{family.run_count}</dd></div>
                <div><dt>Artifacts</dt><dd>{family.artifact_count}</dd></div>
              </dl>
              <div className="dataset-partition-summary">
                {family.partitions.map((partition) => (
                  <span key={partition.name}>
                    {partition.name} · {partition.group_count}
                  </span>
                ))}
              </div>
              <small>revision {shortDigest(family.revision)}</small>
            </article>
          ))}
          {!state && <div className="loading-panel">Loading datasets from Query API…</div>}
        </div>

        {selectedFamily && (
          <div className="dataset-catalog">
            <header className="dataset-catalog-head">
              <div>
                <p className="eyebrow">GROUP BROWSER</p>
                <h3>浏览数据世界</h3>
                <p>
                  选择 Dataset 和 partition；下方每一行都是一个独立 Group。
                </p>
                <label className="dataset-filter">
                  <span>Dataset</span>
                  <select
                    onChange={(event) => {
                      const family = state?.datasetFamilies.find(
                        (item) => item.id === event.target.value,
                      );
                      if (!family) {
                        return;
                      }
                      setSelectedDataset(family.id);
                      setSelectedPartition((current) =>
                        family.partitions.some((item) => item.name === current)
                          ? current
                          : family.partitions.find((item) => item.name === "train")?.name
                            ?? family.partitions[0]?.name
                            ?? "");
                    }}
                    value={selectedDataset}
                  >
                    {state?.datasetFamilies.map((family) => (
                      <option key={family.id} value={family.id}>
                        {family.name}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <dl>
                <div><dt>All groups</dt><dd>{selectedFamily.group_count}</dd></div>
                <div><dt>Run groups</dt><dd>{selectedFamily.run_group_count}</dd></div>
                <div><dt>Runs</dt><dd>{selectedFamily.run_count}</dd></div>
                <div><dt>Artifacts</dt><dd>{selectedFamily.artifact_count}</dd></div>
              </dl>
            </header>

            <div className="partition-tabs" role="tablist" aria-label="Dataset partitions">
              {selectedFamily.partitions.map((partition) => (
                <button
                  aria-selected={partition.name === selectedPartition}
                  className={partition.name === selectedPartition ? "active" : ""}
                  key={partition.name}
                  onClick={() => {
                    setSelectedPartition(partition.name);
                  }}
                  role="tab"
                  type="button"
                >
                  {partition.name}
                  <span>{partition.group_count} groups · {partition.run_group_count} run</span>
                </button>
              ))}
            </div>

            <div className="group-table">
              <div className="group-table-row group-table-header">
                <span>Group</span>
                <span>Build input</span>
                <span>Evaluation</span>
                <span>Execution</span>
                <span />
              </div>
              {groups.map((group) => (
                <article className="group-table-row" key={group.id}>
                  <div className="group-identity">
                    <span className={`status ${group.status === "not_run" ? "neutral" : "success"}`}>
                      {group.status === "not_run" ? "Not run" : group.status}
                    </span>
                    <strong>{group.case_id}</strong>
                    <small>{group.group_kind} · {group.source_kind}</small>
                  </div>
                  <div className="group-stat">
                    <strong>
                      {group.trajectories > 0
                        ? `${group.trajectories} trajectories`
                        : `${group.sessions} sessions`}
                    </strong>
                    <small>{group.turns > 0 ? `${group.turns} turns` : `${group.sources} sources`}</small>
                  </div>
                  <div className="group-stat">
                    <strong>{group.evaluation_cases} cases</strong>
                    <small>
                      {group.case_ids.length > 1
                        ? `${group.case_ids.length} cases share this world`
                        : "one independent world"}
                    </small>
                  </div>
                  <div className="group-stat">
                    <strong>{group.run_count} runs</strong>
                    <small>{group.artifact_count} artifacts</small>
                  </div>
                  <div className="group-action">
                    <a
                      className="button secondary"
                      href={`/dataset?dataset=${encodeURIComponent(group.dataset)}&partition=${encodeURIComponent(group.partition)}&case=${encodeURIComponent(group.case_id)}`}
                    >
                      查看 Group
                    </a>
                    <small>{group.updated_at ? formatDate(group.updated_at) : "尚未运行"}</small>
                  </div>
                </article>
              ))}
              {groupsLoading && groups.length === 0 && (
                <div className="loading-panel">Loading groups from Query API…</div>
              )}
              {!groupsLoading && groups.length === 0 && (
                <div className="loading-panel">这个 partition 暂无 Group。</div>
              )}
            </div>
            {groupPage.next_cursor && (
              <button
                className="load-more"
                disabled={groupsLoading}
                onClick={() => void loadGroups(
                  selectedDataset,
                  selectedPartition,
                  groupPage.next_cursor,
                )}
                type="button"
              >
                {groupsLoading ? "Loading…" : "加载更多 Groups"}
              </button>
            )}
          </div>
        )}
      </section>

      <section className="content-section task-section" id="tasks">
        <div className="section-heading">
          <div>
            <p className="eyebrow">EXPERIMENT TASKS</p>
            <h2>创建和跟踪实验</h2>
          </div>
          <p>后端单并发执行。先预览范围；付费 LLM 任务必须再次确认。</p>
        </div>
        <div className="task-workbench">
          <form className="task-form" onSubmit={(event) => event.preventDefault()}>
            <div className="task-form-heading">
              <div>
                <span>New task</span>
                <h3>{taskDataset || "Dataset"} / {taskGroupID || "Group"}</h3>
              </div>
              <span className={`status ${taskMode === "maintainer" ? "warning" : "neutral"}`}>
                {taskMode === "maintainer" ? "Paid LLM" : "No LLM cost"}
              </span>
            </div>
            <div className="task-fields">
              <label>
                <span>Dataset</span>
                <select
                  value={taskDataset}
                  onChange={(event) => {
                    const family = state?.datasetFamilies.find(
                      (item) => item.id === event.target.value,
                    );
                    if (!family) return;
                    invalidateTaskPreview();
                    setTaskDataset(family.id);
                    setTaskPartition(
                      family.partitions.find((item) => item.name === "train")?.name
                        ?? family.partitions[0]?.name
                        ?? "",
                    );
                    setTaskGroupID("");
                    setTaskGroups([]);
                  }}
                >
                  {state?.datasetFamilies.map((family) => (
                    <option key={family.id} value={family.id}>{family.name}</option>
                  ))}
                </select>
              </label>
              <label>
                <span>Partition（随 Group）</span>
                <input
                  readOnly
                  value={taskPartition}
                />
              </label>
              <label className="wide">
                <span>Group ID</span>
                <select
                  value={taskGroupID}
                  disabled={taskGroups.length === 0}
                  onChange={(event) => {
                    invalidateTaskPreview();
                    const groupID = event.target.value;
                    const selectedGroup = taskGroups.find(
                      (group) => group.case_id === groupID,
                    );
                    setTaskGroupID(groupID);
                    if (selectedGroup) {
                      setTaskPartition(selectedGroup.partition);
                      setTaskQuestionLimit((current) => (
                        Math.min(current, selectedGroup.evaluation_cases)
                      ));
                    }
                  }}
                >
                  {taskGroups.map((group) => (
                    <option key={group.id} value={group.case_id}>
                      {group.case_id} · {group.partition} · {group.evaluation_cases} 道题
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>Solution arm</span>
                <select
                  value={taskMode}
                  onChange={(event) => {
                    invalidateTaskPreview();
                    setTaskMode(event.target.value as "baseline" | "maintainer");
                  }}
                >
                  <option value="baseline">Source-only baseline</option>
                  <option value="maintainer">Baseline + LLM Wiki maintainer</option>
                </select>
              </label>
              <label>
                <span>Questions</span>
                <input
                  min={1}
                  max={taskQuestionTotal}
                  type="number"
                  value={taskQuestionLimit}
                  onChange={(event) => {
                    invalidateTaskPreview();
                    const requested = Math.max(1, Number(event.target.value));
                    setTaskQuestionLimit(
                      taskQuestionTotal ? Math.min(requested, taskQuestionTotal) : requested,
                    );
                  }}
                />
                <small className="task-field-help">
                  本次运行 {taskQuestionLimit} / 总计 {taskQuestionTotal ?? "—"} 道
                </small>
              </label>
              {taskMode === "maintainer" && (
                <>
                  <label>
                    <span>Maintainer model</span>
                    <input
                      list="experiment-model-options"
                      value={taskModel}
                      onChange={(event) => {
                        invalidateTaskPreview();
                        setTaskModel(event.target.value);
                      }}
                    />
                  </label>
                  <label>
                    <span>Reader model</span>
                    <input
                      list="experiment-model-options"
                      value={taskReaderModel}
                      onChange={(event) => {
                        invalidateTaskPreview();
                        setTaskReaderModel(event.target.value);
                      }}
                    />
                  </label>
                  <datalist id="experiment-model-options">
                    {state?.experimentModels.map((model) => (
                      <option key={model.id} value={model.id}>
                        {model.name} · {model.provider}
                      </option>
                    ))}
                  </datalist>
                  <label>
                    <span>Max maintainer rounds</span>
                    <input
                      min={1}
                      type="number"
                      value={taskMaxRounds}
                      onChange={(event) => {
                        invalidateTaskPreview();
                        setTaskMaxRounds(Number(event.target.value));
                      }}
                    />
                  </label>
                </>
              )}
            </div>
            <div className="task-form-actions">
              <button
                className="button secondary"
                disabled={taskBusy || !taskGroupID}
                onClick={() => void previewTask()}
                type="button"
              >
                {taskBusy ? "Working…" : "预览任务"}
              </button>
            </div>
          </form>

          <aside className="task-preview" aria-live="polite">
            <p className="eyebrow">EXECUTION PREVIEW</p>
            {!taskPreview && !taskMessage && (
              <p className="task-empty">选择范围后点击“预览任务”。这里不会启动实验。</p>
            )}
            {taskMessage && <p className="task-message">{taskMessage}</p>}
            {taskPreview && (
              <>
                <span className={`status ${taskPreview.eligible ? "success" : "warning"}`}>
                  {taskPreview.eligible ? "Ready to queue" : "Cannot run"}
                </span>
                <h3>{taskPreview.selected_questions} / {taskPreview.available_questions} questions</h3>
                <dl>
                  <div><dt>Runs</dt><dd>{taskPreview.planned_runs}</dd></div>
                  <div><dt>Max LLM calls</dt><dd>{taskPreview.max_llm_calls}</dd></div>
                  <div><dt>Source</dt><dd>{taskPreview.source_kind}</dd></div>
                  <div><dt>Benchmarks</dt><dd>{taskPreview.benchmarks.length}</dd></div>
                </dl>
                {taskPreview.ineligible_reason && (
                  <p className="task-warning">{taskPreview.ineligible_reason}</p>
                )}
                {taskPreview.paid && taskPreview.eligible && (
                  <label className="paid-confirmation">
                    <input
                      checked={taskConfirmPaid}
                      onChange={(event) => setTaskConfirmPaid(event.target.checked)}
                      type="checkbox"
                    />
                    <span>我确认启动会调用付费 LLM；API key 只在后端使用。</span>
                  </label>
                )}
                <button
                  className="button primary"
                  disabled={
                    taskBusy
                    || !taskPreview.eligible
                    || (taskPreview.paid && !taskConfirmPaid)
                  }
                  onClick={() => void createTask()}
                  type="button"
                >
                  加入实验队列
                </button>
              </>
            )}
          </aside>
        </div>

        <div className="task-list">
          <div className="task-list-head">
            <h3>Task history</h3>
            <span>{state?.tasks.length ?? 0} loaded · every 10s refresh</span>
          </div>
          {state?.tasks.map((task) => {
            const evaluatedQuestions = (task.request.question_offset ?? 0)
              + task.preview.selected_questions;
            const remainingQuestions = Math.max(
              0,
              task.preview.available_questions - evaluatedQuestions,
            );
            const maintainedRun = task.run_ids
              .map((runID) => runByID.get(runID))
              .find((run) => solutionByID.get(run?.solution_version_id ?? "")?.builder_id === "llmwiki-maintainer");
            const additionalQuestionOptions = Array.from(new Set(
              [1, 5, 10, 20, 50, remainingQuestions]
                .filter((value) => value > 0 && value <= remainingQuestions),
            )).sort((left, right) => left - right);
            const additionalQuestions = Math.min(
              additionalQuestionsByTask[task.id] ?? 5,
              remainingQuestions,
            );
            return (
            <article className="task-row" key={task.id}>
              <div>
                <span className={`status ${
                  task.status === "completed"
                    ? "success"
                    : task.status === "failed"
                        || task.status === "cancelled"
                        || task.status === "needs_more_rounds"
                      ? "warning"
                      : "neutral"
                }`}>{task.status === "needs_more_rounds" ? "needs more rounds" : task.status}</span>
                <strong>{task.id}</strong>
                <small>{formatDate(task.created_at)}</small>
              </div>
              <div>
                <strong>{task.request.dataset} / {task.request.group_id}</strong>
                <small>
                  {task.request.partition} · {task.request.mode}
                  {task.retry_of_task_id ? ` · retry of ${task.retry_of_task_id}` : ""}
                  {task.request.reuse_artifact_from_task_id
                    ? ` · +${task.preview.selected_questions} questions from ${task.continued_from_task_id}`
                    : task.continued_from_task_id
                    ? ` · +${task.request.max_rounds} rounds from ${task.continued_from_task_id}`
                    : ""}
                </small>
              </div>
              <div>
                <strong>
                  {task.request.reuse_artifact_from_task_id
                    ? `+${task.preview.selected_questions} questions`
                    : `${task.preview.selected_questions} questions`}
                </strong>
                {task.request.reuse_artifact_from_task_id && (
                  <small>{task.preview.cumulative_questions} cumulative</small>
                )}
                <small>{task.request.model || "context reader · no paid LLM"}</small>
              </div>
              <div className="task-links">
                {task.run_ids.map((runID) => {
                  const run = runByID.get(runID);
                  const solution = run ? solutionByID.get(run.solution_version_id) : undefined;
                  return (
                    <a key={runID} href={`/run?run=${encodeURIComponent(runID)}`}>
                      {taskRunScoreLabel(solution)}
                    </a>
                  );
                })}
                {task.artifact_ids.map((artifactID) => (
                  <a key={artifactID} href={`${API}/artifacts/${encodeURIComponent(artifactID)}`} target="_blank" rel="noreferrer">
                    Artifact JSON ↗
                  </a>
                ))}
                {(task.status === "queued" || task.status === "running") && (
                  <button disabled={taskBusy} onClick={() => void cancelTask(task.id)} type="button">
                    Cancel
                  </button>
                )}
                {(task.status === "failed"
                  || task.status === "cancelled"
                  || task.status === "needs_more_rounds") && (
                  <button disabled={taskBusy} onClick={() => void retryTask(task)} type="button">
                    {task.request.mode === "maintainer" ? "Retry（重新计费）" : "Retry"}
                  </button>
                )}
                {(task.status === "failed" || task.status === "needs_more_rounds")
                  && task.request.mode === "maintainer" && (
                  <>
                    <label className="continue-rounds">
                      <span>追加</span>
                      <select
                        aria-label={`Additional rounds for ${task.id}`}
                        disabled={taskBusy}
                        onChange={(event) => setContinueRoundsByTask((current) => ({
                          ...current,
                          [task.id]: Number(event.target.value),
                        }))}
                        value={continueRoundsByTask[task.id] ?? 20}
                      >
                        {[10, 20, 30, 50, 100, 200].map((rounds) => (
                          <option key={rounds} value={rounds}>{rounds}</option>
                        ))}
                      </select>
                      <span>轮</span>
                    </label>
                    <button
                      disabled={taskBusy}
                      onClick={() => void continueTask(
                        task,
                        continueRoundsByTask[task.id] ?? 20,
                      )}
                      type="button"
                    >
                      Continue（+{continueRoundsByTask[task.id] ?? 20} rounds）
                    </button>
                  </>
                )}
                {task.status === "completed"
                  && task.request.mode === "maintainer"
                  && maintainedRun
                  && remainingQuestions > 0 && (
                  <>
                    <label className="continue-rounds">
                      <span>追加</span>
                      <select
                        aria-label={`Additional questions for ${task.id}`}
                        disabled={taskBusy}
                        onChange={(event) => setAdditionalQuestionsByTask((current) => ({
                          ...current,
                          [task.id]: Number(event.target.value),
                        }))}
                        value={additionalQuestions}
                      >
                        {additionalQuestionOptions.map((questions) => (
                          <option key={questions} value={questions}>{questions}</option>
                        ))}
                      </select>
                      <span>题</span>
                    </label>
                    <button
                      disabled={taskBusy}
                      onClick={() => void evaluateMoreQuestions(
                        task,
                        additionalQuestions,
                        maintainedRun.artifact_id,
                      )}
                      type="button"
                    >
                      复用制品追加题目（+{additionalQuestions}）
                    </button>
                  </>
                )}
              </div>
              {task.error && (
                <p className={task.status === "needs_more_rounds" ? "task-message" : "task-error"}>
                  {task.error}
                </p>
              )}
            </article>
            );
          })}
          {state && state.tasks.length === 0 && (
            <div className="loading-panel">还没有实验任务。Source-only baseline 可以零 LLM 成本运行。</div>
          )}
        </div>
      </section>

      <section className="content-section tinted" id="artifacts">
        <div className="section-heading">
          <div>
            <p className="eyebrow">EXPERIMENTS & ARTIFACTS</p>
            <h2>实验、版本和制品</h2>
          </div>
          <p>当前页展示 {state?.runs.length ?? 0} 个 Run；每个 Artifact View 按需读取。</p>
        </div>
        <div className="experiment-grid">
          {state?.runs.map((run) => {
            const detail = state.details[run.id];
            const solution = solutionByID.get(run.solution_version_id);
            const qa = detail?.trials.find((trial) => trial.benchmark_id === "knowledge-search-get-qa");
            const quality = detail?.trials.find((trial) => trial.benchmark_id === "wiki-artifact-quality");
            const artifact = detail?.artifact;
            return (
              <article className={`experiment-card ${solution?.builder_id === "llmwiki-maintainer" ? "featured" : ""}`} key={run.id}>
                <div className="experiment-head">
                  <div>
                    <span className="role">{artifact?.role ?? "experiment"}</span>
                    <h3>{solutionName(solution)}</h3>
                    <p>{run.dataset} · {run.partition} / {run.case_id}</p>
                  </div>
                  <span className="status success">{run.status}</span>
                </div>
                <div className="score-pair">
                  <div><span>QA accuracy</span><strong>{formatMetric(metricValue(qa, "answer_accuracy"), "answer_accuracy")}</strong></div>
                  <div><span>Judge confidence</span><strong>{formatMetric(metricValue(qa, "judge_mean_confidence"), "judge_mean_confidence")}</strong></div>
                  <div><span>Disputed answers</span><strong>{formatMetric(metricValue(qa, "judge_disputed_rate"), "judge_disputed_rate")}</strong></div>
                  <div><span>Artifact quality</span><strong>{formatMetric(metricValue(quality, "artifact_quality_score"), "artifact_quality_score")}</strong></div>
                  <div><span>Wiki pages</span><strong>{formatMetric(metricValue(quality, "document_count"), "document_count")}</strong></div>
                </div>
                <dl className="version-details">
                  <div><dt>Version</dt><dd>{solution?.builder_id}@{solution?.builder_version}</dd></div>
                  <div><dt>Model</dt><dd>{run.metadata?.model ?? solution?.model ?? "—"}</dd></div>
                  <div><dt>Revision</dt><dd>{solution?.code_revision ?? "—"}</dd></div>
                  <div><dt>Run ID</dt><dd>{run.id}</dd></div>
                  <div><dt>Artifact ID</dt><dd>{shortDigest(run.artifact_id)}</dd></div>
                </dl>
                <div className="artifact-actions">
                  {artifact?.views.native && <a className="button primary" href={artifact.views.native} target="_blank" rel="noreferrer">打开制品 ↗</a>}
                  {Object.entries(artifact?.views ?? {}).filter(([kind]) => kind !== "native").map(([kind, href]) => (
                    <a className="button secondary" href={href} key={kind} target="_blank" rel="noreferrer">{kind} ↗</a>
                  ))}
                  <a className="button secondary" href={`${API}/runs/${encodeURIComponent(run.id)}`} target="_blank" rel="noreferrer">run JSON ↗</a>
                </div>
              </article>
            );
          })}
        </div>
      </section>

      <section className="content-section" id="benchmarks">
        <div className="section-heading">
          <div>
            <p className="eyebrow">BENCHMARKS</p>
            <h2>Benchmark 与成绩矩阵</h2>
          </div>
          <p>矩阵由后端聚合，不在浏览器遍历全部历史 Run。</p>
        </div>
        <div className="benchmark-catalog">
          {state?.benchmarks.map((benchmark) => (
            <article key={benchmark.id}>
              <span className={`status ${benchmark.executed ? "success" : "neutral"}`}>{benchmark.executed ? "Executed" : "Not run"}</span>
              <h3>{benchmark.name}</h3>
              <code>{benchmark.id}</code>
              <p>{benchmark.description}</p>
            </article>
          ))}
        </div>
        <div className="matrix-wrap">
          <div className="matrix-title">
            <h3>Loaded result page</h3>
            <span>Run × Benchmark</span>
          </div>
          <div className="benchmark-matrix" role="table" aria-label="Experiment benchmark scores">
            <div className="matrix-row matrix-header" role="row">
              <div role="columnheader">Experiment</div>
              {state?.matrix.benchmarks.map((benchmark) => <div role="columnheader" key={benchmark.id}>{benchmark.name}</div>)}
            </div>
            {state?.matrix.rows.map((row) => {
              const run = runByID.get(row.run_id);
              const solution = solutionByID.get(row.solution_version_id);
              return (
                <div className="matrix-row" role="row" key={row.run_id}>
                  <div className="matrix-experiment" role="rowheader">
                    <strong>{solutionName(solution)}</strong>
                    <span>{run ? `${run.dataset} / ${run.case_id}` : row.run_id}</span>
                  </div>
                  {row.cells.map((cell) => (
                    <div className={`matrix-cell ${cell.executed ? "" : "empty"}`} role="cell" key={cell.benchmark_id}>
                      <strong>{cell.executed ? formatMetric(cell.score, state.matrix.benchmarks.find((item) => item.id === cell.benchmark_id)?.primary_metric ?? "") : "Not run"}</strong>
                      <small>{cell.metrics?.map((metric) => `${metric.name.replaceAll("_", " ")} ${formatMetric(metric.value, metric.name)}`).join(" · ") ?? "这个 Run 没有执行该 Benchmark"}</small>
                    </div>
                  ))}
                </div>
              );
            })}
          </div>
        </div>
      </section>

      <footer>
        <span>Local API-driven evaluation registry</span>
        <span>{state ? `refreshed ${formatDate(state.loadedAt)}` : "waiting for API"}</span>
      </footer>
    </main>
  );
}
