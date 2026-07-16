import { tool } from "@opencode-ai/plugin"

const hardMaximum = 2
const configuredMaximum = Number.parseInt(process.env.PAXM_ACTIVE_RECALL_MAX_CALLS ?? "2", 10)
const maximumCalls = Number.isInteger(configuredMaximum)
  ? Math.min(Math.max(configuredMaximum, 1), hardMaximum)
  : hardMaximum
let calls = 0

export default tool({
  description: `Recall additional memory with a focused query. May be called at most ${maximumCalls} times.`,
  args: {
    query: tool.schema.string().min(1).describe("Focused memory search query"),
  },
  async execute(args, context) {
    if (calls >= maximumCalls) {
      return `Active recall limit reached: ${maximumCalls} calls were already used.`
    }
    calls += 1

    const binary = process.env.PAXM_BINARY ?? "/usr/local/bin/paxm"
    const config = process.env.PAXM_CONFIG
    if (!config) {
      throw new Error("PAXM_CONFIG is required for active recall")
    }
    const request = {
      jsonrpc: "2.0",
      id: calls,
      method: "tools/call",
      params: {
        name: "paxm_recall",
        arguments: {
          query: args.query,
          limit: 5,
          meta: { session_id: context.sessionID },
        },
      },
    }
    const processHandle = Bun.spawn(
      [binary, "--config", config, "mcp", "serve", "--agent", "opencode"],
      { stdin: "pipe", stdout: "pipe", stderr: "pipe", env: process.env },
    )
    processHandle.stdin.write(`${JSON.stringify(request)}\n`)
    processHandle.stdin.end()
    const [stdout, stderr, exitCode] = await Promise.all([
      new Response(processHandle.stdout).text(),
      new Response(processHandle.stderr).text(),
      processHandle.exited,
    ])
    if (exitCode !== 0) {
      throw new Error(`active recall failed: ${stderr.trim() || `exit ${exitCode}`}`)
    }
    const line = stdout.trim().split("\n").at(-1)
    if (!line) {
      throw new Error("active recall returned no response")
    }
    const response = JSON.parse(line)
    if (response.error) {
      throw new Error(`active recall failed: ${response.error.message}`)
    }
    const content = response.result?.content
    if (!Array.isArray(content)) {
      throw new Error("active recall returned an invalid response")
    }
    return content
      .filter((item) => item?.type === "text")
      .map((item) => item.text)
      .join("\n")
  },
})
