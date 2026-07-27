// Form validation rules from the integration doc (sections 5.2, 5.4, 5.5).

export function validateAgentId(id: string): string | undefined {
  if (!id) return "agent_id is required";
  if (id.length > 128) return "agent_id must be at most 128 characters";
  // eslint-disable-next-line no-control-regex
  if (/[/\\\x00-\x1f]/.test(id)) return "agent_id must not contain /, \\, or control characters";
  return undefined;
}

export function validateDisplayName(name: string): string | undefined {
  const trimmed = name.trim();
  if (!trimmed) return "display_name is required (must not be empty after trimming)";
  if (trimmed.length > 200) return "display_name must be at most 200 characters";
  return undefined;
}

export function validateEmail(email: string): string | undefined {
  return /^[^@\s]+@[^@\s]+$/.test(email) ? undefined : "Invalid email format";
}

/** device_name rules from doc section 5.9. */
export function validateDeviceName(name: string): string | undefined {
  const trimmed = name.trim();
  if (!trimmed) return "device_name is required (must not be empty after trimming)";
  if (trimmed.length > 200) return "device_name must be at most 200 characters";
  // eslint-disable-next-line no-control-regex
  if (/[\x00-\x1f]/.test(trimmed)) return "device_name must not contain control characters";
  return undefined;
}

/** credential_expires_at is optional but must be a future time when set. */
export function validateFutureTime(value: string): string | undefined {
  if (!value) return undefined;
  const time = new Date(value).getTime();
  if (Number.isNaN(time)) return "Invalid time format";
  if (time <= Date.now()) return "credential_expires_at must be a future time";
  return undefined;
}
