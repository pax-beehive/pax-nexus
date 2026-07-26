package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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
	mode := flags.String("mode", "case-context", "selection mode: case-context or full-domain")
	perCategory := flags.Int("per-category", 2, "questions selected per category")
	totalCases := flags.Int("total-cases", 0, "exact balanced question count; overrides per-category when positive")
	topK := flags.Int("top-k", 8, "BM25 messages selected per question")
	neighborRadius := flags.Int("neighbor-radius", 1, "adjacent messages included around BM25 hits")
	maxContextMessages := flags.Int("max-context-messages", 32, "maximum source messages per case")
	annotationsPath := flags.String("annotations", "", "optional GroupMemBench annotations.json (from groupmembench-annotate); high-confidence entries are merged into the manifest as supporting_agent_ids")
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
	config := groupmembench.Config{
		PerCategory: *perCategory, TotalCases: *totalCases, TopK: *topK, NeighborRadius: *neighborRadius,
		MaxContextMessages: *maxContextMessages, Seed: *seed,
	}
	var cases []groupmembench.Case
	switch *mode {
	case "case-context":
		cases, err = groupmembench.Select(questions, messages, config)
		if err == nil {
			err = groupmembench.WriteCases(*outputDirectory, *revision, *domain, *seed, cases)
		}
	case "full-domain":
		cases, err = groupmembench.SelectQuestions(questions, config)
		if err == nil {
			err = groupmembench.WriteV3Cases(*outputDirectory, *revision, *domain, *seed, cases, messages)
		}
	default:
		return fmt.Errorf("unsupported GroupMemBench selection mode %q", *mode)
	}
	if err != nil {
		return err
	}
	logger.Info("GroupMemBench cases selected", "cases", len(cases), "domain", *domain, "mode", *mode, "output", *outputDirectory)

	if strings.TrimSpace(*annotationsPath) == "" {
		return nil
	}
	annotated, withheld, unmatched, err := mergeAnnotations(*outputDirectory, *annotationsPath)
	if err != nil {
		return err
	}
	logger.Info("GroupMemBench annotations merged into manifest",
		"annotated", annotated, "withheld_low_confidence", withheld, "unmatched", unmatched)
	return nil
}

// mergeAnnotations folds high-confidence supporting-author annotations into
// the manifest.json (and manifest.smoke.json, if present) that the selection
// above just wrote. It never touches either file unless annotationsPath is
// non-empty, so runs without -annotations produce byte-identical manifests
// to a build that never had this flag.
//
// Low-confidence annotations are counted but withheld: an unreviewed guess
// must never silently become a strict cross-agent trial. Annotations for a
// case ID absent from this selection are counted as unmatched and otherwise
// ignored, so a human can reconcile a stale annotations.json against a
// re-selected manifest.
func mergeAnnotations(outputDirectory, annotationsPath string) (annotated, withheld, unmatched int, err error) {
	annotations, err := loadAnnotations(annotationsPath)
	if err != nil {
		return 0, 0, 0, err
	}
	annotated, withheld, unmatched, err = mergeAnnotationsIntoManifestFile(filepath.Join(outputDirectory, "manifest.json"), annotations)
	if err != nil {
		return 0, 0, 0, err
	}
	smokePath := filepath.Join(outputDirectory, "manifest.smoke.json")
	if _, statErr := os.Stat(smokePath); statErr == nil {
		if _, _, _, err := mergeAnnotationsIntoManifestFile(smokePath, annotations); err != nil {
			return 0, 0, 0, err
		}
	}
	return annotated, withheld, unmatched, nil
}

func loadAnnotations(path string) ([]groupmembench.Annotation, error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open GroupMemBench annotations %q: %w", path, err)
	}
	var annotations []groupmembench.Annotation
	if err := json.Unmarshal(input, &annotations); err != nil {
		return nil, fmt.Errorf("decode GroupMemBench annotations %q: %w", path, err)
	}
	return annotations, nil
}

func mergeAnnotationsIntoManifestFile(path string, annotations []groupmembench.Annotation) (annotated, withheld, unmatched int, err error) {
	input, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("open GroupMemBench manifest %q: %w", path, err)
	}
	var manifest groupmembench.Manifest
	if err := json.Unmarshal(input, &manifest); err != nil {
		return 0, 0, 0, fmt.Errorf("decode GroupMemBench manifest %q: %w", path, err)
	}

	byCaseID := make(map[string]groupmembench.Annotation, len(annotations))
	for _, annotation := range annotations {
		byCaseID[annotation.CaseID] = annotation
	}
	matched := make(map[string]struct{}, len(annotations))
	for index, evalCase := range manifest.Cases {
		annotation, ok := byCaseID[evalCase.ID]
		if !ok {
			continue
		}
		matched[annotation.CaseID] = struct{}{}
		if annotation.Confidence == groupmembench.ConfidenceLow {
			withheld++
			continue
		}
		if len(annotation.SupportingAgentIDs) == 0 {
			// High confidence with no resolvable supporting author (e.g. only
			// the asking user or a non-participant authored the grounding
			// events): nothing to merge, and not a low-confidence guess either.
			continue
		}
		manifest.Cases[index].SupportingAgentIDs = annotation.SupportingAgentIDs
		annotated++
	}
	for _, annotation := range annotations {
		if _, ok := matched[annotation.CaseID]; !ok {
			unmatched++
		}
	}

	output, err := os.Create(path)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("create GroupMemBench manifest %q: %w", path, err)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(manifest)
	closeErr := output.Close()
	if encodeErr != nil {
		return 0, 0, 0, fmt.Errorf("encode GroupMemBench manifest %q: %w", path, encodeErr)
	}
	if closeErr != nil {
		return 0, 0, 0, fmt.Errorf("close GroupMemBench manifest %q: %w", path, closeErr)
	}
	return annotated, withheld, unmatched, nil
}
