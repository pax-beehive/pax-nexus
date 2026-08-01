package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type preparedManifest struct {
	Dataset  string `json:"dataset"`
	Revision string `json:"revision"`
	License  string `json:"license"`
}

type preparedRecord struct {
	CaseID        string            `json:"case_id"`
	SourceKind    string            `json:"source_kind"`
	Sessions      []preparedSession `json:"sessions"`
	TrajectoryIDs []string          `json:"trajectory_ids"`
}

type preparedSession struct {
	Turns []json.RawMessage `json:"turns"`
}

type preparedQuery struct {
	CaseID       string `json:"case_id"`
	SourceCaseID string `json:"source_case_id"`
}

func loadPreparedCatalog(root string, catalog *Catalog) error {
	manifestRoot := filepath.Join(root, "manifests")
	entries, err := os.ReadDir(manifestRoot)
	if err != nil {
		return fmt.Errorf("list prepared dataset manifests: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		slug := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		manifest, loadErr := loadPreparedManifest(filepath.Join(manifestRoot, entry.Name()))
		if loadErr != nil {
			return loadErr
		}
		if strings.TrimSpace(manifest.Dataset) == "" {
			continue
		}
		catalog.Families = append(catalog.Families, DatasetFamily{
			ID: slug, Name: manifest.Dataset, Revision: manifest.Revision,
			License: manifest.License,
		})
		partitions, partitionErr := preparedPartitions(root, slug)
		if partitionErr != nil {
			return partitionErr
		}
		for _, partition := range partitions {
			if err := loadPreparedPartition(root, slug, partition, catalog); err != nil {
				return err
			}
		}
	}
	return nil
}

func loadPreparedManifest(path string) (preparedManifest, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return preparedManifest{}, fmt.Errorf("read prepared dataset manifest %s: %w", path, err)
	}
	var manifest preparedManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return preparedManifest{}, fmt.Errorf("decode prepared dataset manifest %s: %w", path, err)
	}
	return manifest, nil
}

func preparedPartitions(root, slug string) ([]string, error) {
	var result []string
	for _, partition := range []string{"train", "holdout"} {
		path := filepath.Join(root, partition, slug, "maintainer", "ingest.jsonl")
		if _, err := os.Stat(path); err == nil {
			result = append(result, partition)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect prepared partition %s: %w", path, err)
		}
	}
	if len(result) != 0 {
		return result, nil
	}
	path := filepath.Join(root, "full", slug, "maintainer", "ingest.jsonl")
	if _, err := os.Stat(path); err == nil {
		return []string{"full"}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect prepared partition %s: %w", path, err)
	}
	return nil, nil
}

func loadPreparedPartition(root, slug, partition string, catalog *Catalog) error {
	base := filepath.Join(root, partition, slug)
	questionCounts, err := loadPreparedQuestionCounts(
		filepath.Join(base, "reader", "query.jsonl"),
	)
	if err != nil {
		return err
	}
	records, err := loadPreparedRecords(
		filepath.Join(base, "maintainer", "ingest.jsonl"),
	)
	if err != nil {
		return err
	}
	groupIndexes := make(map[string]int)
	for _, record := range records {
		if strings.TrimSpace(record.CaseID) == "" {
			return fmt.Errorf(
				"decode prepared dataset %s/%s: case_id is required",
				slug,
				partition,
			)
		}
		groupID := record.CaseID
		if len(record.TrajectoryIDs) > 0 {
			groupID = trajectoryEnvironmentID(record.TrajectoryIDs)
		}
		catalogID := datasetID(slug, partition, groupID)
		index, exists := groupIndexes[catalogID]
		if !exists {
			groupIndexes[catalogID] = len(catalog.Datasets)
			sessions, turns := preparedRecordSize(record)
			evaluationCases := questionCounts[record.CaseID]
			catalog.Datasets = append(catalog.Datasets, Dataset{
				ID: catalogID, Name: slug, Partition: partition,
				CaseID: groupID, GroupKind: inferGroupKind(slug, record.SourceKind),
				SourceKind: record.SourceKind, Status: "not_run",
				Sessions: sessions, Turns: turns, Sources: sessions,
				Trajectories: len(record.TrajectoryIDs),
				Questions:    evaluationCases, EvaluationCases: evaluationCases,
				CaseIDs: []string{record.CaseID},
			})
			continue
		}
		group := &catalog.Datasets[index]
		group.CaseIDs = append(group.CaseIDs, record.CaseID)
		group.Questions += questionCounts[record.CaseID]
		group.EvaluationCases += questionCounts[record.CaseID]
	}
	for index := range catalog.Datasets {
		sort.Strings(catalog.Datasets[index].CaseIDs)
	}
	return nil
}

func loadPreparedRecords(path string) ([]preparedRecord, error) {
	var records []preparedRecord
	err := decodeJSONObjects(path, func(decoder *json.Decoder) error {
		var record preparedRecord
		if err := decoder.Decode(&record); err != nil {
			return err
		}
		records = append(records, record)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("decode prepared ingest %s: %w", path, err)
	}
	return records, nil
}

func loadPreparedQuestionCounts(path string) (map[string]int, error) {
	counts := make(map[string]int)
	err := decodeJSONObjects(path, func(decoder *json.Decoder) error {
		var query preparedQuery
		if err := decoder.Decode(&query); err != nil {
			return err
		}
		groupID := query.SourceCaseID
		if groupID == "" {
			groupID = query.CaseID
		}
		counts[groupID]++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("decode prepared queries %s: %w", path, err)
	}
	return counts, nil
}

func decodeJSONObjects(path string, decode func(*json.Decoder) error) (resultErr error) {
	input, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open JSON records: %w", err)
	}
	defer func() {
		if err := input.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close JSON records: %w", err))
		}
	}()
	decoder := json.NewDecoder(input)
	for {
		if err := decode(decoder); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
	}
}

func preparedRecordSize(record preparedRecord) (int, int) {
	turns := 0
	for _, session := range record.Sessions {
		turns += len(session.Turns)
	}
	return len(record.Sessions), turns
}

func trajectoryEnvironmentID(trajectoryIDs []string) string {
	digest := sha256.Sum256([]byte(strings.Join(trajectoryIDs, "\x00")))
	return "env-" + hex.EncodeToString(digest[:6])
}

func inferGroupKind(dataset, sourceKind string) string {
	switch {
	case sourceKind == "agent-trajectory-haystack":
		return "environment"
	case strings.EqualFold(dataset, "locomo"):
		return "conversation"
	default:
		return "case"
	}
}

func upsertDatasetGroup(catalog *Catalog, incoming Dataset) {
	for index := range catalog.Datasets {
		existing := &catalog.Datasets[index]
		if existing.ID != incoming.ID {
			continue
		}
		mergeDatasetGroup(existing, incoming)
		return
	}
	catalog.Datasets = append(catalog.Datasets, incoming)
}

func mergeDatasetGroup(existing *Dataset, incoming Dataset) {
	if existing.GroupKind == "" {
		existing.GroupKind = incoming.GroupKind
	}
	if existing.SourceKind == "" {
		existing.SourceKind = incoming.SourceKind
	}
	if existing.Sessions == 0 {
		existing.Sessions = incoming.Sessions
	}
	if existing.Turns == 0 {
		existing.Turns = incoming.Turns
	}
	if existing.Sources == 0 {
		existing.Sources = incoming.Sources
	}
	if existing.EvaluationCases == 0 {
		existing.EvaluationCases = incoming.EvaluationCases
		existing.Questions = incoming.Questions
	}
	if len(existing.CaseIDs) == 0 {
		existing.CaseIDs = slices.Clone(incoming.CaseIDs)
	}
	existing.RunCount += incoming.RunCount
	existing.ArtifactCount += incoming.ArtifactCount
	existing.ExperimentCount += incoming.ExperimentCount
	if !incoming.UpdatedAt.Before(existing.UpdatedAt) {
		existing.Status = incoming.Status
		existing.UpdatedAt = incoming.UpdatedAt
		existing.SourceArtifact = incoming.SourceArtifact
	}
}

func finalizeDatasetFamilies(catalog *Catalog) {
	indexes := make(map[string]int, len(catalog.Families))
	for index := range catalog.Families {
		indexes[catalog.Families[index].ID] = index
		catalog.Families[index].Partitions = nil
		catalog.Families[index].GroupCount = 0
		catalog.Families[index].RunGroupCount = 0
		catalog.Families[index].RunCount = 0
		catalog.Families[index].ArtifactCount = 0
	}
	partitionIndexes := make(map[string]map[string]int)
	familyGroups := make(map[string]map[string]struct{})
	familyRunGroups := make(map[string]map[string]struct{})
	for _, group := range catalog.Datasets {
		index, exists := indexes[group.Name]
		if !exists {
			index = len(catalog.Families)
			indexes[group.Name] = index
			catalog.Families = append(catalog.Families, DatasetFamily{
				ID: group.Name, Name: group.Name,
			})
		}
		family := &catalog.Families[index]
		if family.GroupKind == "" {
			family.GroupKind = group.GroupKind
		} else if family.GroupKind != group.GroupKind {
			family.GroupKind = "mixed"
		}
		if familyGroups[family.ID] == nil {
			familyGroups[family.ID] = make(map[string]struct{})
			familyRunGroups[family.ID] = make(map[string]struct{})
		}
		familyGroups[family.ID][group.CaseID] = struct{}{}
		family.RunCount += group.RunCount
		family.ArtifactCount += group.ArtifactCount
		if group.RunCount > 0 {
			familyRunGroups[family.ID][group.CaseID] = struct{}{}
		}
		if partitionIndexes[family.ID] == nil {
			partitionIndexes[family.ID] = make(map[string]int)
		}
		partitionIndex, exists := partitionIndexes[family.ID][group.Partition]
		if !exists {
			family.Partitions = append(family.Partitions, DatasetPartition{
				Name: group.Partition,
			})
			partitionIndex = len(family.Partitions) - 1
			partitionIndexes[family.ID][group.Partition] = partitionIndex
		}
		partition := &family.Partitions[partitionIndex]
		partition.GroupCount++
		if group.RunCount > 0 {
			partition.RunGroupCount++
		}
	}
	for index := range catalog.Families {
		catalog.Families[index].GroupCount = len(familyGroups[catalog.Families[index].ID])
		catalog.Families[index].RunGroupCount = len(
			familyRunGroups[catalog.Families[index].ID],
		)
		sort.Slice(catalog.Families[index].Partitions, func(left, right int) bool {
			return catalog.Families[index].Partitions[left].Name <
				catalog.Families[index].Partitions[right].Name
		})
	}
}
