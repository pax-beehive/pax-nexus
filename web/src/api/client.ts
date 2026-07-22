// Single request wrapper for all Human Portal API calls (integration doc 3.2).
// - same-origin relative /v1/... URLs, credentials: "include"
// - double-submit CSRF: tm_csrf cookie -> X-CSRF-Token header on mutations
// - throws ApiError on !ok; structured error codes drive flow control and
//   the diagnostic message remains display/debug context only (doc section 9)
// - 429 responses surface the Retry-After hint on ApiError.retryAfterSeconds
//   so callers can show it; automatic retries stay forbidden (doc section 9)
// - no global 401 redirect here; route guards own auth transitions

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly diagnostic: string,
    readonly code?: string,
    readonly retryAfterSeconds?: number,
  ) {
    super(`API request failed with ${status}`);
    this.name = "ApiError";
  }
}

export function readCookie(name: string): string | undefined {
  const prefix = `${encodeURIComponent(name)}=`;
  return document.cookie
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix))
    ?.slice(prefix.length);
}

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

// Retry-After may be delay-seconds or an HTTP-date (RFC 9110); anything else
// is ignored. Only consulted on 429 responses.
function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return Math.ceil(seconds);
  const date = Date.parse(value);
  if (!Number.isNaN(date)) return Math.max(0, Math.ceil((date - Date.now()) / 1000));
  return undefined;
}

export async function humanFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");

  if (!SAFE_METHODS.has(method)) {
    const csrf = readCookie("tm_csrf");
    if (!csrf) throw new Error("Missing CSRF cookie");
    headers.set("X-CSRF-Token", decodeURIComponent(csrf));
  }

  const response = await fetch(path, {
    ...init,
    method,
    headers,
    credentials: "include",
  });

  if (!response.ok) {
    const body = await response.text();
    const retryAfterSeconds =
      response.status === 429 ? parseRetryAfter(response.headers.get("Retry-After")) : undefined;
    try {
      const parsed = JSON.parse(body) as { code?: unknown; message?: unknown };
      if (typeof parsed.code === "string" && typeof parsed.message === "string") {
        throw new ApiError(response.status, parsed.message, parsed.code, retryAfterSeconds);
      }
    } catch (err) {
      if (err instanceof ApiError) throw err;
    }
    throw new ApiError(response.status, body, undefined, retryAfterSeconds);
  }
  // DELETE and other empty-body responses must not fail JSON parsing.
  const text = await response.text();
  return (text ? JSON.parse(text) : undefined) as T;
}
