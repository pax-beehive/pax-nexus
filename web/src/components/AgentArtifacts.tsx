// Enrollment issuance + enrollment/credential metadata lists, shared between
// the owner view (/v1/me/agents/...) and the admin governance view
// (/v1/admin/agents/...). Presentation lives under ./artifacts; this entry
// owns the state, data hooks, and revoke flow and wires the pieces together.

import { useState } from "react";
import {
  beginAction,
  revokeCredential,
  revokeEnrollment,
  type AgentScope,
} from "../api/actions";
import { listCredentials, listEnrollments } from "../api/queries";
import type { EnrollmentSecret } from "../api/types";
import { usePagedList } from "../lib/usePagedList";
import { useErrorHandler } from "../lib/useErrorHandler";
import { ConfirmDialog } from "./ConfirmDialog";
import { useToast } from "./Toasts";
import { CredentialsCard } from "./artifacts/CredentialsCard";
import { EnrollmentsCard } from "./artifacts/EnrollmentsCard";
import { EnrollmentSecretCard } from "./artifacts/EnrollmentSecretCard";
import { IssueEnrollmentModal } from "./artifacts/IssueEnrollmentModal";

export interface AgentArtifactsProps {
  scope: AgentScope;
  agentId: string;
  agentStatus: string;
  canIssue: boolean;
}

export function AgentArtifacts({
  scope,
  agentId,
  agentStatus,
  canIssue,
}: AgentArtifactsProps) {
  const toast = useToast();
  const handleError = useErrorHandler();
  const [secret, setSecret] = useState<EnrollmentSecret | undefined>();
  const [issueOpen, setIssueOpen] = useState(false);
  const [enrollmentFilter, setEnrollmentFilter] = useState<string>("all");
  const [credentialFilter, setCredentialFilter] = useState<string>("all");
  // Pending revoke confirmation; the Idempotency-Key is bound to the action
  // instance (one per opened dialog, reused if the confirm is retried).
  const [revokeTarget, setRevokeTarget] = useState<
    | { kind: "enrollment"; id: string; key: string }
    | { kind: "credential"; id: string; key: string }
    | undefined
  >();
  const [busy, setBusy] = useState(false);

  const enrollments = usePagedList(
    (cursor) =>
      listEnrollments(scope, agentId, {
        status: enrollmentFilter === "all" ? undefined : enrollmentFilter,
        cursor,
      }),
    [scope, agentId, enrollmentFilter],
  );

  const credentials = usePagedList(
    (cursor) =>
      listCredentials(scope, agentId, {
        status: credentialFilter === "all" ? undefined : credentialFilter,
        cursor,
      }),
    [scope, agentId, credentialFilter],
  );

  const confirmRevoke = async () => {
    if (!revokeTarget || busy) return;
    setBusy(true);
    try {
      if (revokeTarget.kind === "enrollment") {
        await revokeEnrollment(scope, agentId, revokeTarget.id, revokeTarget.key);
        toast("ok", "Enrollment revoked");
        enrollments.reload();
      } else {
        await revokeCredential(scope, agentId, revokeTarget.id, revokeTarget.key);
        toast("ok", "Credential revoked; the API key stops working immediately");
        credentials.reload();
      }
      setRevokeTarget(undefined);
    } catch (err) {
      handleError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      {secret && (
        <EnrollmentSecretCard secret={secret} onClose={() => setSecret(undefined)} />
      )}

      <EnrollmentsCard
        agentStatus={agentStatus}
        canIssue={canIssue}
        onIssue={() => setIssueOpen(true)}
        filter={enrollmentFilter}
        onFilterChange={setEnrollmentFilter}
        enrollments={enrollments}
        onRevoke={(id) => setRevokeTarget({ kind: "enrollment", id, key: beginAction() })}
      />

      <CredentialsCard
        filter={credentialFilter}
        onFilterChange={setCredentialFilter}
        credentials={credentials}
        onRevoke={(id) => setRevokeTarget({ kind: "credential", id, key: beginAction() })}
      />

      {issueOpen && (
        <IssueEnrollmentModal
          agentId={agentId}
          onClose={() => setIssueOpen(false)}
          onCreated={(s) => {
            setIssueOpen(false);
            setSecret(s);
            enrollments.reload();
          }}
          onMaybeCreated={() => {
            setIssueOpen(false);
            enrollments.reload();
          }}
        />
      )}

      {revokeTarget && (
        <ConfirmDialog
          title={revokeTarget.kind === "enrollment" ? "Revoke Enrollment" : "Revoke Credential"}
          consequences={
            revokeTarget.kind === "enrollment"
              ? ["The one-time token stops working immediately and any in-progress client onboarding will fail", "This cannot be undone; issue a new Enrollment if needed"]
              : ["The API key stops working immediately and the Agent client holding it loses access", "This cannot be undone; issue a new Enrollment if needed"]
          }
          confirmLabel="Confirm revoke"
          busy={busy}
          onConfirm={() => void confirmRevoke()}
          onClose={() => setRevokeTarget(undefined)}
        />
      )}
    </>
  );
}
