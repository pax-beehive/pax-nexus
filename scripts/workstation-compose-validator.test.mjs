import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { validateWorkstationCompose } from "./workstation-compose-validator.mjs";

const validatorPath = fileURLToPath(new URL("./workstation-compose-validator.mjs", import.meta.url));

function validConfig() {
  return {
    services: {
      postgres: {
        ports: [{ target: 5432, published: "55432", host_ip: "127.0.0.1" }],
      },
      "team-memory": {
        environment: {
          TEAM_MEMORY_HUMAN_COOKIE_SECURE: "true",
          TEAM_MEMORY_OIDC_REDIRECT_URL: "https://memory.example.internal/v1/auth/callback",
          TEAM_MEMORY_PORTAL_URL: "https://memory.example.internal/",
        },
        ports: [{ target: 8080, published: "58080", host_ip: "127.0.0.1" }],
      },
      caddy: {
        environment: {
          TEAM_MEMORY_PORTAL_HOST: "memory.example.internal",
        },
        ports: [
          { target: 80, published: "80" },
          { target: 443, published: "443" },
        ],
      },
    },
  };
}

test("accepts a canonical HTTPS workstation deployment", () => {
  assert.doesNotThrow(() => validateWorkstationCompose(validConfig()));
});

test("rejects a network-exposed backend listener", () => {
  const config = validConfig();
  config.services["team-memory"].ports[0].host_ip = "0.0.0.0";

  assert.throws(
    () => validateWorkstationCompose(config),
    /team-memory.*127\.0\.0\.1/,
  );
});

test("rejects a network-exposed PostgreSQL listener", () => {
  const config = validConfig();
  config.services.postgres.ports[0].host_ip = "0.0.0.0";

  assert.throws(
    () => validateWorkstationCompose(config),
    /postgres.*127\.0\.0\.1/,
  );
});

test("rejects an IP address as the persistent Portal host", () => {
  const config = validConfig();
  config.services.caddy.environment.TEAM_MEMORY_PORTAL_HOST = "100.125.72.76";

  assert.throws(
    () => validateWorkstationCompose(config),
    /stable DNS hostname/,
  );
});

test("requires the Portal URL to match the canonical HTTPS host", () => {
  const config = validConfig();
  config.services["team-memory"].environment.TEAM_MEMORY_PORTAL_URL =
    "http://memory.example.internal/";

  assert.throws(
    () => validateWorkstationCompose(config),
    /TEAM_MEMORY_PORTAL_URL.*https:\/\/memory\.example\.internal\//,
  );
});

test("requires the OIDC callback to use the canonical HTTPS host", () => {
  const config = validConfig();
  config.services["team-memory"].environment.TEAM_MEMORY_OIDC_REDIRECT_URL =
    "https://other.example.internal/v1/auth/callback";

  assert.throws(
    () => validateWorkstationCompose(config),
    /TEAM_MEMORY_OIDC_REDIRECT_URL.*memory\.example\.internal/,
  );
});

test("requires secure Human Session cookies", () => {
  const config = validConfig();
  config.services["team-memory"].environment.TEAM_MEMORY_HUMAN_COOKIE_SECURE = "false";

  assert.throws(
    () => validateWorkstationCompose(config),
    /TEAM_MEMORY_HUMAN_COOKIE_SECURE.*true/,
  );
});

test("requires the TLS gateway to publish HTTPS", () => {
  const config = validConfig();
  config.services.caddy.ports = [{ target: 80, published: "80" }];

  assert.throws(
    () => validateWorkstationCompose(config),
    /caddy.*443/,
  );
});

test("requires the gateway HTTP listener for canonical HTTPS redirects", () => {
  const config = validConfig();
  config.services.caddy.ports = [{ target: 443, published: "443" }];

  assert.throws(
    () => validateWorkstationCompose(config),
    /caddy.*80.*redirect/,
  );
});

test("command-line validation fails without printing the rendered configuration", () => {
  const config = validConfig();
  config.services["team-memory"].ports[0].host_ip = "0.0.0.0";

  const result = spawnSync(process.execPath, [validatorPath], {
    encoding: "utf8",
    input: JSON.stringify(config),
  });

  assert.equal(result.status, 1);
  assert.match(result.stderr, /team-memory.*127\.0\.0\.1/);
  assert.doesNotMatch(result.stderr, /TEAM_MEMORY_OIDC_REDIRECT_URL/);
  assert.equal(result.stdout, "");
});
