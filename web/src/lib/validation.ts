// Form validation rules from the integration doc (sections 5.2, 5.4, 5.5).

export function validateAgentId(id: string): string | undefined {
  if (!id) return "agent_id 必填";
  if (id.length > 128) return "agent_id 不能超过 128 字符";
  // eslint-disable-next-line no-control-regex
  if (/[/\\\x00-\x1f]/.test(id)) return "agent_id 不能包含 /、\\ 或控制字符";
  return undefined;
}

export function validateDisplayName(name: string): string | undefined {
  const trimmed = name.trim();
  if (!trimmed) return "display_name 必填（trim 后不能为空）";
  if (trimmed.length > 200) return "display_name 不能超过 200 字符";
  return undefined;
}

export function validateEmail(email: string): string | undefined {
  return /^[^@\s]+@[^@\s]+$/.test(email) ? undefined : "邮箱格式不正确";
}

/** credential_expires_at is optional but must be a future time when set. */
export function validateFutureTime(value: string): string | undefined {
  if (!value) return undefined;
  const time = new Date(value).getTime();
  if (Number.isNaN(time)) return "时间格式不正确";
  if (time <= Date.now()) return "credential_expires_at 必须是未来时间";
  return undefined;
}
