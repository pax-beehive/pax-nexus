// Single request wrapper for all Human Portal API calls (integration doc 3.2).
// - same-origin relative /v1/... URLs, credentials: "include"
// - double-submit CSRF: tm_csrf cookie -> X-CSRF-Token header on mutations
// - throws ApiError on !ok; structured error codes drive flow control and
//   the diagnostic message remains display/debug context only (doc section 9)
// - no global 401 redirect here; route guards own auth transitions

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly diagnostic: string,
    readonly code?: string,
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
    try {
      const parsed = JSON.parse(body) as { code?: unknown; message?: unknown };
      if (typeof parsed.code === "string" && typeof parsed.message === "string") {
        throw new ApiError(response.status, parsed.message, parsed.code);
      }
    } catch (err) {
      if (err instanceof ApiError) throw err;
    }
    throw new ApiError(response.status, body);
  }
  // DELETE and other empty-body responses must not fail JSON parsing.
  const text = await response.text();
  return (text ? JSON.parse(text) : undefined) as T;
}
