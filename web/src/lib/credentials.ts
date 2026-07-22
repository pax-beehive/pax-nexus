// Credential metadata has no explicit status field (doc section 2.3): derive
// it from revoked_at / expires_at for display only. Server-side status
// filters remain the authoritative view.

export type DerivedCredentialStatus = "active" | "expired" | "revoked";

export function deriveCredentialStatus(
  credential: { revoked_at?: string | null; expires_at?: string | null },
  now: Date = new Date(),
): DerivedCredentialStatus {
  if (credential.revoked_at) return "revoked";
  if (credential.expires_at) {
    const expiresAt = new Date(credential.expires_at).getTime();
    if (!Number.isNaN(expiresAt) && expiresAt <= now.getTime()) return "expired";
  }
  return "active";
}
