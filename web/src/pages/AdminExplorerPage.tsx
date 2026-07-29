import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { listTeamNotes } from "../api/queries";
import { Badge } from "../components/Badge";
import { PagedListCard } from "../components/PagedListCard";
import { formatTime } from "../lib/format";
import { useErrorHandler } from "../lib/useErrorHandler";
import { usePagedList } from "../lib/usePagedList";

const STATES = ["all", "active", "resolved", "expired"] as const;

export function AdminExplorerPage() {
  const handleError = useErrorHandler();
  const [queryInput, setQueryInput] = useState("");
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState("");
  const [state, setState] = useState("all");
  const [agentId, setAgentId] = useState("");

  const notes = usePagedList(
    (cursor) =>
      listTeamNotes({
        q: query || undefined,
        kind: kind || undefined,
        state: state === "all" ? undefined : state,
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
    <>
      <div className="page-head">
        <div>
          <h1>Team Memory Explorer</h1>
          <p className="muted" style={{ margin: 0 }}>
            Inspect a Team Note from source event through extraction, revision, delivery, and recall
          </p>
        </div>
      </div>

      <div className="card">
        <div className="row wrap explorer-filters">
          <input
            type="search"
            aria-label="Search Team Notes"
            placeholder="Search subject or body"
            value={queryInput}
            onChange={(event) => setQueryInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") setQuery(queryInput.trim());
            }}
          />
          <button className="btn sm" onClick={() => setQuery(queryInput.trim())}>
            Search
          </button>
          <input
            aria-label="Filter by kind"
            placeholder="Kind"
            value={kind}
            onChange={(event) => setKind(event.target.value.trim())}
          />
          <input
            aria-label="Filter by Agent ID"
            placeholder="Agent ID"
            value={agentId}
            onChange={(event) => setAgentId(event.target.value.trim())}
          />
        </div>
        <div className="tabs" role="group" aria-label="Team Note state">
          {STATES.map((value) => (
            <button
              key={value}
              className={state === value ? "on" : ""}
              aria-pressed={state === value}
              onClick={() => setState(value)}
            >
              {value}
            </button>
          ))}
        </div>
      </div>

      <PagedListCard
        list={notes}
        columns={["Team Note", "Kind", "Agent", "State", "Updated"]}
        emptyText="No matching Team Notes."
        renderRow={(note) => (
          <tr key={note.note_id}>
            <td>
              <Link to={`/admin/explorer/notes/${encodeURIComponent(note.note_id)}`}>
                {note.subject}
              </Link>
              <div className="small mono faint">
                {note.note_id} · revision {note.revision}
              </div>
              {(note.task_ref || note.thread_ref) && (
                <div className="small faint">
                  {note.task_ref && `task ${note.task_ref}`}
                  {note.task_ref && note.thread_ref && " · "}
                  {note.thread_ref && `thread ${note.thread_ref}`}
                </div>
              )}
            </td>
            <td>
              <span className="badge b-role">{note.kind}</span>
            </td>
            <td>
              <code>{note.origin_agent_id}</code>
            </td>
            <td>
              <Badge status={note.state} />
            </td>
            <td className="small" title={note.updated_at}>
              {formatTime(note.updated_at)}
            </td>
          </tr>
        )}
      />
    </>
  );
}
