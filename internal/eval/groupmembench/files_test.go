package groupmembench_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/stretchr/testify/suite"
)

type filesSuite struct{ suite.Suite }

func TestFilesSuite(t *testing.T) { suite.Run(t, new(filesSuite)) }

func (s *filesSuite) TestWriteCasesPublishesAcceptanceAndThreeScenarioSmokeManifests() {
	directory := s.T().TempDir()
	cases := make([]groupmembench.Case, 0)
	for _, category := range groupmembench.Categories() {
		cases = append(cases, groupmembench.Case{
			Category: category,
			Question: groupmembench.Question{ID: category + "_1", Question: "question", Answer: "answer", AskingUserID: "user"},
			Messages: []groupmembench.Message{{NodeID: "message", Content: "context"}},
		})
	}

	s.Require().NoError(groupmembench.WriteCases(directory, "revision", "Finance", "seed", cases))
	acceptance := loadManifest(s.T(), filepath.Join(directory, "manifest.json"))
	smoke := loadManifest(s.T(), filepath.Join(directory, "manifest.smoke.json"))
	s.Len(acceptance.Cases, 6)
	s.Require().Len(smoke.Cases, 3)
	s.Equal([]string{"multi_hop", "knowledge_update", "abstention"}, []string{
		smoke.Cases[0].Category, smoke.Cases[1].Category, smoke.Cases[2].Category,
	})
}

func loadManifest(t *testing.T, path string) groupmembench.Manifest {
	t.Helper()
	input, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest groupmembench.Manifest
	if err := json.Unmarshal(input, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}
