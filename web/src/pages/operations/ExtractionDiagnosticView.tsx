// Extraction diagnostic view: one retained extraction run with its source
// events, admission candidates and resulting Team Notes.

import type { ExtractionDiagnostic } from "../../api/types";
import { formatTime } from "../../lib/format";
import { Stat } from "./Stat";

export function ExtractionDiagnosticView({ diagnostic }: { diagnostic: ExtractionDiagnostic }) {
  return (
    <>
      <div className="stat-grid">
        <Stat label="status" value={diagnostic.run.status} />
        <Stat label="agent" value={<code>{diagnostic.run.agent_id}</code>} />
        <Stat label="session" value={<code>{diagnostic.run.session_id}</code>} />
        <Stat label="sequence" value={`${diagnostic.run.from_sequence}–${diagnostic.run.to_sequence}`} />
        <Stat label="model" value={diagnostic.run.model} />
        <Stat label="prompt version" value={diagnostic.run.prompt_version} />
        <Stat label="tokens" value={`${diagnostic.run.input_tokens}/${diagnostic.run.output_tokens}`} />
        <Stat label="error code" value={diagnostic.run.error_code ?? "—"} />
        <Stat label="created" value={formatTime(diagnostic.run.created_at)} />
        <Stat label="completed" value={
          diagnostic.run.completed_at ? formatTime(diagnostic.run.completed_at) : "—"
        } />
      </div>
      <h3>Source events</h3>
      {diagnostic.source_events.map((event) => (
        <article className="explorer-event" key={event.event_id}>
          <code className="small">#{event.sequence} · {event.type}</code>
          <p>{event.content}</p>
        </article>
      ))}
      {diagnostic.source_events.length === 0 && <p className="faint small">None</p>}
      <h3>Candidates</h3>
      {diagnostic.candidates.map((candidate) => (
        <article className="explorer-event" key={candidate.candidate_id}>
          <div className="row between">
            <strong>{candidate.subject}</strong>
            <span className={`badge ${candidate.admission_status === "admitted" ? "b-active" : "b-suspended"}`}>
              {candidate.admission_status}
            </span>
          </div>
          <p>{candidate.body}</p>
          {candidate.rejection_reason && <code>{candidate.rejection_reason}</code>}
          {candidate.resulting_note_id && (
            <div style={{ marginTop: 8 }}>
              <a href={`/admin/explorer/notes/${encodeURIComponent(candidate.resulting_note_id)}`}>
                Open resulting Team Note
              </a>
            </div>
          )}
        </article>
      ))}
      <h3>Resulting Team Notes</h3>
      {diagnostic.resulting_notes.length === 0 ? (
        <p className="faint small">None</p>
      ) : (
        <ul>
          {diagnostic.resulting_notes.map((note) => (
            <li key={note.note_id}>
              <a href={`/admin/explorer/notes/${encodeURIComponent(note.note_id)}`}>
                {note.subject}
              </a>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
