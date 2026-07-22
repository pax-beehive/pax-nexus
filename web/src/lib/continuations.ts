// Login continuations (doc section 4). Two separate sessionStorage channels:
// - return_url: internal path restored after the OIDC round trip
// - pending_invitation: one-time invitation token captured from the join URL
// They must never be mixed. Everything here is tab-scoped sessionStorage;
// secrets never touch localStorage.

const RETURN_URL_KEY = "return_url";
const PENDING_INVITATION_KEY = "pending_invitation";

/**
 * Accept only same-origin absolute paths to prevent open redirects.
 * Rejects protocol-relative ("//host"), backslash tricks, and control chars.
 */
export function isInternalPath(path: string): boolean {
  if (!path.startsWith("/") || path.startsWith("//")) return false;
  // eslint-disable-next-line no-control-regex
  if (/[\\]/.test(path) || /[\x00-\x1f]/.test(path)) return false;
  return true;
}

export function saveReturnUrl(path: string): void {
  if (isInternalPath(path)) sessionStorage.setItem(RETURN_URL_KEY, path);
}

/** Read-once-then-delete; re-validates the stored value before returning it. */
export function takeReturnUrl(): string | undefined {
  const value = sessionStorage.getItem(RETURN_URL_KEY);
  if (value !== null) sessionStorage.removeItem(RETURN_URL_KEY);
  return value !== null && isInternalPath(value) ? value : undefined;
}

export function savePendingInvitation(token: string): void {
  sessionStorage.setItem(PENDING_INVITATION_KEY, token);
}

export function peekPendingInvitation(): string | undefined {
  return sessionStorage.getItem(PENDING_INVITATION_KEY) ?? undefined;
}

export function clearPendingInvitation(): void {
  sessionStorage.removeItem(PENDING_INVITATION_KEY);
}
