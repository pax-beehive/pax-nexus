import { useParams } from "react-router-dom";
import { getAdminAgent } from "../api/queries";
import type { HumanMe } from "../api/types";
import { can } from "../lib/capabilities";
import { useAgentDetail } from "../lib/useAgentDetail";
import { AgentDetailView } from "../components/AgentDetailView";

/**
 * Admin governance view of a single agent (doc section 5.7). Admin may only
 * suspend; edit / resume / retire / transfer are Owner-only. Enrollment
 * issuance stays with the owning human — admins can view and revoke only.
 */
export function AdminAgentDetailPage({ me }: { me: HumanMe }) {
  const { agentId = "" } = useParams();
  const { agent, setAgent, notFound, refetch } = useAgentDetail(getAdminAgent, agentId);

  const actorRole = me.role ?? "member";
  const mayGovern = can(actorRole, "govern.any-agent");

  return (
    <AgentDetailView
      scope="admin"
      backTo="/admin/agents"
      agent={agent}
      notFound={notFound}
      showOwner
      canEdit={mayGovern}
      canSuspend={can(actorRole, "suspend.any-agent")}
      canResume={mayGovern}
      canRetire={mayGovern}
      canIssue={false}
      onChanged={setAgent}
      refetch={refetch}
    />
  );
}
