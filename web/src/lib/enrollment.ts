/**
 * Build the paxl connect command for an enrollment token. Three-segment
 * (self-describing) tokens carry the deployment origin, so paxl resolves it
 * from the token itself; two-segment legacy tokens still need an explicit
 * --url. See docs/decisions/2026-07-22-self-describing-enrollment-token.md.
 */
export function isSelfDescribingEnrollmentToken(token: string): boolean {
  return token.startsWith("tm_enroll_") && token.split(".").length === 3;
}

export function enrollmentConnectCommand(token: string, origin: string): string {
  const base = "paxl channel connect onprem";
  if (isSelfDescribingEnrollmentToken(token)) {
    return `${base} --enrollment-token ${token}`;
  }
  return `${base} --url ${origin} --enrollment-token ${token}`;
}
