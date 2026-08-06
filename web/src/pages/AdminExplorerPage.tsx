// Governance · Memory explorer：双栏容器。左 340px 是 NoteList（自己取数、
// 自己扛取数失败），右侧按 useParams().noteId 决定渲染详情还是空态。两栏各自
// 独立取数——右栏挂了不拖累左栏，这是设计裁定（task-7 brief 的验收点之一）。
//
// Task 6 先把骨架和左栏落好，右栏在 :noteId 存在时只是占位；Task 7 会把占位
// 换成真正的溯源链渲染（NoteProvenance / NoteRecalls）。
import { Link, useParams } from "react-router-dom";
import { EmptyState } from "../components/EmptyState";
import { Kicker } from "../components/Kicker";
import { NoteList } from "./governance/NoteList";

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
              <p className="muted small">占位：详情视图由 Task 7 补齐（note {noteId}）。</p>
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
