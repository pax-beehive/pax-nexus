// 右栏「每一次被端到 Agent 面前」：召回决策挂在笔记（而非版本）上，
// 用 describeRecall 把三类原因（拒绝/预算丢弃/硬门失败）拼成一句人话。
import type { ExplorerRecallUse } from "../../api/types";
import { EmptyState } from "../../components/EmptyState";
import { formatTime } from "../../lib/format";
import { describeRecall } from "./provenance";

// describeRecall (provenance.ts) spreads the three reason arrays without a
// null guard, and the backend's `omitempty` on a nil []string can round-trip
// as JSON `null` rather than `[]` (the previous Explorer detail page had the
// same edge case — see the now-superseded "backend omits empty reason
// arrays" coverage). Normalize here rather than touching provenance.ts,
// which is Task 1's already-landed contract.
function normalizeReasons(use: ExplorerRecallUse): ExplorerRecallUse {
  return {
    ...use,
    rejection_reasons: use.rejection_reasons ?? [],
    budget_drop_reasons: use.budget_drop_reasons ?? [],
    hard_gate_failures: use.hard_gate_failures ?? [],
  };
}

export function NoteRecalls({ recalls }: { recalls: ExplorerRecallUse[] }) {
  return (
    <section className="card">
      <h2>每一次被端到 Agent 面前</h2>
      {recalls.length === 0 ? (
        <EmptyState
          title="还没有召回记录"
          body="这条 Team Note 还没有被任何一次召回决策命中过。"
        />
      ) : (
        <table>
          <thead>
            <tr>
              <th>时间</th>
              <th>接收方</th>
              <th>结果</th>
            </tr>
          </thead>
          <tbody>
            {recalls.map((recall) => (
              <tr key={recall.observation_id}>
                <td className="small" title={recall.occurred_at}>
                  {formatTime(recall.occurred_at)}
                </td>
                <td>
                  <code>
                    {recall.recipient_agent_id}/{recall.recipient_session_id}
                  </code>
                </td>
                <td className="small">{describeRecall(normalizeReasons(recall))}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
