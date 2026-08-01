// One-time enrollment token display: shown once right after issuance, with a
// "copy client connect command" extra action.

import type { EnrollmentSecret } from "../../api/types";
import { copyTextToClipboard } from "../../lib/clipboard";
import { enrollmentConnectCommand, isSelfDescribingEnrollmentToken } from "../../lib/enrollment";
import { Button } from "../Button";
import { SecretCard } from "../SecretCard";
import { useToast } from "../Toasts";

export function EnrollmentSecretCard({
  secret,
  onClose,
}: {
  secret: EnrollmentSecret;
  onClose: () => void;
}) {
  const toast = useToast();
  return (
    <SecretCard
      title="One-time Enrollment token (shown only once)"
      value={secret.token}
      valueLabel=" token"
      expiresAt={secret.expires_at}
      note={
        isSelfDescribingEnrollmentToken(secret.token)
          ? "The token is never written to durable storage, logs, or analytics. If lost, revoke it and issue a new one; the token embeds the connect address so clients can parse it directly; the client performs the exchange, and the Portal never sees the API key."
          : "The token is never written to durable storage, logs, or analytics. If lost, revoke it and issue a new one; the client performs the exchange, and the Portal never sees the API key."
      }
      extraActions={
        <Button
          size="sm"
          onClick={() => {
            const command = enrollmentConnectCommand(secret.token, window.location.origin);
            void copyTextToClipboard(command).then((ok) => {
              if (ok) toast("ok", "Connect command copied");
              else window.prompt("Copy manually:", command);
            });
          }}
        >
          Copy client command
        </Button>
      }
      onClose={onClose}
    />
  );
}
