import { useEffect, useState } from "react";
import { getLLMUsage, type LLMUsageRow } from "../../api/wiki";
import { Kicker } from "../../components/Kicker";
import { isAbortError } from "../../lib/usePolling";
import { WikiLLMUsageCard } from "../wiki-status/WikiLLMUsageCard";

/**
 * /settings/usage — LLM token spend behind memory generation. Split from
 * the former WikiStatusPage (phase 6 task 1); fetches its own data since
 * this route never mounts alongside MemoryRulesPage.
 */
export function ModelUsagePage() {
  // LLM token usage card: reloaded whenever the window selector changes.
  const [usageDays, setUsageDays] = useState(7);
  const [usage, setUsage] = useState<LLMUsageRow[]>([]);
  const [usageError, setUsageError] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    getLLMUsage(usageDays, controller.signal)
      .then((result) => {
        setUsage(result.rows);
        setUsageError(false);
      })
      .catch((error: unknown) => {
        if (isAbortError(error)) return;
        setUsageError(true);
      });
    return () => controller.abort();
  }, [usageDays]);

  return (
    <>
      <div className="page-head">
        <div>
          <Kicker>Settings · model usage</Kicker>
          <h1>记忆跑起来要花多少</h1>
        </div>
      </div>

      <WikiLLMUsageCard
        usageDays={usageDays}
        usage={usage}
        usageError={usageError}
        onUsageDaysChange={setUsageDays}
      />
    </>
  );
}
