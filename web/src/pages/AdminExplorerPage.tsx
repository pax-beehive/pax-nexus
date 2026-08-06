// Governance · Memory explorer：双栏容器。左 340px 是 NoteList（自己取数、
// 自己扛取数失败），右侧按 useParams().noteId 决定渲染详情还是空态。两栏各自
// 独立取数——右栏挂了不拖累左栏，反之亦然（task-7 brief 的验收点之一）。
//
// 右栏的取数是本页唯一的一次 getTeamNote：那一次响应里已经带齐了六段链条
// （前五段挂在每个 revision 上，由 provenance.ts 的 buildProvenance 派生；
// 第六段「召回决策」挂在笔记本身，由 NoteRecalls 直接渲染）。绝不额外调用
// getExtractionDiagnostic / getChannelDiagnostic / getRecallDiagnostic——那三个
// 诊断端点已被设计裁定退出主链，只作为 /governance/pipeline 的可选下钻。
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { getTeamNote } from "../api/queries";
import type { TeamNoteDetail } from "../api/types";
import { Badge } from "../components/Badge";
import { EmptyState } from "../components/EmptyState";
import { Kicker } from "../components/Kicker";
import { Tag } from "../components/Tag";
import { formatTime } from "../lib/format";
import { useErrorHandler } from "../lib/useErrorHandler";
import { NoteList } from "./governance/NoteList";
import { NoteProvenance } from "./governance/NoteProvenance";
import { NoteRecalls } from "./governance/NoteRecalls";

type DetailState =
  | { status: "loading" }
  | { status: "ready"; detail: TeamNoteDetail }
  | { status: "error" };

function NoteHead({ detail }: { detail: TeamNoteDetail }) {
  const summary = detail.note.summary;
  return (
    <section className="card">
      <div className="row wrap">
        <Tag tone="outline">{summary.kind}</Tag>
        <Badge status={summary.state} />
        <code className="small faint">{summary.note_id}</code>
      </div>
      <h2>{summary.subject}</h2>
      <p className="gv-note-head-body">{detail.note.body}</p>
      <div className="row wrap small faint">
        <span>
          来自 <code>{summary.origin_agent_id}</code> · session{" "}
          <code>{detail.note.origin_session_id}</code>
        </span>
        <span>创建于 {formatTime(summary.created_at)}</span>
        <span>更新于 {formatTime(summary.updated_at)}</span>
        <span>软过期 {formatTime(summary.soft_expires_at)}</span>
        <span>硬过期 {formatTime(summary.hard_expires_at)}</span>
      </div>
    </section>
  );
}

function NoteDetail({ noteId }: { noteId: string }) {
  const handleError = useErrorHandler();
  const [state, setState] = useState<DetailState>({ status: "loading" });

  useEffect(() => {
    const controller = new AbortController();
    setState({ status: "loading" });
    getTeamNote(noteId, controller.signal)
      .then((detail) => setState({ status: "ready", detail }))
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        handleError(error);
        setState({ status: "error" });
      });
    return () => controller.abort();
  }, [noteId, handleError]);

  if (state.status === "loading") return <p className="muted small">Loading…</p>;
  if (state.status === "error") {
    return (
      <div className="note warn">这条 Team Note 加载失败，它可能已经过期或被留存策略清理。</div>
    );
  }

  return (
    <>
      <NoteHead detail={state.detail} />
      <NoteProvenance detail={state.detail} />
      <NoteRecalls recalls={state.detail.recall_observations} />
    </>
  );
}

export function AdminExplorerPage() {
  const { noteId } = useParams();

  return (
    <>
      <div className="gv-head">
        <div>
          <Kicker>Governance · 追一条事实</Kicker>
          <h1>这是从哪来的？</h1>
          <p>
            挑一条团队正在流传的事实，顺着它回到产生它的那次会话——再往前看它被交给过哪些
            Agent。
          </p>
        </div>
      </div>
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
