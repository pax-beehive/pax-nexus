// Command groupmembench-sessions 把 GroupMemBench 域数据加工成 per-user
// agent 工作 session 数据集。见 docs/superpowers/specs/
// 2026-08-01-groupmembench-agent-sessions-design.md。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench/agentsessions"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

func main() {
	domainFile := flag.String("domain-file", "", "GroupMemBench domain JSON")
	questionsDir := flag.String("questions-dir", "", "questions directory")
	outDir := flag.String("out-dir", "", "output directory")
	cacheDir := flag.String("cache-dir", "", "LLM response cache directory")
	model := flag.String("model", "deepseek-v4-flash", "LLM model")
	concurrency := flag.Int("concurrency", 28, "LLM concurrency")
	apiKey := flag.String("api-key", os.Getenv("DEEPSEEK_API_KEY"), "DeepSeek API key")
	flag.Parse()
	if *domainFile == "" || *questionsDir == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "usage: -domain-file, -questions-dir, -out-dir are required")
		os.Exit(2)
	}
	if *apiKey == "" {
		fmt.Fprintln(os.Stderr, "missing DeepSeek API key (-api-key or DEEPSEEK_API_KEY)")
		os.Exit(2)
	}
	client := &agentsessions.RetryingChatClient{
		Inner: llm.NewDeepSeekClient(llm.DeepSeekConfig{APIKey: *apiKey}),
	}
	report, err := agentsessions.RunPipeline(context.Background(),
		agentsessions.PipelineConfig{
			DomainFile: *domainFile, QuestionsDir: *questionsDir,
			OutDir: *outDir, CacheDir: *cacheDir, Model: *model,
			Concurrency: *concurrency, MaxObs: 60, TopK: 5, Client: client,
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "pipeline failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sessions=%d users=%d calls=%d cache_hits=%d fallbacks=%d "+
		"violations=%d exceptions=%d\n",
		report.Sessions, report.Users, report.Fill.Calls, report.Fill.CacheHits,
		report.Fill.Fallbacks, report.Violations, len(report.Exceptions))
	if report.Fill.Calls > 0 &&
		float64(report.Fill.Fallbacks)/float64(report.Fill.Calls) > 0.02 {
		fmt.Fprintln(os.Stderr, "WARNING: fallback ratio above 2% red line")
	}
}
