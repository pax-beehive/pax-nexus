import { useParams } from "react-router-dom";
import { getOwnAgent } from "../api/queries";
import { useAgentDetail } from "../lib/useAgentDetail";
import { AgentDetailView } from "../components/AgentDetailView";

export function AgentDetailPage() {
  const { agentId = "" } = useParams();
  const { agent, setAgent, notFound, refetch } = useAgentDetail(getOwnAgent, agentId);

  return (
    <AgentDetailView
      scope="me"
      backTo="/agents"
      agent={agent}
      notFound={notFound}
      canEdit={agent !== undefined && !agent.retired_at}
      canSuspend
      canResume
      canRetire
      canIssue={agent?.status === "active"}
      onChanged={setAgent}
      refetch={refetch}
    />
  );
}
