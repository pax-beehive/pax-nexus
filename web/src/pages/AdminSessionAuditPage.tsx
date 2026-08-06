// Session Audit admin page: findings-first, read-only view over the session
// lake audit trail. The three views (Findings, Tool Calls, Activity) are
// independent lists behind a .seg toggle; the selected view lives in the URL
// (?view=tool-calls) so it is shareable, while filters stay component-local.
// The endpoints return bounded lists (limit only, no cursor), so this page
// does not use the cursor-paginated scaffolding of the other admin pages.

import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import {
  listSessionAuditActivity,
  listSessionAuditFindings,
  listSessionAuditToolCalls,
} from "../api/queries";
import type { SessionAuditToolCall } from "../api/types";
import { useErrorHandler } from "../lib/useErrorHandler";
import { formatTime } from "../lib/format";
import { Button } from "../components/Button";
import { Kicker } from "../components/Kicker";
import { RegionError } from "../components/RegionError";
import { Seg } from "../components/Seg";
import { findingKindLabel, SessionFindingsList } from "./governance/SessionFindings";
import { SessionDaysChart } from "./governance/SessionDays";

// Fixed vocabularies from the backend session audit schema.
const RISK_LEVELS = ["low", "medium", "high", "critical"] as const;
const APPROVAL_STATES = ["unknown", "approved", "denied", "auto"] as const;
const FINDING_KINDS = [
  "high_risk_unapproved",
  "denied_tool_executed",
  "visibility_unknown",
  "attribution_missing",
] as const;

/** Severity and risk level share a vocabulary, so they share a badge. */
function RiskBadge({ level }: { level: string }) {
  return <span className={`badge b-risk-${level}`}>{level}</span>;
}

function ApprovalBadge({ state }: { state: string }) {
  return <span className={`badge b-approval-${state}`}>{state}</span>;
}

/** IDs stay copyable via the title tooltip while the cell keeps its width. */
function TruncatedId({ value }: { value: string }) {
  return (
    <code className="session-audit-truncate" title={value}>
      {value}
    </code>
  );
}

interface ListState<T> {
  items: T[];
  loading: boolean;
  error: unknown;
  reload: () => void;
}

/** Limit-bounded list fetch; any dep change (applied filters) refetches. */
function useSessionAuditList<T>(
  fetchList: () => Promise<T[]>,
  deps: readonly unknown[],
): ListState<T> {
  const [items, setItems] = useState<T[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [epoch, setEpoch] = useState(0);
  const fetchRef = useRef(fetchList);
  fetchRef.current = fetchList;

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetchRef
      .current()
      .then((list) => {
        if (cancelled) return;
        setItems(list);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setError(err);
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // Deps are the applied filters; a bump of `epoch` via reload() retries.
  }, [...deps, epoch]);

  return { items, loading, error, reload: () => setEpoch((e) => e + 1) };
}

function ListCard<T>({
  state,
  columns,
  emptyText,
  renderRow,
}: {
  state: ListState<T>;
  columns: ReactNode[];
  emptyText: string;
  renderRow: (item: T) => ReactNode;
}) {
  let body: ReactNode;
  if (state.loading) {
    body = <p className="muted small">Loading…</p>;
  } else if (state.error) {
    body = <RegionError error={state.error} onRetry={state.reload} />;
  } else if (state.items.length === 0) {
    body = <p className="muted small">{emptyText}</p>;
  } else {
    body = (
      <table>
        <thead>
          <tr>
            {columns.map((column, index) => (
              <th key={index}>{column}</th>
            ))}
          </tr>
        </thead>
        <tbody>{state.items.map(renderRow)}</tbody>
      </table>
    );
  }
  return <div className="card">{body}</div>;
}

function FindingsView({ onInspectSession }: { onInspectSession: (sessionId: string) => void }) {
  const handleError = useErrorHandler();
  const [searchParams] = useSearchParams();
  const [userInput, setUserInput] = useState("");
  const [agentInput, setAgentInput] = useState(searchParams.get("agent") ?? "");
  const [userId, setUserId] = useState("");
  const [agentId, setAgentId] = useState(searchParams.get("agent") ?? "");
  const [kind, setKind] = useState("");
  const [severity, setSeverity] = useState("");

  const state = useSessionAuditList(
    () =>
      listSessionAuditFindings({
        user_id: userId || undefined,
        agent_id: agentId || undefined,
        kind: kind || undefined,
        severity: severity || undefined,
      }),
    [userId, agentId, kind, severity],
  );

  useEffect(() => {
    if (state.error) handleError(state.error);
  }, [state.error, handleError]);

  const applyFilters = () => {
    setUserId(userInput.trim());
    setAgentId(agentInput.trim());
  };

  return (
    <>
      <div className="toolbar" style={{ marginBottom: 14 }}>
        <input
          type="text"
          placeholder="user_id"
          value={userInput}
          onChange={(e) => setUserInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyFilters();
          }}
        />
        <input
          type="text"
          placeholder="agent_id"
          value={agentInput}
          onChange={(e) => setAgentInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyFilters();
          }}
        />
        <select
          aria-label="Filter by kind"
          value={kind}
          onChange={(e) => setKind(e.target.value)}
        >
          <option value="">kind: all</option>
          {FINDING_KINDS.map((k) => (
            <option key={k} value={k}>
              {findingKindLabel(k)}
            </option>
          ))}
        </select>
        <select
          aria-label="Filter by severity"
          value={severity}
          onChange={(e) => setSeverity(e.target.value)}
        >
          <option value="">severity: all</option>
          {RISK_LEVELS.map((level) => (
            <option key={level} value={level}>
              {level}
            </option>
          ))}
        </select>
        <Button size="sm" onClick={applyFilters}>
          Apply filters
        </Button>
      </div>
      <SessionFindingsList state={state} onInspectToolCalls={onInspectSession} />
    </>
  );
}

function ToolCallsView() {
  const handleError = useErrorHandler();
  const [searchParams] = useSearchParams();
  const [userInput, setUserInput] = useState("");
  const [agentInput, setAgentInput] = useState(searchParams.get("agent") ?? "");
  // Both the draft (bound to the filter box) and applied (fed into the
  // request) states seed from ?session=, mirroring the ?agent= pattern below
  // — this is how the Findings row card's "看这些调用" jump prefills the
  // tool-calls view's session filter.
  const [sessionInput, setSessionInput] = useState(searchParams.get("session") ?? "");
  const [userId, setUserId] = useState("");
  const [agentId, setAgentId] = useState(searchParams.get("agent") ?? "");
  const [sessionId, setSessionId] = useState(searchParams.get("session") ?? "");
  const [riskLevel, setRiskLevel] = useState("");
  const [approvalState, setApprovalState] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const state = useSessionAuditList(
    () =>
      listSessionAuditToolCalls({
        user_id: userId || undefined,
        agent_id: agentId || undefined,
        session_id: sessionId || undefined,
        risk_level: riskLevel || undefined,
        approval_state: approvalState || undefined,
      }),
    [userId, agentId, sessionId, riskLevel, approvalState],
  );

  useEffect(() => {
    if (state.error) handleError(state.error);
  }, [state.error, handleError]);

  const applyFilters = () => {
    setUserId(userInput.trim());
    setAgentId(agentInput.trim());
    setSessionId(sessionInput.trim());
  };

  return (
    <>
      <div className="toolbar" style={{ marginBottom: 14 }}>
        <input
          type="text"
          placeholder="user_id"
          value={userInput}
          onChange={(e) => setUserInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyFilters();
          }}
        />
        <input
          type="text"
          placeholder="agent_id"
          value={agentInput}
          onChange={(e) => setAgentInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyFilters();
          }}
        />
        <input
          type="text"
          placeholder="session_id"
          value={sessionInput}
          onChange={(e) => setSessionInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyFilters();
          }}
        />
        <select
          aria-label="Filter by risk level"
          value={riskLevel}
          onChange={(e) => setRiskLevel(e.target.value)}
        >
          <option value="">risk: all</option>
          {RISK_LEVELS.map((level) => (
            <option key={level} value={level}>
              {level}
            </option>
          ))}
        </select>
        <select
          aria-label="Filter by approval state"
          value={approvalState}
          onChange={(e) => setApprovalState(e.target.value)}
        >
          <option value="">approval: all</option>
          {APPROVAL_STATES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <Button size="sm" onClick={applyFilters}>
          Apply filters
        </Button>
      </div>
      <ListCard
        state={state}
        columns={["Occurred", "Risk", "Tool", "Input", "Approval", "User / Agent", ""]}
        emptyText="No matching tool calls."
        renderRow={(tc: SessionAuditToolCall) => (
          <Fragment key={tc.event_id}>
            <tr>
              <td className="small">{formatTime(tc.occurred_at)}</td>
              <td>
                <RiskBadge level={tc.risk_level} />
              </td>
              <td className="mono small">{tc.tool_name}</td>
              <td>
                <span className="session-audit-truncate small" title={tc.input_summary}>
                  {tc.input_summary}
                </span>
              </td>
              <td>
                <ApprovalBadge state={tc.approval_state} />
              </td>
              <td className="small">
                <TruncatedId value={tc.user_id} /> <span className="faint">/</span>{" "}
                <TruncatedId value={tc.agent_id} />
              </td>
              <td>
                <Button
                  size="sm"
                  onClick={() => setExpandedId(expandedId === tc.event_id ? null : tc.event_id)}
                >
                  {expandedId === tc.event_id ? "Collapse" : "Details"}
                </Button>
              </td>
            </tr>
            {expandedId === tc.event_id && (
              <tr>
                <td colSpan={7}>
                  <div className="small">
                    <span className="faint">risk_reasons: </span>
                    {tc.risk_reasons.length === 0 ? (
                      <span className="faint">—</span>
                    ) : (
                      <span className="chips">
                        {tc.risk_reasons.map((reason) => (
                          <code key={reason}>{reason}</code>
                        ))}
                      </span>
                    )}
                  </div>
                </td>
              </tr>
            )}
          </Fragment>
        )}
      />
    </>
  );
}

function ActivityView() {
  const handleError = useErrorHandler();
  const [searchParams] = useSearchParams();
  const [userInput, setUserInput] = useState("");
  const [agentInput, setAgentInput] = useState(searchParams.get("agent") ?? "");
  const [fromInput, setFromInput] = useState("");
  const [toInput, setToInput] = useState("");
  const [userId, setUserId] = useState("");
  const [agentId, setAgentId] = useState(searchParams.get("agent") ?? "");
  const [fromDay, setFromDay] = useState("");
  const [toDay, setToDay] = useState("");

  const state = useSessionAuditList(
    () =>
      listSessionAuditActivity({
        user_id: userId || undefined,
        agent_id: agentId || undefined,
        from_day: fromDay || undefined,
        to_day: toDay || undefined,
      }),
    [userId, agentId, fromDay, toDay],
  );

  useEffect(() => {
    if (state.error) handleError(state.error);
  }, [state.error, handleError]);

  const applyFilters = () => {
    setUserId(userInput.trim());
    setAgentId(agentInput.trim());
    setFromDay(fromInput);
    setToDay(toInput);
  };

  return (
    <>
      <div className="toolbar" style={{ marginBottom: 14 }}>
        <input
          type="text"
          placeholder="user_id"
          value={userInput}
          onChange={(e) => setUserInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyFilters();
          }}
        />
        <input
          type="text"
          placeholder="agent_id"
          value={agentInput}
          onChange={(e) => setAgentInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") applyFilters();
          }}
        />
        <input
          type="date"
          aria-label="From day"
          value={fromInput}
          onChange={(e) => setFromInput(e.target.value)}
        />
        <input
          type="date"
          aria-label="To day"
          value={toInput}
          onChange={(e) => setToInput(e.target.value)}
        />
        <Button size="sm" onClick={applyFilters}>
          Apply filters
        </Button>
      </div>
      {state.loading ? (
        <div className="card">
          <p className="muted small">Loading…</p>
        </div>
      ) : state.error ? (
        <div className="card">
          <RegionError error={state.error} onRetry={state.reload} />
        </div>
      ) : (
        <SessionDaysChart days={state.items} />
      )}
    </>
  );
}

type AuditView = "findings" | "tool-calls" | "activity";

const VIEWS: { id: AuditView; label: string }[] = [
  { id: "findings", label: "Findings" },
  { id: "tool-calls", label: "工具调用" },
  { id: "activity", label: "按天" },
];

function viewFromParam(param: string | null): AuditView {
  return VIEWS.some((v) => v.id === param) ? (param as AuditView) : "findings";
}

export function AdminSessionAuditPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const view = viewFromParam(searchParams.get("view"));

  const selectView = (next: AuditView) => {
    // The default view keeps the URL bare; other views stay shareable.
    setSearchParams(next === "findings" ? {} : { view: next });
  };

  // Findings row card's "看这些调用" action: jump to the tool-calls view
  // with that finding's session_id pre-filled (read back by ToolCallsView
  // via ?session=, mirroring the ?agent= deep-link pattern).
  const jumpToToolCalls = (sessionId: string) => {
    setSearchParams({ view: "tool-calls", session: sessionId });
  };

  return (
    <>
      <div className="gv-head">
        <div>
          <Kicker>Governance · 会话审计</Kicker>
          <h1>Agent 到底做了什么</h1>
          <p>
            Finding 由证据推导，从不来自 prompt 或内容。每条都会点名它取材的事件，
            你可以自己去读源头。
          </p>
        </div>
      </div>
      <div className="toolbar" style={{ marginBottom: 14, padding: "0 var(--space-4)" }}>
        <Seg
          label="session audit view"
          options={VIEWS.map((v) => ({ value: v.id, label: v.label }))}
          value={view}
          onChange={selectView}
        />
      </div>
      {view === "findings" && <FindingsView onInspectSession={jumpToToolCalls} />}
      {view === "tool-calls" && <ToolCallsView />}
      {view === "activity" && <ActivityView />}
    </>
  );
}
