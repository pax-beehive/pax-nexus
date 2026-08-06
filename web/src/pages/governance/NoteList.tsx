// Explorer 左栏：可筛选的 Team Note 列表。纯粹是取数 + 渲染，详情由右栏
// （AdminExplorerPage）按 useParams().noteId 单独取数，两者互不干扰——这一栏
// 挂了也不影响右栏能不能看，反之亦然。
//
// `agent` 这个 query 参数是从 AgentHeader.tsx（「查看它的记忆」链接）带过来的
// 深链，不能丢：web/tests/agent-deeplink.dom.test.tsx 钉住了它必须进筛选框、
// 也必须进首次请求。
//
// Kind 是有界枚举（5 个值，见 noteKind.ts），用 <select> 而不是第二个 <Seg>
// ——6 档 Seg 在 340px 的窄栏里塞不下；State 只有 4 档，Seg 塞得下。
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { listTeamNotes } from "../../api/queries";
import { Badge } from "../../components/Badge";
import { Button } from "../../components/Button";
import { Seg } from "../../components/Seg";
import { Tag } from "../../components/Tag";
import { useErrorHandler } from "../../lib/useErrorHandler";
import { usePagedList } from "../../lib/usePagedList";
import { NOTE_KIND_OPTIONS, noteKindLabel, type NoteKindFilter } from "./noteKind";

type StateFilter = "" | "active" | "resolved" | "expired";

const STATE_OPTIONS: { value: StateFilter; label: string }[] = [
  { value: "", label: "全部" },
  { value: "active", label: "生效中" },
  { value: "resolved", label: "已解决" },
  { value: "expired", label: "已过期" },
];

export function NoteList({ activeNoteId }: { activeNoteId?: string }) {
  const handleError = useErrorHandler();
  const [searchParams] = useSearchParams();
  const [queryInput, setQueryInput] = useState("");
  const [query, setQuery] = useState("");
  const [agentId, setAgentId] = useState(searchParams.get("agent") ?? "");
  const [kind, setKind] = useState<NoteKindFilter>("");
  const [state, setState] = useState<StateFilter>("");

  const notes = usePagedList(
    (cursor) =>
      listTeamNotes({
        q: query || undefined,
        kind: kind || undefined,
        state: state || undefined,
        agent_id: agentId || undefined,
        limit: 50,
        cursor,
      }),
    [query, kind, state, agentId],
  );

  useEffect(() => {
    if (notes.error) handleError(notes.error);
  }, [notes.error, handleError]);

  return (
    <div className="gv-note-list">
      <div className="gv-note-filters">
        <input
          type="search"
          aria-label="搜索 Team Note"
          placeholder="搜索主题或正文"
          value={queryInput}
          onChange={(event) => setQueryInput(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") setQuery(queryInput.trim());
          }}
        />
        <input
          aria-label="按 Agent 筛选"
          placeholder="Agent ID"
          value={agentId}
          onChange={(event) => setAgentId(event.target.value.trim())}
        />
        <select
          aria-label="按类型筛选"
          value={kind}
          onChange={(event) => setKind(event.target.value as NoteKindFilter)}
        >
          {NOTE_KIND_OPTIONS.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        <Seg label="按状态筛选" options={STATE_OPTIONS} value={state} onChange={setState} />
      </div>
      {notes.loading ? (
        <p className="muted small gv-note-status">Loading…</p>
      ) : notes.error && notes.items.length === 0 ? (
        <div className="note bad row between gv-note-status" role="alert">
          <span>加载失败。</span>
          <Button size="sm" onClick={notes.reload}>
            重试
          </Button>
        </div>
      ) : notes.items.length === 0 ? (
        <p className="muted small gv-note-status">没有匹配的 Team Note。</p>
      ) : (
        <>
          {notes.items.map((note) => (
            <Link
              key={note.note_id}
              to={`/governance/memory/${encodeURIComponent(note.note_id)}`}
              className={`gv-note-item${note.note_id === activeNoteId ? " on" : ""}`}
            >
              <strong>{note.subject}</strong>
              <div className="small mono faint">
                {note.note_id} · revision {note.revision}
              </div>
              <div className="row wrap">
                <Tag tone="outline">{noteKindLabel(note.kind)}</Tag>
                <Badge status={note.state} />
              </div>
            </Link>
          ))}
          {notes.error ? (
            <div className="note bad row between gv-note-status" role="alert">
              <span>加载更多失败。</span>
              <Button size="sm" onClick={() => void notes.loadMore()}>
                重试
              </Button>
            </div>
          ) : null}
        </>
      )}
      {notes.nextCursor && !(notes.error && notes.items.length > 0) ? (
        <div className="gv-note-status gv-note-loadmore">
          <Button size="sm" disabled={notes.loadingMore} onClick={() => void notes.loadMore()}>
            {notes.loadingMore ? "Loading…" : "加载更多"}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
