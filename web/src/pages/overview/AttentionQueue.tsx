// "Needs your attention" block: the overview aggregate's attention queue
// (findings, quarantines, expiring access) plus a one-shot Access summary
// strip. The queue shares the overview region's loading/ready/error state
// (passed in by OverviewPage, same as MetricsRow/ThroughputChart/NoteMixBlock);
// the Access strip is independent -- a single Promise.allSettled on mount,
// no polling, and it silently disappears on any rejection rather than owning
// its own region state (doc: portal-modernist phase 2b, Access strip).

import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { listAdminAgents, listAllMembers, listDevices } from "../../api/queries";
import type { OverviewAttentionItem } from "../../api/types";
import { Button } from "../../components/Button";
import { Card } from "../../components/Card";
import { EmptyState } from "../../components/EmptyState";
import { Tag } from "../../components/Tag";

const CTA_LABEL: Record<string, string> = {
  finding: "Review",
  quarantine: "Inspect",
  invitation: "Manage",
  enrollment: "Manage",
};

function ctaLabel(kind: string): string {
  return CTA_LABEL[kind] ?? "Review";
}

function isAccentSeverity(severity: string): boolean {
  return severity === "high" || severity === "critical";
}

interface AccessSummary {
  people: number;
  machines: number;
  agents: number;
}

/**
 * One-shot Access summary: fires once on mount, never polls. Any rejected
 * leg (a scope the caller lacks, a transient error) drops the whole strip
 * rather than rendering a partial or stale count.
 */
function useAccessSummary(): AccessSummary | undefined {
  const [summary, setSummary] = useState<AccessSummary | undefined>();

  useEffect(() => {
    let cancelled = false;
    Promise.allSettled([
      listAllMembers({ limit: 100 }),
      listDevices({ limit: 100 }),
      listAdminAgents({ limit: 100 }),
    ]).then(([people, machines, agents]) => {
      if (cancelled) return;
      if (
        people.status === "fulfilled" &&
        machines.status === "fulfilled" &&
        agents.status === "fulfilled"
      ) {
        setSummary({
          people: people.value.length,
          machines: machines.value.items.length,
          agents: agents.value.items.length,
        });
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  return summary;
}

export function AttentionQueue({ items }: { items: OverviewAttentionItem[] }) {
  const navigate = useNavigate();
  const access = useAccessSummary();

  return (
    <Card title="Needs your attention">
      {items.length === 0 ? (
        <EmptyState
          title="Nothing needs you right now"
          body="Findings, quarantines, and expiring access will land here."
        />
      ) : (
        <ul className="stack" style={{ gap: 10, listStyle: "none", margin: 0, padding: 0 }}>
          {items.map((item) => (
            <li key={item.ref} className="row" style={{ alignItems: "flex-start", gap: 12 }}>
              <Tag tone={isAccentSeverity(item.severity) ? "attention" : "neutral"}>
                {item.severity}
              </Tag>
              <div style={{ flex: 1 }}>
                <div>{item.title}</div>
                <div className="muted small">{item.body}</div>
                <div className="mono small" style={{ opacity: 0.45 }}>
                  {item.ref}
                </div>
              </div>
              <Button size="sm" onClick={() => navigate(item.target)}>
                {ctaLabel(item.kind)}
              </Button>
            </li>
          ))}
        </ul>
      )}
      {access && (
        <div className="row between note" style={{ marginTop: 14 }}>
          <span>{`${access.people} people · ${access.machines} machines · ${access.agents} agents`}</span>
          <Link to="/management">Open Management →</Link>
        </div>
      )}
    </Card>
  );
}
