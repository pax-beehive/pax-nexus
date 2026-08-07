// 设备注册令牌的仪式。AccessTreePage 与 AdminDevicesPage 共用——两处此前
// 是逐字重复的两份 SecretCard 调用。

import type { DeviceEnrollmentSecret } from "../api/types";
import { deviceConnectCommand, isSelfDescribingEnrollmentToken } from "../lib/enrollment";
import { SecretCeremony } from "./SecretCeremony";

export function DeviceEnrollmentCeremony({
  secret,
  onClose,
}: {
  secret: DeviceEnrollmentSecret;
  onClose: () => void;
}) {
  return (
    <SecretCeremony
      title="One-time device enrollment token · shown once, stored nowhere"
      headline="Copy this now. We can't show it again."
      body={
        <>
          This token lets <b>{secret.device_name}</b> claim a long-lived device key. It exists
          only on this screen — not in our database, not in your email, not in the audit log.
        </>
      }
      value={secret.token}
      valueLabel="token"
      expiresAt={secret.expires_at}
      steps={[
        `Run the command below on ${secret.device_name}.`,
        "The token is exchanged for a long-lived key and burns itself.",
        "This machine appears in the access tree, and its agents can self-register.",
      ]}
      recovery={
        isSelfDescribingEnrollmentToken(secret.token)
          ? "Nothing is broken. Revoke this device and create a new enrollment — the token embeds the connect address, so the client can resolve it directly."
          : "Nothing is broken. Revoke this device and create a new enrollment."
      }
      command={deviceConnectCommand(secret.token, window.location.origin, secret.device_name)}
      onClose={onClose}
    />
  );
}
