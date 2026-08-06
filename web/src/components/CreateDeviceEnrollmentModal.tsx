import { useState } from "react";
import { apiError } from "../api/client";
import { createDeviceEnrollment } from "../api/actions";
import type { DeviceEnrollmentSecret } from "../api/types";
import { validateDeviceName } from "../lib/validation";
import { Button } from "./Button";
import { Modal } from "./Modal";
import { useToast } from "./Toasts";

const EXPIRY_OPTIONS = [
  { value: 300, label: "5 minutes" },
  { value: 900, label: "15 minutes" },
  { value: 1800, label: "30 minutes" },
] as const;

export function CreateDeviceEnrollmentModal({
  onClose,
  onCreated,
  onMaybeCreated,
}: {
  onClose: () => void;
  onCreated: (secret: DeviceEnrollmentSecret) => void;
  onMaybeCreated: () => void;
}) {
  const toast = useToast();
  const [deviceName, setDeviceName] = useState("");
  const [expiresIn, setExpiresIn] = useState<number>(900);
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | undefined>();

  const submit = async () => {
    const nameError = validateDeviceName(deviceName);
    if (nameError) return setFormError(nameError);
    setFormError(undefined);
    setBusy(true);
    try {
      const secret = await createDeviceEnrollment({
        device_name: deviceName.trim(),
        expires_in_seconds: expiresIn,
      });
      onCreated(secret);
    } catch (err) {
      if (apiError(err) && err.status < 500) {
        // Client-side rejection: keep the form so the user can correct it.
        setFormError(`Request rejected (HTTP ${err.status}); please check the input`);
      } else {
        // One-time secret, no Idempotency-Key (doc 3.3): never blind retry;
        // refresh the list and let the user revoke duplicates.
        toast("warn", "Request failed; no automatic retry. The list has been refreshed: if the Device already appears in it, use it or revoke it and create a new one");
        onMaybeCreated();
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal title="Create Device Enrollment" onClose={onClose}>
      <label htmlFor="de-name">device_name (required)</label>
      <input
        id="de-name"
        type="text"
        placeholder="todd-macbook-air"
        maxLength={200}
        value={deviceName}
        onChange={(e) => setDeviceName(e.target.value)}
      />
      <label htmlFor="de-exp">Token lifetime</label>
      <select id="de-exp" value={expiresIn} onChange={(e) => setExpiresIn(Number(e.target.value))}>
        {EXPIRY_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      {formError && <div className="note bad">{formError}</div>}
      <div className="note small">
        A Device Credential always carries only the <code>agent_provision</code> permission; no
        permission matrix is offered. The permission ceiling of the Agents it provisions is set by
        the deployment&apos;s grantable configuration.
      </div>
      <div className="note small">
        The create response contains a one-time token and <b>does not support Idempotency-Key</b>:
        timeouts are not retried automatically. Refresh the Devices list first to check whether a
        record was created.
      </div>
      <div className="row" style={{ justifyContent: "flex-end" }}>
        <Button variant="ghost" onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button variant="primary" disabled={busy} onClick={() => void submit()}>
          {busy ? "Creating…" : "Create"}
        </Button>
      </div>
    </Modal>
  );
}
