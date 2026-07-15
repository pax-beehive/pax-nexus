package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/pax-beehive/pax-nexus/internal/platform/observability"
)

func main() {
	logger := observability.NewLogger(os.Stderr)
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("GroupMemBench selection failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("groupmembench-select", flag.ContinueOnError)
	conversationPath := flags.String("conversation", "", "GroupMemBench conversation JSON")
	questionsDirectory := flags.String("questions", "", "GroupMemBench question directory")
	outputDirectory := flags.String("output", "", "case output directory")
	domain := flags.String("domain", "Finance", "GroupMemBench domain")
	revision := flags.String("revision", "", "GroupMemBench dataset revision")
	seed := flags.String("seed", "team-memory-v1", "deterministic selection seed")
	perCategory := flags.Int("per-category", 2, "questions selected per category")
	totalCases := flags.Int("total-cases", 0, "exact balanced question count; overrides per-category when positive")
	topK := flags.Int("top-k", 8, "BM25 messages selected per question")
	neighborRadius := flags.Int("neighbor-radius", 1, "adjacent messages included around BM25 hits")
	maxContextMessages := flags.Int("max-context-messages", 32, "maximum source messages per case")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse GroupMemBench selection flags: %w", err)
	}
	messages, err := groupmembench.LoadConversation(*conversationPath)
	if err != nil {
		return err
	}
	questions, err := groupmembench.LoadQuestions(*questionsDirectory)
	if err != nil {
		return err
	}
	cases, err := groupmembench.Select(questions, messages, groupmembench.Config{
		PerCategory: *perCategory, TotalCases: *totalCases, TopK: *topK, NeighborRadius: *neighborRadius,
		MaxContextMessages: *maxContextMessages, Seed: *seed,
	})
	if err != nil {
		return err
	}
	if err := groupmembench.WriteCases(*outputDirectory, *revision, *domain, *seed, cases); err != nil {
		return err
	}
	logger.Info("GroupMemBench cases selected", "cases", len(cases), "domain", *domain, "output", *outputDirectory)
	return nil
}
