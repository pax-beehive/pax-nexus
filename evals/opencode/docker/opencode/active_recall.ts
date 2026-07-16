import { tool } from "@opencode-ai/plugin"

const configuredMaximum = Number.parseInt(process.env.PAXM_ACTIVE_RECALL_MAX_CALLS ?? "2", 10)
const maximumCalls = Number.isInteger(configuredMaximum)
  ? Math.min(Math.max(configuredMaximum, 1), 2)
  : 2

export default tool({
  description: `Recall additional memory with a focused query. May be called at most ${maximumCalls} times.`,
  args: {
    query: tool.schema.string().min(1).describe("Focused memory search query"),
  },
  async execute(args, context) {
    const binary = process.env.PAXM_ACTIVE_RECALL_BINARY ?? "/usr/local/bin/eval-v2-active-recall"
    const processHandle = Bun.spawn(
      [
        binary,
        "-query",
        args.query,
        "-session-id",
        context.sessionID,
        "-max-calls",
        maximumCalls.toString(),
      ],
      { stdout: "pipe", stderr: "pipe", env: process.env },
    )
    const [stdout, stderr, exitCode] = await Promise.all([
      new Response(processHandle.stdout).text(),
      new Response(processHandle.stderr).text(),
      processHandle.exited,
    ])
    if (exitCode !== 0) {
      throw new Error(`active recall failed: ${stderr.trim() || `exit ${exitCode}`}`)
    }
    const result = stdout.trim()
    if (!result) {
      throw new Error("active recall returned no response")
    }
    return result
  },
})
