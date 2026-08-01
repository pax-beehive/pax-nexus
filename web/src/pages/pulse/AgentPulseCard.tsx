// Agent pulse card: one card per agent with the type glyph, freshness status
// dot, count-up stats, last-active label, and the recent-notes list.

import { Link } from "react-router-dom";
import type { OperationsAgentStats } from "../../api/types";
import { formatTime } from "../../lib/format";
import { agentActivity, relativeAge } from "../../lib/operations";
import { useCountUp } from "../../lib/useCountUp";

const ACTIVITY_LABEL = {
  active: "Active (within 1 min)",
  recent: "Recently active (within 10 min)",
  idle: "Idle",
} as const;

/** Inline agent-type glyph; unknown types get the generic mark. */
function AgentGlyph({ type }: { type?: string }) {
  const common = {
    width: 26,
    height: 26,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.6,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    "aria-hidden": true,
  };
  switch (type) {
    case "codex":
      return (
        <svg {...common} className="pulse-glyph">
          <rect x="2.5" y="4" width="19" height="16" rx="3" />
          <path d="M7 9l3 3-3 3" />
          <line x1="12.5" y1="15" x2="17" y2="15" />
        </svg>
      );
    case "claude":
      return (
        <svg {...common} className="pulse-glyph">
          <path d="M12 3v18M3.5 12h17M6 6l12 12M18 6L6 18" />
        </svg>
      );
    case "pi":
      return (
        <svg {...common} className="pulse-glyph">
          <path d="M5 8h14M9 8c0 5-1 8-2.5 10M15 8c0 5 1 8 2.5 10" />
        </svg>
      );
    default:
      return (
        <svg {...common} className="pulse-glyph">
          <path d="M12 3l7.8 4.5v9L12 21l-7.8-4.5v-9L12 3z" />
          <circle cx="12" cy="12" r="2.4" />
        </svg>
      );
  }
}

function PulseDot({ activity }: { activity: keyof typeof ACTIVITY_LABEL }) {
  return (
    <span
      className={`pulse-dot s-${activity}`}
      title={ACTIVITY_LABEL[activity]}
      aria-label={ACTIVITY_LABEL[activity]}
    />
  );
}

function PulseStat({ label, value, title }: { label: string; value: number; title?: string }) {
  const display = useCountUp(value);
  return (
    <div className="stat" title={title}>
      <div className="stat-value">{display}</div>
      <div className="stat-label">{label}</div>
    </div>
  );
}

export interface AgentPulseCardProps {
  agent: OperationsAgentStats;
  agentType?: string;
  freshNotes: Set<string>;
  now: number;
  canExplore: boolean;
}

export function AgentPulseCard({
  agent,
  agentType,
  freshNotes,
  now,
  canExplore,
}: AgentPulseCardProps) {
  const activity = agentActivity(agent.last_active_at, now);
  return (
    <div className="card pulse-card">
      <div className="row between" style={{ alignItems: "flex-start" }}>
        <div className="row">
          <AgentGlyph type={agentType} />
          <div>
            <div className="pulse-name">{agent.display_name || agent.agent_id}</div>
            <code className="small">{agent.agent_id}</code>
          </div>
        </div>
        <PulseDot activity={activity} />
      </div>
      <div className="stat-grid pulse-stats">
        <PulseStat label="events written" value={agent.events_written} />
        <PulseStat label="notes authored" value={agent.notes_authored} />
        <PulseStat label="recalls" value={agent.recall_requests} />
        <PulseStat
          label="capsules"
          value={agent.channel_sent + agent.channel_received_accepted}
          title={`sent ${agent.channel_sent} · accepted ${agent.channel_received_accepted}`}
        />
      </div>
      <p className="faint small" style={{ marginBottom: 0 }}>
        Last active:{" "}
        {agent.last_active_at ? (
          <span title={agent.last_active_at}>{relativeAge(agent.last_active_at, now)}</span>
        ) : (
          "no activity"
        )}
        {" · "}extraction {agent.extraction_runs} runs · tokens{" "}
        {agent.extraction_input_tokens}/{agent.extraction_output_tokens}
      </p>
      {agent.recent_notes.length > 0 && (
        <ul className="pulse-notes">
          {agent.recent_notes.map((note) => (
            <li
              key={note.note_id}
              className={freshNotes.has(note.note_id) ? "fade-in" : undefined}
            >
              <span className="badge b-role">{note.kind}</span>
              {canExplore ? (
                <Link
                  className="small"
                  title={formatTime(note.created_at)}
                  to={`/admin/explorer/notes/${encodeURIComponent(note.note_id)}`}
                >
                  {note.subject}
                </Link>
              ) : (
                <span className="small" title={formatTime(note.created_at)}>
                  {note.subject}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
