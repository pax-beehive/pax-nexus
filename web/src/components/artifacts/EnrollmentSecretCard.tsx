// One-time enrollment token display: shown once right after issuance.
//
// Phase 4 stopgap: `SecretCard` was deleted (portal-modernist-phase4 task 4)
// once its other three call sites moved to the full-screen `SecretCeremony`.
// This call site belongs to task 10 (agent identity/artifact ceremony), which
// is expected to redesign it properly; until then this keeps the build green
// with a minimal port to `SecretCeremony` that preserves the external
// `{ secret, onClose }` contract `AgentArtifacts.tsx` calls.

import type { EnrollmentSecret } from "../../api/types";
import { enrollmentConnectCommand, isSelfDescribingEnrollmentToken } from "../../lib/enrollment";
import { SecretCeremony } from "../SecretCeremony";

export function EnrollmentSecretCard({
  secret,
  onClose,
}: {
  secret: EnrollmentSecret;
  onClose: () => void;
}) {
  return (
    <SecretCeremony
      title="One-time Enrollment token — shown only once"
      headline="Copy it now. We can't show it to you again."
      body="This token is never written to durable storage, logs, or analytics."
      value={secret.token}
      valueLabel=" token"
      expiresAt={secret.expires_at}
      steps={[
        "Run the connect command on the target client.",
        "The token is exchanged for a long-lived credential and self-destructs.",
        "The Portal never sees the resulting API key.",
      ]}
      recovery={
        isSelfDescribingEnrollmentToken(secret.token)
          ? "If lost, revoke it and issue a new one; the token embeds the connect address so clients can parse it directly."
          : "If lost, revoke it and issue a new one."
      }
      command={enrollmentConnectCommand(secret.token, window.location.origin)}
      onClose={onClose}
    />
  );
}
