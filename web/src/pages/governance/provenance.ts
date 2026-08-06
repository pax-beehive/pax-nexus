// 溯源链的派生：把一次 getTeamNote 响应里已经带齐的证据链，拆成每个版本
// 固定五段（源事件 / 抽取 / 候选 / 版本 / 投递）的展示结构。纯函数，不发请求。
// 第六段「召回决策」挂在笔记（而非版本）上，由调用方单独用 describeRecall 渲染。
import type {
  ExplorerCandidate,
  ExplorerDelivery,
  ExplorerExtractionRun,
  ExplorerRecallUse,
  ExplorerRevision,
  ExplorerSourceEvent,
  TeamNoteDetail,
} from "../../api/types";

export type ProvenanceStage = "source" | "extraction" | "candidate" | "revision" | "delivery";

export interface ProvenanceStep {
  stage: ProvenanceStage;
  /** 阶段名的中文标签，直接渲染 */
  label: string;
  title: string;
  body: string;
  /** mono 小字的引用；无引用时为 undefined */
  ref?: string;
  /** 这一段没有记录 */
  missing: boolean;
}

export interface ProvenanceRevision {
  revision: number;
  createdAt: string;
  steps: ProvenanceStep[]; // 恒为 5 段，顺序固定
}

function formatRefList(ids: string[]): string | undefined {
  if (ids.length === 0) return undefined;
  if (ids.length === 1) return ids[0];
  return `${ids[0]} 等 ${ids.length} 条`;
}

function buildSourceStep(evidence: ExplorerSourceEvent[]): ProvenanceStep {
  if (evidence.length === 0) {
    return {
      stage: "source",
      label: "源事件",
      title: "源事件",
      body: "没有记录源事件。",
      missing: true,
    };
  }
  const sessionId = evidence[0].session_id;
  return {
    stage: "source",
    label: "源事件",
    title: `共 ${evidence.length} 条源事件`,
    body: `来自 session ${sessionId} 的 ${evidence.length} 条原始事件。`,
    ref: formatRefList(evidence.map((event) => event.event_id)),
    missing: false,
  };
}

function buildExtractionStep(run: ExplorerExtractionRun): ProvenanceStep {
  if (!run || !run.run_id || run.status === "") {
    return {
      stage: "extraction",
      label: "抽取",
      title: "抽取",
      body: "没有记录抽取运行。",
      missing: true,
    };
  }
  return {
    stage: "extraction",
    label: "抽取",
    title: `模型 ${run.model} · prompt ${run.prompt_version}`,
    body: `状态 ${run.status}，输入 ${run.input_tokens} / 输出 ${run.output_tokens} tokens。`,
    ref: run.run_id,
    missing: false,
  };
}

function buildCandidateStep(candidate: ExplorerCandidate): ProvenanceStep {
  if (!candidate || !candidate.candidate_id || candidate.admission_status === "") {
    return {
      stage: "candidate",
      label: "候选",
      title: "候选",
      body: "没有记录候选。",
      missing: true,
    };
  }
  const statusText =
    candidate.admission_status === "rejected"
      ? `被拒绝：${candidate.rejection_reason ?? "未说明原因"}`
      : candidate.admission_status;
  return {
    stage: "candidate",
    label: "候选",
    title: `候选 ${candidate.candidate_id}（${candidate.action}）`,
    body: `状态 ${statusText}。`,
    ref: candidate.candidate_id,
    missing: false,
  };
}

function buildRevisionStep(revision: ExplorerRevision): ProvenanceStep {
  if (!revision.body || revision.body.trim() === "") {
    return {
      stage: "revision",
      label: "版本",
      title: `版本 ${revision.revision}`,
      body: "没有记录版本正文。",
      missing: true,
    };
  }
  return {
    stage: "revision",
    label: "版本",
    title: `版本 ${revision.revision} · ${revision.operation}`,
    body: revision.body,
    ref: revision.candidate_id,
    missing: false,
  };
}

function buildDeliveryStep(deliveries: ExplorerDelivery[]): ProvenanceStep {
  if (deliveries.length === 0) {
    return {
      stage: "delivery",
      label: "投递",
      title: "投递",
      body: "没有投递记录。",
      missing: true,
    };
  }
  const totalTokens = deliveries.reduce((sum, delivery) => sum + delivery.context_tokens, 0);
  return {
    stage: "delivery",
    label: "投递",
    title: `投递给 ${deliveries.length} 个接收方`,
    body: `共 ${deliveries.length} 次投递，消耗 ${totalTokens} context tokens。`,
    ref: formatRefList(deliveries.map((delivery) => delivery.recipient_agent_id)),
    missing: false,
  };
}

export function buildProvenance(detail: TeamNoteDetail): ProvenanceRevision[] {
  return [...detail.revisions]
    .sort((a, b) => b.revision - a.revision)
    .map((revision) => ({
      revision: revision.revision,
      createdAt: revision.created_at,
      steps: [
        buildSourceStep(revision.evidence),
        buildExtractionStep(revision.extraction),
        buildCandidateStep(revision.candidate),
        buildRevisionStep(revision),
        buildDeliveryStep(revision.deliveries),
      ],
    }));
}

export function describeRecall(use: ExplorerRecallUse): string {
  const reasons = [
    ...use.rejection_reasons,
    ...use.budget_drop_reasons,
    ...use.hard_gate_failures,
  ];
  if (use.delivered && reasons.length === 0) {
    return `命中并投递给 ${use.recipient_agent_id}（session ${use.recipient_session_id}）。`;
  }
  if (reasons.length > 0) {
    return `未投递给 ${use.recipient_agent_id}，原因：${reasons.join("、")}。`;
  }
  return `未投递给 ${use.recipient_agent_id}，没有记录拒绝原因。`;
}
