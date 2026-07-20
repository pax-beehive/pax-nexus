// Package v3 defines the multi-agent GroupMemBench protocol while reusing the
// durable Eval v2 execution engine.
package v3

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode"

	v2 "github.com/pax-beehive/pax-nexus/internal/eval/v2"
)

const (
	ConfigVersion = "v3"

	ArmNoMemoryTeam          = "no_memory_team"
	ArmGroupMemBenchMem0     = "groupmembench_mem0"
	ArmPrivateSQLiteTeamNote = "private_sqlite_plus_team_note"

	ReproductionExact      = "exact_reproduction"
	ReproductionProtocol   = "protocol_reproduction"
	ReproductionComparable = "comparable_baseline"

	SourceOverlapExcluded   = "excluded"
	SourceOverlapUnknown    = "unknown"
	SourceOverlapNoEligible = "no_cross_agent_answerer_available"
)

var architectureArms = []string{
	ArmNoMemoryTeam,
	ArmGroupMemBenchMem0,
	ArmPrivateSQLiteTeamNote,
}

// AnswererSelection records the deterministic teammate assigned to a case.
type AnswererSelection struct {
	AgentID          string
	StrictCrossAgent bool
	SourceOverlap    string
}

// Validate applies the Eval v3 protocol contract on top of the shared runner
// invariants.
func Validate(config v2.Config) error {
	if config.Version != ConfigVersion {
		return fmt.Errorf("validate eval v3 config: version must be %q", ConfigVersion)
	}
	if err := config.ValidateBase(); err != nil {
		return err
	}
	if strings.TrimSpace(config.AnswererSeed) == "" {
		return fmt.Errorf("validate eval v3 config: answerer_seed is required")
	}
	if config.Mem0ReproductionLevel != ReproductionComparable {
		return fmt.Errorf("validate eval v3 config: current self-hosted runner requires mem0_reproduction_level %q", ReproductionComparable)
	}
	if config.BaselineArm != ArmNoMemoryTeam {
		return fmt.Errorf("validate eval v3 config: baseline_arm must be %q", ArmNoMemoryTeam)
	}
	if config.BeforeRun == nil {
		return fmt.Errorf("validate eval v3 config: before_run must construct full-domain memory")
	}
	if config.Judge == nil {
		return fmt.Errorf("validate eval v3 config: judge is required for comparative acceptance")
	}
	if len(config.Arms) != len(architectureArms) {
		return fmt.Errorf("validate eval v3 config: exactly three architecture arms are required")
	}
	names := make([]string, 0, len(config.Arms))
	for _, arm := range config.Arms {
		names = append(names, arm.Name)
		if arm.Producer != nil || arm.Ingest != nil || arm.AfterProducer != nil {
			return fmt.Errorf("validate eval v3 config: arm %q must reuse full-domain memory built by before_run", arm.Name)
		}
	}
	slices.Sort(names)
	want := slices.Clone(architectureArms)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		return fmt.Errorf("validate eval v3 config: arms must be %s", strings.Join(architectureArms, ", "))
	}
	return nil
}

// SelectAnswerer deterministically chooses one case participant. Annotated
// cases exclude both the Asking User and all gold-supporting authors. Cases
// without annotations exclude only the Asking User and are not strict trials.
func SelectAnswerer(evalCase v2.Case, datasetRevision, seed string) AnswererSelection {
	askingAgentID := groupMemBenchAgentID(evalCase.AskingUserID)
	supporting := make(map[string]struct{}, len(evalCase.SupportingAgentIDs))
	for _, agentID := range evalCase.SupportingAgentIDs {
		supporting[agentID] = struct{}{}
	}
	eligible := make([]string, 0, len(evalCase.ParticipantAgentIDs))
	seen := make(map[string]struct{}, len(evalCase.ParticipantAgentIDs))
	for _, agentID := range evalCase.ParticipantAgentIDs {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" || agentID == askingAgentID {
			continue
		}
		if len(supporting) > 0 {
			if _, isSource := supporting[agentID]; isSource {
				continue
			}
		}
		if _, duplicate := seen[agentID]; duplicate {
			continue
		}
		seen[agentID] = struct{}{}
		eligible = append(eligible, agentID)
	}
	if len(eligible) == 0 {
		return AnswererSelection{SourceOverlap: SourceOverlapNoEligible}
	}
	slices.SortFunc(eligible, func(left, right string) int {
		return strings.Compare(answererKey(datasetRevision, seed, evalCase.ID, left), answererKey(datasetRevision, seed, evalCase.ID, right))
	})
	selection := AnswererSelection{AgentID: eligible[0], SourceOverlap: SourceOverlapUnknown}
	if len(supporting) > 0 {
		selection.StrictCrossAgent = true
		selection.SourceOverlap = SourceOverlapExcluded
	}
	return selection
}

// AssignAnswerers binds one paired Answering Agent to every case before the
// shared runner expands the case across arms.
func AssignAnswerers(cases []v2.Case, datasetRevision, seed string) ([]v2.Case, error) {
	assigned := slices.Clone(cases)
	for index := range assigned {
		selection := SelectAnswerer(assigned[index], datasetRevision, seed)
		assigned[index].AnsweringAgentID = selection.AgentID
		assigned[index].AnswererSeed = seed
		assigned[index].StrictCrossAgent = selection.StrictCrossAgent
		assigned[index].AnswererSourceOverlap = selection.SourceOverlap
	}
	return assigned, nil
}

func groupMemBenchAgentID(userID string) string {
	value := strings.Map(func(current rune) rune {
		if unicode.IsLetter(current) || unicode.IsNumber(current) || current == '-' || current == '_' {
			return current
		}
		return '-'
	}, strings.TrimSpace(userID))
	return "groupmembench-" + value
}

func answererKey(datasetRevision, seed, caseID, agentID string) string {
	digest := sha256.Sum256([]byte(datasetRevision + "\x00" + seed + "\x00" + caseID + "\x00" + agentID))
	return hex.EncodeToString(digest[:])
}
