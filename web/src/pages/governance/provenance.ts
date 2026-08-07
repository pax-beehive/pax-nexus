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
  return `${ids[0]} +${ids.length - 1} more`;
}

function buildSourceStep(evidence: ExplorerSourceEvent[]): ProvenanceStep {
  if (evidence.length === 0) {
    return {
      stage: "source",
      label: "Evidence",
      title: "Evidence",
      body: "No source events recorded.",
      missing: true,
    };
  }
  const sessionId = evidence[0].session_id;
  return {
    stage: "source",
    label: "Evidence",
    title: `${evidence.length} source events`,
    body: `${evidence.length} raw events from session ${sessionId}.`,
    ref: formatRefList(evidence.map((event) => event.event_id)),
    missing: false,
  };
}

function buildExtractionStep(run: ExplorerExtractionRun): ProvenanceStep {
  if (!run || !run.run_id || run.status === "") {
    return {
      stage: "extraction",
      label: "Extraction",
      title: "Extraction",
      body: "No extraction run recorded.",
      missing: true,
    };
  }
  return {
    stage: "extraction",
    label: "Extraction",
    title: `Model ${run.model} · prompt ${run.prompt_version}`,
    body: `Status ${run.status} · ${run.input_tokens} in / ${run.output_tokens} out tokens.`,
    ref: run.run_id,
    missing: false,
  };
}

// admission_status 是后端枚举原文（见 migrations/001_init.sql 里的
// note_candidates.admission_status：pending / admitted / rejected），中文名在前、
// 原文括号跟随，和 Audit 页「名字在前、原始 ID 括号跟随」的处理保持一致。
function describeAdmissionStatus(status: string): string {
  switch (status) {
    case "admitted":
      return "Admitted";
    case "rejected":
      return "Rejected";
    case "pending":
      return "Pending";
    default:
      return status;
  }
}

function buildCandidateStep(candidate: ExplorerCandidate): ProvenanceStep {
  if (!candidate || !candidate.candidate_id || candidate.admission_status === "") {
    return {
      stage: "candidate",
      label: "Candidate",
      title: "Candidate",
      body: "No candidate recorded.",
      missing: true,
    };
  }
  const statusText =
    candidate.admission_status === "rejected"
      ? `${describeAdmissionStatus(candidate.admission_status)}: ${candidate.rejection_reason ?? "no reason given"}`
      : describeAdmissionStatus(candidate.admission_status);
  return {
    stage: "candidate",
    label: "Candidate",
    title: `Candidate ${candidate.candidate_id} (${candidate.action})`,
    body: `Status: ${statusText}.`,
    ref: candidate.candidate_id,
    missing: false,
  };
}

function buildRevisionStep(revision: ExplorerRevision): ProvenanceStep {
  const hasBody = !!revision.body && revision.body.trim() !== "";
  if (!hasBody) {
    if (revision.operation === "resolve") {
      // resolve 操作允许空正文：这是领域模型里合法且预期的形态（见
      // internal/teamnote/ledger.go validateCandidate 对 ActionResolve 的豁免），
      // 不能标成 missing，否则会让一条正常的 resolve 版本显示成「记录缺失」。
      return {
        stage: "revision",
        label: "Revision",
        title: `Revision ${revision.revision} · resolve`,
        body: "This is a resolve operation, which carries no body by design — not a missing record.",
        ref: revision.candidate_id,
        missing: false,
      };
    }
    return {
      stage: "revision",
      label: "Revision",
      title: `Revision ${revision.revision}`,
      body: "No revision body recorded.",
      missing: true,
    };
  }
  return {
    stage: "revision",
    label: "Revision",
    title: `Revision ${revision.revision} · ${revision.operation}`,
    body: revision.body,
    ref: revision.candidate_id,
    missing: false,
  };
}

function buildDeliveryStep(deliveries: ExplorerDelivery[]): ProvenanceStep {
  if (deliveries.length === 0) {
    return {
      stage: "delivery",
      label: "Delivery",
      title: "Delivery",
      body: "No deliveries recorded.",
      missing: true,
    };
  }
  const totalTokens = deliveries.reduce((sum, delivery) => sum + delivery.context_tokens, 0);
  return {
    stage: "delivery",
    label: "Delivery",
    title: `Delivered to ${deliveries.length} recipients`,
    body: `${deliveries.length} deliveries · ${totalTokens} context tokens.`,
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
    return `Delivered to ${use.recipient_agent_id} (session ${use.recipient_session_id}).`;
  }
  if (reasons.length > 0) {
    return `Not delivered to ${use.recipient_agent_id}: ${reasons.join(", ")}.`;
  }
  return `Not delivered to ${use.recipient_agent_id}; no rejection reason recorded.`;
}
