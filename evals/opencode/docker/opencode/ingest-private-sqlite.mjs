import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { spawnSync } from "node:child_process";

const [batchesPath, privateRoot] = process.argv.slice(2);
if (!batchesPath || !privateRoot) {
  throw new Error("usage: ingest-private-sqlite.mjs <session-batches.json> <private-root>");
}

const batches = JSON.parse(readFileSync(batchesPath, "utf8"));
if (!Array.isArray(batches) || batches.length === 0) {
  throw new Error("private SQLite ingest requires session batches");
}

mkdirSync(privateRoot, { recursive: true });
const markerPath = join(privateRoot, "ingest-complete.json");
try {
  const existing = JSON.parse(readFileSync(markerPath, "utf8"));
  process.stdout.write(`${JSON.stringify({ ...existing, accepted: 0, duplicate: existing.source_events, noop_known: true, noop: true })}\n`);
  process.exit(0);
} catch (error) {
  if (error?.code !== "ENOENT") throw error;
}

const byAgent = new Map();
let sourceEvents = 0;
for (const [batchIndex, batch] of batches.entries()) {
  if (!batch?.complete || !Array.isArray(batch.events) || batch.events.length === 0) {
    throw new Error(`batch ${batchIndex} must be complete and non-empty`);
  }
  for (const event of batch.events) {
    const agentID = event?.actor?.agent_id;
    if (!agentID || !event?.actor?.user_id || !event?.id || !event?.content) {
      throw new Error(`batch ${batchIndex} contains an event with missing provenance`);
    }
    const events = byAgent.get(agentID) ?? [];
    events.push(event);
    byAgent.set(agentID, events);
    sourceEvents++;
  }
}

const configRoot = join(privateRoot, ".configs");
mkdirSync(configRoot, { recursive: true });
try {
  for (const [agentID, events] of [...byAgent.entries()].sort(([left], [right]) => left.localeCompare(right))) {
    const safeAgentID = agentID.replace(/[^A-Za-z0-9_.-]/g, "_");
    const databasePath = join(privateRoot, `${safeAgentID}.sqlite`);
    const configPath = join(configRoot, `${safeAgentID}.yaml`);
    const userID = events[0].actor.user_id;
    writeFileSync(configPath, `version: 1
identity:
  user_id: "${yamlString(userID)}"
providers:
  private:
    type: sqlite
    enabled: true
    path: "${yamlString(databasePath)}"
recall_profiles:
  default:
    providers:
      - name: private
        required: true
    max_results: 5
  passive:
    providers:
      - name: private
        required: true
    max_results: 5
write_profiles:
  ltm:
    tier: ltm
    providers:
      - name: private
        required: true
`);
    for (const event of events.sort(compareEvents)) {
      const text = renderEvent(event);
      const result = spawnSync(process.env.PAXM_BINARY ?? "/usr/local/bin/paxm", [
        "--config", configPath, "remember", "--profile", "ltm", "--source", `groupmembench:${event.id}`, "--text", text,
      ], { encoding: "utf8" });
      if (result.status !== 0) {
        throw new Error(`paxm remember failed for ${agentID}/${event.id}: ${result.stderr || result.stdout}`);
      }
    }
  }
} finally {
  rmSync(configRoot, { recursive: true, force: true });
}

const receipt = {
  provider: "private_sqlite",
  accepted: sourceEvents,
  duplicate: 0,
  created: sourceEvents,
  updated: 0,
  deleted: 0,
  noop_known: true,
  noop: false,
  source_events: sourceEvents,
  source_actors: byAgent.size,
  source_sessions: batches.length,
};
writeFileSync(markerPath, `${JSON.stringify(receipt, null, 2)}\n`);
process.stdout.write(`${JSON.stringify(receipt)}\n`);

function compareEvents(left, right) {
  const time = String(left.occurred_at).localeCompare(String(right.occurred_at));
  return time || String(left.id).localeCompare(String(right.id));
}

function renderEvent(event) {
  const metadata = event.metadata ?? {};
  return [
    `Message ${event.id}`,
    `User: ${event.actor.user_id}`,
    `Agent: ${event.actor.agent_id}`,
    metadata.role ? `Role: ${metadata.role}` : "",
    event.occurred_at ? `Timestamp: ${event.occurred_at}` : "",
    metadata.channel ? `Channel: ${metadata.channel}` : "",
    "",
    event.content,
  ].filter((line) => line !== "").join("\n");
}

function yamlString(value) {
  return String(value).replaceAll("\\", "\\\\").replaceAll('"', '\\"');
}
