import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { getTeamNote } from "../api/queries";
import type { TeamNoteDetail } from "../api/types";
import { Badge } from "../components/Badge";
import { formatTime } from "../lib/format";
import { useErrorHandler } from "../lib/useErrorHandler";

type DetailState =
  | { status: "loading" }
  | { status: "ready"; detail: TeamNoteDetail }
  | { status: "error" };

function Chips({ values }: { values: string[] | null | undefined }) {
  const list = values ?? [];
  return list.length === 0 ? (
    <span className="faint small">None</span>
  ) : (
    <div className="chips">
      {list.map((value) => (
        <code key={value}>{value}</code>
      ))}
    </div>
  );
}

export function AdminTeamNoteDetailPage() {
  const { noteId = "" } = useParams();
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
      <div className="note warn">
        This Team Note could not be loaded. It may have expired or been removed by retention.
      </div>
    );
  }

  const { note, revisions, recall_observations: recalls } = state.detail;
  const summary = note.summary;

  return (
    <>
      <div className="page-head">
        <div>
          <Link className="small" to="/admin/explorer">
            ← Team Memory Explorer
          </Link>
          <h1 style={{ marginTop: 8 }}>{summary.subject}</h1>
          <div className="row wrap">
            <span className="badge b-role">{summary.kind}</span>
            <Badge status={summary.state} />
            <code>{summary.note_id}</code>
          </div>
        </div>
      </div>

      <div className="explorer-chain" aria-label="Team Note diagnostic chain">
        <span>Source events</span><b>→</b>
        <span>Extraction</span><b>→</b>
        <span>Candidate</span><b>→</b>
        <span>Revision {summary.revision}</span><b>→</b>
        <span>Deliveries</span><b>→</b>
        <span>Recall</span>
      </div>

      <div className="grid">
        <section className="card">
          <h2>Current Team Note</h2>
          <p className="explorer-content">{note.body}</p>
          <dl className="kv">
            <dt>Origin agent</dt><dd><code>{summary.origin_agent_id}</code></dd>
            <dt>Origin session</dt><dd><code>{note.origin_session_id}</code></dd>
            <dt>Created</dt><dd>{formatTime(summary.created_at)}</dd>
            <dt>Updated</dt><dd>{formatTime(summary.updated_at)}</dd>
            <dt>Soft expiry</dt><dd>{formatTime(summary.soft_expires_at)}</dd>
            <dt>Hard expiry</dt><dd>{formatTime(summary.hard_expires_at)}</dd>
          </dl>
        </section>
        <section className="card">
          <h2>Relations</h2>
          <h3>Related subjects</h3>
          <Chips values={note.related_subjects} />
          <h3>Related Team Notes</h3>
          {state.detail.related_notes.length === 0 ? (
            <p className="faint small">None</p>
          ) : (
            <ul>
              {state.detail.related_notes.map((related) => (
                <li key={related.note_id}>
                  <Link to={`/admin/explorer/notes/${encodeURIComponent(related.note_id)}`}>
                    {related.subject}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>

      <h2>Provenance timeline</h2>
      {revisions.length === 0 ? (
        <div className="card"><p className="muted small">No revision provenance is available.</p></div>
      ) : (
        revisions.map((revision) => (
          <section className="card explorer-revision" key={revision.revision}>
            <div className="row between wrap">
              <h2 className="flush">Revision {revision.revision}</h2>
              <span className="badge b-role">{revision.operation}</span>
            </div>
            <div className="explorer-stage">
              <h3>1 · Source events</h3>
              {revision.evidence.map((event) => (
                <article className="explorer-event" key={event.event_id}>
                  <div className="row between wrap small">
                    <code>{event.agent_id}/{event.session_id} #{event.sequence}</code>
                    <span title={event.occurred_at}>{formatTime(event.occurred_at)}</span>
                  </div>
                  <p>{event.content}</p>
                </article>
              ))}
              {revision.evidence.length === 0 && <p className="faint small">No evidence rows.</p>}
            </div>
            <div className="explorer-stage">
              <h3>2 · Extraction</h3>
              <div className="row wrap">
                <Link to={`/admin/operations?detail=extraction_run&id=${encodeURIComponent(revision.extraction.run_id)}`}>
                  <code>{revision.extraction.run_id}</code>
                </Link>
                <span className="badge b-active">{revision.extraction.status}</span>
                <span className="small faint">
                  {revision.extraction.model} · {revision.extraction.prompt_version} ·{" "}
                  {revision.extraction.input_tokens}/{revision.extraction.output_tokens} tokens
                </span>
              </div>
            </div>
            <div className="explorer-stage">
              <h3>3 · Candidate</h3>
              <div className="row between wrap">
                <strong>{revision.candidate.subject}</strong>
                <span className={`badge ${
                  revision.candidate.admission_status === "admitted" ? "b-active" : "b-suspended"
                }`}>
                  {revision.candidate.admission_status}
                </span>
              </div>
              <p className="explorer-content">{revision.candidate.body}</p>
              <div className="chips">
                <code>{revision.candidate.candidate_id}</code>
                <code>{revision.candidate.action}</code>
                <code>{revision.candidate.kind}</code>
                {revision.candidate.rejection_reason && (
                  <code>{revision.candidate.rejection_reason}</code>
                )}
              </div>
            </div>
            <div className="explorer-stage">
              <h3>4 · Persisted revision</h3>
              <p className="explorer-content">{revision.body}</p>
            </div>
            <div className="explorer-stage">
              <h3>5 · Deliveries</h3>
              {revision.deliveries.length === 0 ? (
                <p className="faint small">Not delivered directly.</p>
              ) : (
                <table>
                  <thead><tr><th>Agent</th><th>Session</th><th>Tokens</th><th>Time</th></tr></thead>
                  <tbody>
                    {revision.deliveries.map((delivery) => (
                      <tr key={`${delivery.recipient_session_id}-${delivery.delivered_at}`}>
                        <td><code>{delivery.recipient_agent_id}</code></td>
                        <td><code>{delivery.recipient_session_id}</code></td>
                        <td>{delivery.context_tokens}</td>
                        <td>{formatTime(delivery.delivered_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </section>
        ))
      )}

      <h2>Recall decisions</h2>
      <section className="card">
        {recalls.length === 0 ? (
          <p className="muted small">No retained recall observation references this Team Note.</p>
        ) : (
          <table>
            <thead>
              <tr><th>Time</th><th>Recipient</th><th>Disposition</th><th>Delivered</th><th>Reasons</th></tr>
            </thead>
            <tbody>
              {recalls.map((recall) => (
                <tr key={recall.observation_id}>
                  <td>{formatTime(recall.occurred_at)}</td>
                  <td><code>{recall.recipient_agent_id}/{recall.recipient_session_id}</code></td>
                  <td>{recall.disposition ?? "—"}</td>
                  <td><Badge status={recall.delivered ? "active" : "expired"} /></td>
                  <td>
                    <Chips values={[
                      ...(recall.rejection_reasons ?? []),
                      ...(recall.budget_drop_reasons ?? []),
                      ...(recall.hard_gate_failures ?? []),
                    ]} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}
