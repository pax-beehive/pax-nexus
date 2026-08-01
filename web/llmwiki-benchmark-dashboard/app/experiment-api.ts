const EXPERIMENT_API = "/v1/knowledge-eval";
const EXPERIMENT_REQUEST_TIMEOUT_MS = 20_000;
let idempotencySequence = 0;

export function createExperimentIdempotencyKey(): string {
  const randomUUID = globalThis.crypto?.randomUUID;
  if (typeof randomUUID === "function") {
    return randomUUID.call(globalThis.crypto);
  }
  const randomValues = globalThis.crypto?.getRandomValues;
  if (typeof randomValues === "function") {
    const values = new Uint32Array(4);
    randomValues.call(globalThis.crypto, values);
    return `knowledge-eval-${Array.from(values, (value) => value.toString(16)).join("")}`;
  }
  idempotencySequence += 1;
  return [
    "knowledge-eval",
    Date.now().toString(36),
    idempotencySequence.toString(36),
  ].join("-");
}

export async function postExperimentJSON<T>(
  path: string,
  body: unknown,
  idempotencyKey?: string,
): Promise<T> {
  const headers: Record<string, string> = {
    accept: "application/json",
    "content-type": "application/json",
  };
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  const controller = new AbortController();
  const timeout = globalThis.setTimeout(
    () => controller.abort(),
    EXPERIMENT_REQUEST_TIMEOUT_MS,
  );
  let response: Response;
  try {
    response = await fetch(`${EXPERIMENT_API}${path}`, {
      method: "POST",
      headers,
      body: JSON.stringify(body),
      signal: controller.signal,
    });
  } catch (error) {
    if (controller.signal.aborted) {
      throw new Error("本地实验 API 超时，请确认服务正在运行后重试。");
    }
    throw error;
  } finally {
    globalThis.clearTimeout(timeout);
  }
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const payload = await response.json() as { error?: string };
      message = payload.error || message;
    } catch {
      // The status text is the fallback for non-JSON failures.
    }
    throw new Error(message);
  }
  return response.json() as Promise<T>;
}
