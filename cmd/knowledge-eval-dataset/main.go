package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/demo"
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
	flag.Parse()
	bundle, err := demo.GenerateSessionDataset(context.Background(), demo.SessionDatasetConfig{
		Dataset: *dataset, Partition: *partition, CaseID: *caseID,
		IngestPath: *ingest, QueryPath: *queries, GoldPath: *gold,
		QuestionLimit: *limit, OutputDirectory: *output,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "run knowledge dataset eval: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf(
		"generated %s/%s with %d sessions, %d turns, and %d questions\n",
		bundle.Dataset,
		bundle.CaseID,
		bundle.Ingest.Sessions,
		bundle.Ingest.Turns,
		bundle.Questions,
	)
}
