package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/demo"
)

func main() {
	output := flag.String("output", "tmp/knowledge-eval-demo", "acceptance bundle output directory")
	flag.Parse()
	bundle, err := demo.Generate(context.Background(), *output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate knowledge eval demo: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf(
		"generated %d artifacts and %d runs at %s\n",
		len(bundle.Artifacts),
		len(bundle.Query.Runs),
		*output,
	)
}
