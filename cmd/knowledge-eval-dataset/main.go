package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	llmwikidriver "github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/artifact/llmwiki"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/benchmark/qa"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/demo"
	"github.com/pax-beehive/pax-nexus/internal/llmwiki/workspace"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

func main() {
	dataset := flag.String("dataset", "", "dataset name")
	partition := flag.String("partition", "", "dataset partition")
	caseID := flag.String("case", "", "dataset case ID")
	ingest := flag.String("ingest", "", "maintainer ingest JSONL")
	queries := flag.String("queries", "", "reader query JSONL")
	gold := flag.String("gold", "", "evaluator gold JSONL")
	limit := flag.Int("limit", 5, "maximum questions")
	output := flag.String("output", "tmp/knowledge-eval-dataset", "output directory")
	runMaintainer := flag.Bool("maintainer", false, "run the real LLM Wiki maintainer arm")
	model := flag.String("model", "deepseek-v4-pro", "maintainer model")
	readerModel := flag.String("reader-model", "deepseek-v4-pro", "fixed QA reader model")
	baseURL := flag.String("base-url", "https://api.deepseek.com", "DeepSeek API base URL")
	maxRounds := flag.Int("max-rounds", 30, "maximum maintainer model turns")
	instruction := flag.String(
		"instruction",
		workspace.DefaultMaintenanceInstruction,
		"maintainer instruction",
	)
	codeRevision := flag.String("code-revision", "working-tree", "builder code revision")
	resumeFailure := flag.String(
		"resume-failure",
		"",
		"content digest of a preserved maintainer failure workspace",
	)
	flag.Parse()
	config := demo.SessionDatasetConfig{
		Dataset: *dataset, Partition: *partition, CaseID: *caseID,
		IngestPath: *ingest, QueryPath: *queries, GoldPath: *gold,
		QuestionLimit: *limit, OutputDirectory: *output, CodeRevision: *codeRevision,
	}
	if *runMaintainer || *resumeFailure != "" {
		client := llm.NewDeepSeekClient(llm.DeepSeekConfig{
			BaseURL: *baseURL, APIKey: os.Getenv("DEEPSEEK_API_KEY"),
		})
		reader, err := qa.NewChatReader(client, *readerModel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "configure knowledge dataset reader: %v\n", err)
			os.Exit(1)
		}
		maintainer := &llmwikidriver.AgentBuilderConfig{
			Model: *model, MaxRounds: *maxRounds,
			Instruction: *instruction, Client: client,
		}
		if *resumeFailure != "" {
			root, err := filepath.Abs(filepath.Join(
				*output,
				"artifacts",
				*resumeFailure,
				"tree",
			))
			if err != nil {
				fmt.Fprintf(os.Stderr, "resolve failed maintainer workspace: %v\n", err)
				os.Exit(1)
			}
			maintainer.Resume = &knowledgeeval.OpaqueRef{
				Kind:          "llmwiki-maintainer-failure",
				SchemaVersion: "pax.knowledge-eval.llmwiki-maintainer-failure.v1",
				URI:           (&url.URL{Scheme: "file", Path: root}).String(),
				SHA256:        *resumeFailure,
			}
		}
		config.Maintainer = maintainer
		config.Reader = reader
	}
	bundle, err := demo.GenerateSessionDataset(context.Background(), config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run knowledge dataset eval: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf(
		"generated %s/%s mode=%s status=%s with %d sessions, %d turns, and %d questions\n",
		bundle.Dataset,
		bundle.CaseID,
		bundle.Mode,
		bundle.BuildStatus,
		bundle.Ingest.Sessions,
		bundle.Ingest.Turns,
		bundle.Questions,
	)
}
