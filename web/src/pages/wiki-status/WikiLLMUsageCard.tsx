import type { LLMUsageRow } from "../../api/wiki";
import { Button } from "../../components/Button";

export interface WikiLLMUsageCardProps {
  usageDays: number;
  usage: LLMUsageRow[];
  usageError: boolean;
  onUsageDaysChange: (days: number) => void;
  onRetry: () => void;
}

export function WikiLLMUsageCard({
  usageDays,
  usage,
  usageError,
  onUsageDaysChange,
  onRetry,
}: WikiLLMUsageCardProps) {
  const usageTotals = usage.reduce(
    (totals, row) => ({
      calls: totals.calls + row.calls,
      input_tokens: totals.input_tokens + row.input_tokens,
      cache_hit_tokens: totals.cache_hit_tokens + row.cache_hit_tokens,
      cache_miss_tokens: totals.cache_miss_tokens + row.cache_miss_tokens,
      output_tokens: totals.output_tokens + row.output_tokens,
    }),
    { calls: 0, input_tokens: 0, cache_hit_tokens: 0, cache_miss_tokens: 0, output_tokens: 0 },
  );
  return (
    <section className="card wiki-llm-usage" aria-label="LLM token usage">
      <div className="wiki-llm-usage-header">
        <div className="wiki-ingestion-copy">
          <span className="wiki-eyebrow">Provider spend</span>
          <strong>LLM token usage</strong>
        </div>
        <div className="wiki-llm-usage-window">
          <label htmlFor="wiki-llm-usage-window">Window</label>
          <select
            id="wiki-llm-usage-window"
            value={usageDays}
            onChange={(event) => onUsageDaysChange(Number(event.target.value))}
          >
            <option value={1}>24h</option>
            <option value={7}>7d</option>
            <option value={30}>30d</option>
          </select>
        </div>
      </div>
      {usageError && usage.length === 0 ? (
        <div className="row">
          <p className="muted small">LLM usage is unavailable.</p>
          <Button size="sm" type="button" onClick={onRetry}>
            Retry
          </Button>
        </div>
      ) : usage.length === 0 ? (
        <p className="muted small">No LLM calls recorded in this window.</p>
      ) : (
        <>
          {usageError && (
            <div className="row">
              <p className="muted small">
                LLM usage refresh failed; showing the last successful window.
              </p>
              <Button size="sm" type="button" onClick={onRetry}>
                Retry
              </Button>
            </div>
          )}
          <table className="wiki-llm-usage-table">
            <thead>
              <tr>
                <th>Component</th>
                <th>Model</th>
                <th>Calls</th>
                <th>Input</th>
                <th>Cache hit</th>
                <th>Cache miss</th>
                <th>Output</th>
              </tr>
            </thead>
            <tbody>
              {usage.map((row) => (
                <tr key={`${row.component}-${row.model}`}>
                  <td>{row.component}</td>
                  <td>{row.model}</td>
                  <td>{row.calls.toLocaleString()}</td>
                  <td>{row.input_tokens.toLocaleString()}</td>
                  <td>{row.cache_hit_tokens.toLocaleString()}</td>
                  <td>{row.cache_miss_tokens.toLocaleString()}</td>
                  <td>{row.output_tokens.toLocaleString()}</td>
                </tr>
              ))}
              <tr className="wiki-llm-usage-totals">
                <td>Total</td>
                <td />
                <td>{usageTotals.calls.toLocaleString()}</td>
                <td>{usageTotals.input_tokens.toLocaleString()}</td>
                <td>{usageTotals.cache_hit_tokens.toLocaleString()}</td>
                <td>{usageTotals.cache_miss_tokens.toLocaleString()}</td>
                <td>{usageTotals.output_tokens.toLocaleString()}</td>
              </tr>
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}
