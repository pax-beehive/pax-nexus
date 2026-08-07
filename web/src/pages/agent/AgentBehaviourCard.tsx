// Recent behaviour：近 7 天的三个计数。
//
// 设计稿是「工具调用 · 24h / 高危 / 未批准」。实际改动两处（spec §8）：
// - activity 端点按 YYYY-MM-DD 天聚合，凑不出滚动 24 小时，写 24h 是谎报口径
// - 未批准数只能靠拉一页 tool-calls 数条数，会被 limit 截成假数字；
//   activity 直接给 session_count，精确
//
// 组件自己不做能力门控——有没有 view.audit 由调用方（AgentDetailPage）
// 决定挂不挂它。取数失败只塌自己这一格，不影响页面其余部分，也不调
// useErrorHandler 弹全局错误。

import { useEffect, useState } from "react";
import { listSessionAuditActivity } from "../../api/queries";
import type { SessionAuditActivityDay } from "../../api/types";
import { Card } from "../../components/Card";
import { MetricTile } from "../../components/MetricTile";

const DAY_MS = 24 * 60 * 60 * 1000;

/** 本地日期的 YYYY-MM-DD。刻意不用 toISOString——那是 UTC。 */
function dayKey(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

/** 含两端的 7 天窗口。 */
export function activityWindow(now: Date): { from_day: string; to_day: string } {
  return { from_day: dayKey(new Date(now.getTime() - 6 * DAY_MS)), to_day: dayKey(now) };
}

export function sumActivity(days: SessionAuditActivityDay[]): {
  toolCalls: number;
  highRisk: number;
  sessions: number;
} {
  return days.reduce(
    (acc, d) => ({
      toolCalls: acc.toolCalls + d.tool_call_count,
      highRisk: acc.highRisk + d.high_risk_count,
      sessions: acc.sessions + d.session_count,
    }),
    { toolCalls: 0, highRisk: 0, sessions: 0 },
  );
}

export function AgentBehaviourCard({ agentId }: { agentId: string }) {
  const [totals, setTotals] = useState<ReturnType<typeof sumActivity> | undefined>();
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    const window = activityWindow(new Date());
    listSessionAuditActivity({ agent_id: agentId, ...window })
      .then((days) => {
        if (!cancelled) setTotals(sumActivity(days));
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [agentId]);

  return (
    <Card title="Recent behaviour">
      {failed ? (
        <p className="small muted">近期行为没取到。页面其余部分不受影响。</p>
      ) : totals === undefined ? (
        <p className="small muted">加载中…</p>
      ) : (
        <div className="ag-metrics">
          <MetricTile label="工具调用 · 7 天" value={String(totals.toolCalls)} />
          <MetricTile label="高危 · 7 天" value={String(totals.highRisk)} />
          <MetricTile label="会话 · 7 天" value={String(totals.sessions)} />
        </div>
      )}
    </Card>
  );
}
