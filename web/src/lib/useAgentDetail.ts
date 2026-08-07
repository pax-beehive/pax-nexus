// Agent-detail loading for the merged /management/agents/:agentId route
// (AgentDetailPage, its only consumer): one fetch per agentId with a
// cancelled flag, 404 maps to a notFound state, and every other error goes
// through the shared page error handler.

import { useCallback, useEffect, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { apiError } from "../api/client";
import type { AgentProfile } from "../api/types";
import { useErrorHandler } from "./useErrorHandler";

export function useAgentDetail(
  fetcher: (agentId: string) => Promise<AgentProfile>,
  agentId: string,
): {
  agent: AgentProfile | undefined;
  setAgent: Dispatch<SetStateAction<AgentProfile | undefined>>;
  notFound: boolean;
  refetch: () => Promise<AgentProfile>;
} {
  const handleError = useErrorHandler();
  const [agent, setAgent] = useState<AgentProfile | undefined>();
  const [notFound, setNotFound] = useState(false);

  const refetch = useCallback(async () => {
    const fresh = await fetcher(agentId);
    setAgent(fresh);
    return fresh;
  }, [fetcher, agentId]);

  useEffect(() => {
    let cancelled = false;
    fetcher(agentId)
      .then((a) => {
        if (!cancelled) setAgent(a);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        // 404: do not distinguish hidden from nonexistent (doc section 9).
        if (apiError(err, 404)) setNotFound(true);
        else handleError(err);
      });
    return () => {
      cancelled = true;
    };
  }, [fetcher, agentId, handleError]);

  return { agent, setAgent, notFound, refetch };
}
