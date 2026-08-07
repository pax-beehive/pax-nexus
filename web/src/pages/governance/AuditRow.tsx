import { useEffect, useRef, useState, type ReactNode } from "react";
import { getAuditEvent } from "../../api/queries";
import type { AgentProfile, AuditEvent, Member } from "../../api/types";
import { useErrorHandler } from "../../lib/useErrorHandler";
import { formatTime } from "../../lib/format";
import { Tag } from "../../components/Tag";

export interface LabelDirectory {
  members: Map<string, Member>;
  agents: Map<string, AgentProfile>;
}

/**
 * Non-authoritative label enrichment (doc section 5.8): audit events carry
 * only IDs, so we resolve labels from already-loaded member/agent data and
 * always keep the raw ID visible as fallback for deleted objects. The design
 * (phase 5 §2.1) requires the resolved name first with the raw ID trailing
 * in a small parenthetical, never the name alone.
 */
function Label({ id, directory }: { id: string; directory: LabelDirectory }) {
  const member = directory.members.get(id);
  if (member) {
    return (
      <span>
        {/* M7: this parenthetical is the spec-mandated raw ID (§2.1), not
            optional secondary text -- `.gv-id-ref` (governance.css) instead
            of `.faint`, which measured below AA on all three themes here. */}
        {member.email ?? member.display_name} <span className="gv-id-ref small">({id})</span>
      </span>
    );
  }
  const agent = directory.agents.get(id);
  if (agent) {
    return (
      <span>
        {agent.display_name} <span className="gv-id-ref small">({id})</span>
      </span>
    );
  }
  return <code>{id}</code>;
}

function actorId(event: AuditEvent): string {
  return (
    event.actor_membership_id ??
    event.actor_user_id ??
    event.actor_agent_id ??
    event.actor_credential_id ??
    "—"
  );
}

function DetailField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <span className="faint">{label}: </span>
      {children}
    </div>
  );
}

/**
 * One audit row. The whole row is a button (design §2.1); clicking it
 * expands detail in place below. The detail fetch (`getAuditEvent`) is owned
 * here and fires at most once per row — the fetched flag survives collapse,
 * so re-expanding never re-fetches.
 */
export function AuditRow({
  event,
  directory,
  expanded,
  onToggle,
}: {
  event: AuditEvent;
  directory: LabelDirectory;
  expanded: boolean;
  onToggle: () => void;
}) {
  const handleError = useErrorHandler();
  const [detail, setDetail] = useState<AuditEvent | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const fetchedRef = useRef(false);

  useEffect(() => {
    if (!expanded || fetchedRef.current) return;
    fetchedRef.current = true;
    setDetailLoading(true);
    let cancelled = false;
    getAuditEvent(event.audit_event_id)
      .then((result) => {
        if (!cancelled) setDetail(result);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        fetchedRef.current = false;
        handleError(err);
      })
      .finally(() => {
        if (!cancelled) setDetailLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [expanded, event.audit_event_id, handleError]);

  return (
    <>
      <button
        type="button"
        className="gv-audit-row"
        onClick={onToggle}
        aria-expanded={expanded}
      >
        <span className="gv-audit-time">{formatTime(event.occurred_at)}</span>
        <Tag tone="neutral">{event.target_kind}</Tag>
        <span>
          <span className="faint small">{event.actor_kind}: </span>
          <Label id={actorId(event)} directory={directory} />
        </span>
        <span className="gv-audit-action">{event.action}</span>
        <span className="gv-audit-target">
          <Label id={event.target_id} directory={directory} />
        </span>
      </button>
      {expanded && (
        <div className="gv-audit-open">
          {detailLoading ? (
            "Loading…"
          ) : detail ? (
            <div style={{ display: "grid", gap: 4 }}>
              <DetailField label="audit_event_id">
                <code>{detail.audit_event_id}</code>
              </DetailField>
              <DetailField label="occurred_at">{formatTime(detail.occurred_at)}</DetailField>
              <DetailField label="action">
                <span className="mono">{detail.action}</span>
              </DetailField>
              <DetailField label="actor_kind">{detail.actor_kind}</DetailField>
              {detail.actor_user_id && (
                <DetailField label="actor_user_id">
                  <Label id={detail.actor_user_id} directory={directory} />
                </DetailField>
              )}
              {detail.actor_membership_id && (
                <DetailField label="actor_membership_id">
                  <Label id={detail.actor_membership_id} directory={directory} />
                </DetailField>
              )}
              {detail.actor_agent_id && (
                <DetailField label="actor_agent_id">
                  <Label id={detail.actor_agent_id} directory={directory} />
                </DetailField>
              )}
              {detail.actor_credential_id && (
                <DetailField label="actor_credential_id">
                  <Label id={detail.actor_credential_id} directory={directory} />
                </DetailField>
              )}
              <DetailField label="target_kind">{detail.target_kind}</DetailField>
              <DetailField label="target_id">
                <Label id={detail.target_id} directory={directory} />
              </DetailField>
            </div>
          ) : null}
        </div>
      )}
    </>
  );
}
