import { pathToFileURL } from "node:url";

function publishedPorts(service, target) {
  return (service?.ports ?? []).filter((port) => Number(port.target) === target);
}

function hasCanonicalGatewayPort(service, target) {
  return publishedPorts(service, target).some((port) =>
    Number(port.published) === target
      && (!port.host_ip || port.host_ip === "0.0.0.0" || port.host_ip === "::"),
  );
}

function isStableDNSHostname(value) {
  if (typeof value !== "string" || value.length === 0 || value !== value.trim()) return false;
  if (value.toLowerCase() === "localhost" || value.includes(":") || value.includes("/")) return false;
  if (/^\d{1,3}(?:\.\d{1,3}){3}$/.test(value)) return false;

  return value.length <= 253 && value.split(".").every((label) =>
    /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(label),
  );
}

export function validateWorkstationCompose(config) {
  const backendPorts = publishedPorts(config?.services?.["team-memory"], 8080);
  if (backendPorts.length === 0 || backendPorts.some((port) => port.host_ip !== "127.0.0.1")) {
    throw new Error("team-memory port 8080 must be published on 127.0.0.1 only");
  }

  const postgresPorts = publishedPorts(config?.services?.postgres, 5432);
  if (postgresPorts.length === 0 || postgresPorts.some((port) => port.host_ip !== "127.0.0.1")) {
    throw new Error("postgres port 5432 must be published on 127.0.0.1 only");
  }

  const portalHost = config?.services?.caddy?.environment?.TEAM_MEMORY_PORTAL_HOST;
  if (!isStableDNSHostname(portalHost)) {
    throw new Error("TEAM_MEMORY_PORTAL_HOST must be a stable DNS hostname, not an IP address or URL");
  }

  const caddy = config?.services?.caddy;
  if (publishedPorts(caddy, 443).length === 0) {
    throw new Error("caddy must publish HTTPS port 443");
  }
  if (!hasCanonicalGatewayPort(caddy, 443)) {
    throw new Error("caddy must be externally reachable on host port 443");
  }
  if (publishedPorts(caddy, 80).length === 0) {
    throw new Error("caddy must publish HTTP port 80 for canonical HTTPS redirects");
  }
  if (!hasCanonicalGatewayPort(caddy, 80)) {
    throw new Error("caddy must be externally reachable on host port 80 for canonical HTTPS redirects");
  }

  const backendEnvironment = config?.services?.["team-memory"]?.environment ?? {};
  const expectedPortalURL = `https://${portalHost}/`;
  if (backendEnvironment.TEAM_MEMORY_PORTAL_URL !== expectedPortalURL) {
    throw new Error(`TEAM_MEMORY_PORTAL_URL must be exactly ${expectedPortalURL}`);
  }

  const expectedOIDCCallback = `${expectedPortalURL}v1/auth/callback`;
  if (backendEnvironment.TEAM_MEMORY_OIDC_REDIRECT_URL !== expectedOIDCCallback) {
    throw new Error(`TEAM_MEMORY_OIDC_REDIRECT_URL must be exactly ${expectedOIDCCallback}`);
  }

  if (backendEnvironment.TEAM_MEMORY_HUMAN_COOKIE_SECURE !== "true") {
    throw new Error("TEAM_MEMORY_HUMAN_COOKIE_SECURE must be true for workstation deployment");
  }
}

async function run() {
  try {
    let input = "";
    for await (const chunk of process.stdin) input += chunk;
    validateWorkstationCompose(JSON.parse(input));
    process.stdout.write("workstation compose validation passed\n");
  } catch (error) {
    const message = error instanceof Error ? error.message : "unknown validation error";
    process.stderr.write(`workstation compose validation failed: ${message}\n`);
    process.exitCode = 1;
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await run();
}
