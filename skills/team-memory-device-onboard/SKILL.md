---
name: team-memory-device-onboard
description: Onboard a workstation's local agent CLIs (codex, claude, pi, kimi) to an on-prem team-memory (pax-nexus) deployment using a single device enrollment token — connects paxl device credentials, provisions per-agent keys, installs hooks, and configures paxm. Use when the user hands over a device enrollment token (tm_enroll_...) and asks to configure/onboard local agents, set up paxl/paxm for agents, or connect agents to team-memory/pax-nexus.
---

# Team Memory Device Onboarding

Turn one device enrollment token into a fully configured workstation: every local
agent CLI gets its own team-memory agent credential, paxl channel, and paxm access.

## Rules

- Enrollment tokens are one-time and expire in ~15 minutes. Use immediately; if an
  exchange fails after reaching the server, the token is consumed — ask the user
  for a fresh one. Never write tokens or `tm_key_...` keys to files, logs, or chat.
- Secrets travel only via command stdout (`paxl ... --json`) and the tools' own
  credential stores.
- The backend exchange response must contain `user_id` + `permissions` (pax-nexus
  PR #16 or later). If `device connect` fails with "incomplete device credential",
  the deployment is too old — stop and tell the user to upgrade the backend.

## Workflow

1. **Preflight**: `paxl version` (>= 0.1.40, needs `paxl device`), `paxm version`
   (>= 0.2.4, needs credential discovery). Note the on-prem URL: usually
   `http://<tailscale-ip>:58080`; decode it from the token's third dot-segment
   (base64url) if unsure.
2. **Connect device**:
   `paxl device connect onprem --url <url> --device-name <hostname> --enrollment-token <token>`
   (add `--allow-tailnet-http` for plain HTTP to 100.x addresses).
   Verify: `paxl device status` shows the device and its user.
3. **Discover local agent CLIs**: `paxl agent list` if available, else check for
   `~/.codex`, `~/.claude`, `~/.pi`, `~/.kimi`. Naming convention:
   `personal-<cli>` (e.g. `personal-codex`). Ask the user before inventing other names.
4. **Provision each agent**: `paxl device provision --agent <agent-id> --json`.
   Skip agents that already have a working credential; if provisioning returns
   409, that agent is human-registered or owned by another device — keep its
   existing credential and note it.
5. **paxl per agent**: `paxl channel connect onprem --agent <agent-id>`
   (uses the device credential, no token needed; do NOT pass `--allow-tailnet-http`
   here — agent mode inherits the device's URL/trust settings).
   Install hooks: `paxl setup --agent <cli> [<cli>...]`.
   Verify: `paxl channel list`, `paxl channel status <profile>`.

   Permission caveat (paxl <= 0.1.40): `channel connect --agent` provisions with
   hardcoded `channel_send,channel_receive` only, and every provision rotates
   (revokes) the previous key for that agent. To give an agent the full set
   (observe,search,get,channel_send,channel_receive) shared by paxl and paxm:
   1. `paxl device provision --agent <id> --permission observe --permission search --permission get --permission channel_send --permission channel_receive --json`
   2. update the profile row in `~/.local/share/paxl/paxl.sqlite`
      (`channel_profiles.api_key/credential_id/permissions_json`) to the new key
   3. pre-seed `~/.config/paxm/credentials/team-<id>.json` with the same key so
      paxm discovery never re-provisions (which would rotate again).
   A paxl fix (`--permission` on channel connect) is filed; prefer it once released.
6. **paxm per agent**: the team provider discovers credentials via
   `paxl device provision` automatically — remove any explicit
   `TEAM_MEMORY_API_KEY` from `~/.config/paxm/config.yaml` to activate discovery.
   Enable passive capture only for codex/claude/pi (paxm supports no kimi hook;
   kimi uses `paxm mcp serve --agent <id>` for active recall/remember).
7. **Verify everything** (see [REFERENCE.md](REFERENCE.md) for the full checklist):
   `paxm remember` + `paxm recall` round-trip shows `provider: team` with no
   `provider_errors`; `paxl channel status` connected for every profile;
   `paxl device status` shows the expected provisioned count.

## Failure handling

- `plain HTTP ... requires explicit --allow-tailnet-http` → add the flag.
- `incomplete device credential` → backend too old (needs PR #16), stop.
- 409 on provision → agent owned by human or another device; keep existing credential.
- paxm provider_errors for non-team providers (e.g. dead openviking) → remove that
  provider from `recall_profiles`/`providers` in paxm config; do not block on it.
