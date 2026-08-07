// Governance · Memory explorer：双栏容器。左 340px 是 NoteList（自己取数、
// 自己扛取数失败），右侧按 useParams().noteId 决定渲染详情还是空态。两栏各自
// 独立取数——右栏挂了不拖累左栏，反之亦然（task-7 brief 的验收点之一）。
//
// 右栏的取数是本页唯一的一次 getTeamNote：那一次响应里已经带齐了六段链条
// （前五段挂在每个 revision 上，由 provenance.ts 的 buildProvenance 派生；
// 第六段「召回决策」挂在笔记本身，由 NoteRecalls 直接渲染）。绝不额外调用
// getExtractionDiagnostic / getChannelDiagnostic / getRecallDiagnostic——那三个
// 诊断端点已被设计裁定退出主链，只作为 /governance/pipeline 的可选下钻。
import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { getTeamNote } from "../api/queries";
import type { TeamNoteDetail } from "../api/types";
import { Badge } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { PageHeader } from "../components/PageHeader";
import { RegionError } from "../components/RegionError";
import { Tag } from "../components/Tag";
import { formatTime } from "../lib/format";
import { useErrorHandler } from "../lib/useErrorHandler";
import { NoteList } from "./governance/NoteList";
import { noteKindLabel } from "./governance/noteKind";
import { NoteProvenance } from "./governance/NoteProvenance";
import { NoteRecalls } from "./governance/NoteRecalls";

type DetailState =
  | { status: "loading" }
  | { status: "ready"; detail: TeamNoteDetail }
  | { status: "error"; error: unknown };

// spec §3.2 第 1 条：笔记头必须能看到 subject、写它的 Agent、受众、有效期、
// note_id · rev N · state。后四项都是「这条事实的权限边界与身份」，缺一项
// 都会让人凭直觉猜错——例如受众缺失时容易误以为是全团队广播。
function NoteHead({ detail }: { detail: TeamNoteDetail }) {
  const summary = detail.note.summary;
  const audience =
    summary.audience_agent_ids.length === 0
      ? "不限（全团队可见）"
      : summary.audience_agent_ids.join("、");
  return (
    <section className="card">
      <div className="row wrap">
        <Tag tone="outline">{noteKindLabel(summary.kind)}</Tag>
        <Badge status={summary.state} />
      </div>
      <h2>{summary.subject}</h2>
      {/* M7: both this identity line and the meta row below are the
          spec-mandated §3.2 note-head fields (raw note_id/rev/state, origin
          Agent, audience, expiry), not optional secondary text -- `.gv-id-ref`
          (governance.css) instead of `.faint`, which measured below AA on
          all three themes here. */}
      <code className="small gv-id-ref">
        {summary.note_id} · rev {summary.revision} · {summary.state}
      </code>
      <p className="gv-note-head-body">{detail.note.body}</p>
      <div className="row wrap small gv-id-ref">
        <span>
          来自 <code>{summary.origin_agent_id}</code> · session{" "}
          <code>{detail.note.origin_session_id}</code>
        </span>
        <span>受众 {audience}</span>
        <span>创建于 {formatTime(summary.created_at)}</span>
        <span>更新于 {formatTime(summary.updated_at)}</span>
        <span>软过期 {formatTime(summary.soft_expires_at)}</span>
        <span>硬过期 {formatTime(summary.hard_expires_at)}</span>
      </div>
    </section>
  );
}

// 笔记之间唯一的横向跳转手段：related_notes 链到另一条 Explorer 详情，
// related_subjects 是主题 chips。两者都是「顺着链条走」这一屏的题中之义，
// 旧详情页的 Relations 块被静默弄丢过一次，这里补回来。
function NoteRelations({ detail }: { detail: TeamNoteDetail }) {
  const subjects = detail.note.related_subjects;
  const notes = detail.related_notes;
  const hasAny = subjects.length > 0 || notes.length > 0;
  return (
    <section className="card">
      <h2>相关的事实</h2>
      {!hasAny ? (
        <p className="muted small">没有关联的主题或笔记。</p>
      ) : (
        <>
          {subjects.length > 0 && (
            <div className="chips">
              {subjects.map((subject) => (
                <code key={subject}>{subject}</code>
              ))}
            </div>
          )}
          {notes.length > 0 && (
            <ul className="gv-related-list">
              {notes.map((related) => (
                <li key={related.note_id}>
                  <Link to={`/governance/memory/${encodeURIComponent(related.note_id)}`}>
                    {related.subject}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}

function NoteDetail({ noteId }: { noteId: string }) {
  const handleError = useErrorHandler();
  const [state, setState] = useState<DetailState>({ status: "loading" });
  // Bumped by the RegionError retry button below; the effect depends on it
  // so a retry re-issues getTeamNote without touching noteId itself.
  const [retryKey, setRetryKey] = useState(0);
  const retry = useCallback(() => setRetryKey((k) => k + 1), []);

  useEffect(() => {
    const controller = new AbortController();
    setState({ status: "loading" });
    getTeamNote(noteId, controller.signal)
      .then((detail) => setState({ status: "ready", detail }))
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        handleError(error);
        setState({ status: "error", error });
      });
    return () => controller.abort();
  }, [noteId, handleError, retryKey]);

  if (state.status === "loading") return <p className="muted small">Loading…</p>;
  if (state.status === "error") {
    // I1: the right column used to dead-end in an unretryable <div>; it now
    // reuses the same `RegionError` primitive as the rest of Governance
    // (note bad + Retry), satisfying design §4's "右栏塌成可重试错误".
    return <RegionError error={state.error} onRetry={retry} />;
  }

  return (
    <>
      <NoteHead detail={state.detail} />
      <NoteRelations detail={state.detail} />
      <NoteProvenance detail={state.detail} />
      <NoteRecalls recalls={state.detail.recall_observations} />
    </>
  );
}

export function AdminExplorerPage() {
  const { noteId } = useParams();

  return (
    <>
      <PageHeader
        variant="bleed"
        kicker="Governance · 追一条事实"
        title="这是从哪来的？"
        lede={
          <p className="lede-dim">
            挑一条团队正在流传的事实，顺着它回到产生它的那次会话——再往前看它被交给过哪些
            Agent。
          </p>
        }
      />
      <div className="gv-explorer" data-active={noteId ? "detail" : "list"}>
        <NoteList activeNoteId={noteId} />
        <div className="gv-note-detail">
          {noteId ? (
            <>
              <Link className="small gv-back-link" to="/governance/memory">
                ← 返回列表
              </Link>
              <NoteDetail noteId={noteId} />
            </>
          ) : (
            <EmptyState
              title="挑一条 Team Note"
              body="从左边选一条正在流传的事实，看看它是怎么来的。"
            />
          )}
        </div>
      </div>
    </>
  );
}
