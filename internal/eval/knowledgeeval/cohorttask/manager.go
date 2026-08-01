package cohorttask

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/dashboard"
	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval/experimenttask"
)

const campaignStoreFile = "campaigns.json"

type Catalog interface {
	Load(context.Context) (dashboard.Catalog, error)
}

type TaskService interface {
	Preview(context.Context, experimenttask.Request) (experimenttask.Preview, error)
	Create(context.Context, experimenttask.Request, string) (experimenttask.Task, error)
	Get(string) (experimenttask.Task, error)
	Cancel(string) (experimenttask.Task, error)
}

type ManagerConfig struct {
	Directory    string
	Catalog      Catalog
	Tasks        TaskService
	Now          func() time.Time
	IDGenerator  func() (string, error)
	TickInterval time.Duration
}

type Manager struct {
	mu           sync.Mutex
	directory    string
	catalog      Catalog
	tasks        TaskService
	now          func() time.Time
	idGenerator  func() (string, error)
	campaigns    map[string]*Campaign
	order        []string
	idempotency  map[string]string
	tickInterval time.Duration
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
}

func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Catalog == nil || config.Tasks == nil {
		return nil, fmt.Errorf(
			"%w: cohort catalog and task service are required",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	if strings.TrimSpace(config.Directory) == "" {
		return nil, fmt.Errorf("%w: cohort directory is required", knowledgeeval.ErrInvalidRecord)
	}
	directory, err := filepath.Abs(config.Directory)
	if err != nil {
		return nil, fmt.Errorf("resolve cohort directory: %w", err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create cohort directory: %w", err)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.IDGenerator == nil {
		config.IDGenerator = randomCampaignID
	}
	if config.TickInterval <= 0 {
		config.TickInterval = time.Second
	}
	managerContext, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		directory: directory, catalog: config.Catalog, tasks: config.Tasks,
		now: config.Now, idGenerator: config.IDGenerator,
		campaigns: make(map[string]*Campaign), idempotency: make(map[string]string),
		tickInterval: config.TickInterval, ctx: managerContext, cancel: cancel,
		done: make(chan struct{}),
	}
	if err := manager.load(); err != nil {
		cancel()
		return nil, err
	}
	go manager.work()
	return manager, nil
}

func (m *Manager) Close() {
	m.cancel()
	<-m.done
}

func (m *Manager) Preview(ctx context.Context, request Request) (Preview, error) {
	preview, _, _, err := m.plan(ctx, request)
	return preview, err
}

func (m *Manager) Create(
	ctx context.Context,
	request Request,
	idempotencyKey string,
) (Campaign, error) {
	request = normalizeRequest(request)
	if strings.TrimSpace(idempotencyKey) == "" {
		return Campaign{}, fmt.Errorf("%w: Idempotency-Key is required", knowledgeeval.ErrInvalidRecord)
	}
	preview, executions, normalized, err := m.plan(ctx, request)
	if err != nil {
		return Campaign{}, err
	}
	if !preview.Eligible {
		return Campaign{}, fmt.Errorf("%w: cohort has no eligible groups", knowledgeeval.ErrInvalidRecord)
	}
	if preview.Paid && !normalized.ConfirmPaid {
		return Campaign{}, fmt.Errorf(
			"%w: paid cohort execution requires explicit confirmation",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	if preview.Paid && normalized.LLMCallLimit < preview.MaxLLMCalls {
		return Campaign{}, fmt.Errorf(
			"%w: LLM call limit %d is below planned maximum %d",
			knowledgeeval.ErrInvalidRecord,
			normalized.LLMCallLimit,
			preview.MaxLLMCalls,
		)
	}
	digest, err := requestDigest(normalized)
	if err != nil {
		return Campaign{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existingID, exists := m.idempotency[idempotencyKey]; exists {
		existing := m.campaigns[existingID]
		if existing.RequestDigest != digest {
			return Campaign{}, fmt.Errorf(
				"%w: Idempotency-Key was already used for another cohort",
				ErrConflict,
			)
		}
		return cloneCampaign(*existing), nil
	}
	campaignID, err := m.idGenerator()
	if err != nil {
		return Campaign{}, fmt.Errorf("generate cohort ID: %w", err)
	}
	if _, exists := m.campaigns[campaignID]; exists {
		return Campaign{}, fmt.Errorf("%w: duplicate cohort ID %s", ErrConflict, campaignID)
	}
	now := m.now().UTC()
	campaign := &Campaign{
		ID: campaignID, Request: normalized, Preview: preview, Status: StatusQueued,
		Summary: initialSummary(preview), Executions: executions,
		CreatedAt: now, UpdatedAt: now, IdempotencyKey: idempotencyKey,
		RequestDigest: digest,
	}
	m.campaigns[campaignID] = campaign
	m.order = append(m.order, campaignID)
	m.idempotency[idempotencyKey] = campaignID
	if err := m.persistLocked(); err != nil {
		delete(m.campaigns, campaignID)
		delete(m.idempotency, idempotencyKey)
		m.order = m.order[:len(m.order)-1]
		return Campaign{}, err
	}
	return cloneCampaign(*campaign), nil
}

func (m *Manager) List() []Campaign {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Campaign, 0, len(m.order))
	for index := len(m.order) - 1; index >= 0; index-- {
		result = append(result, cloneCampaign(*m.campaigns[m.order[index]]))
	}
	return result
}

func (m *Manager) Get(campaignID string) (Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	campaign, exists := m.campaigns[campaignID]
	if !exists {
		return Campaign{}, fmt.Errorf("%w: cohort %s", knowledgeeval.ErrNotFound, campaignID)
	}
	return cloneCampaign(*campaign), nil
}

func (m *Manager) Cancel(campaignID string) (Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	campaign, exists := m.campaigns[campaignID]
	if !exists {
		return Campaign{}, fmt.Errorf("%w: cohort %s", knowledgeeval.ErrNotFound, campaignID)
	}
	if isTerminalCampaign(campaign.Status) {
		return cloneCampaign(*campaign), fmt.Errorf(
			"%w: cohort %s is already %s", ErrConflict, campaignID, campaign.Status,
		)
	}
	campaign.CancellationRequested = true
	for index := range campaign.Executions {
		execution := &campaign.Executions[index]
		if execution.TaskID != "" && isActiveExecution(execution.Status) {
			if _, err := m.tasks.Cancel(execution.TaskID); err != nil &&
				!errors.Is(err, experimenttask.ErrConflict) {
				return Campaign{}, fmt.Errorf("cancel cohort child %s: %w", execution.TaskID, err)
			}
			execution.Status = ExecutionCancelled
		}
		if execution.Status == ExecutionPlanned {
			execution.Status = ExecutionCancelled
		}
	}
	now := m.now().UTC()
	campaign.Status = StatusCancelled
	campaign.UpdatedAt = now
	campaign.CompletedAt = &now
	campaign.Summary = summarize(*campaign)
	if err := m.persistLocked(); err != nil {
		return Campaign{}, err
	}
	return cloneCampaign(*campaign), nil
}

func (m *Manager) Reconcile(ctx context.Context) error {
	catalog, err := m.catalog.Load(ctx)
	if err != nil {
		return fmt.Errorf("load cohort result catalog: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := false
	for _, campaignID := range m.order {
		campaign := m.campaigns[campaignID]
		if isTerminalCampaign(campaign.Status) {
			continue
		}
		campaignChanged, reconcileErr := m.reconcileCampaign(ctx, campaign, catalog)
		if reconcileErr != nil {
			return reconcileErr
		}
		changed = changed || campaignChanged
	}
	if changed {
		return m.persistLocked()
	}
	return nil
}

func (m *Manager) reconcileCampaign(
	ctx context.Context,
	campaign *Campaign,
	catalog dashboard.Catalog,
) (bool, error) {
	changed := false
	for index := range campaign.Executions {
		execution := &campaign.Executions[index]
		if execution.TaskID == "" || !isActiveExecution(execution.Status) {
			continue
		}
		child, err := m.tasks.Get(execution.TaskID)
		if err != nil {
			return false, fmt.Errorf("get cohort child %s: %w", execution.TaskID, err)
		}
		if syncExecution(execution, child, campaign.Request.Recipe, catalog) {
			changed = true
		}
		if isActiveExecution(execution.Status) {
			campaign.Status = StatusRunning
			campaign.Summary = summarize(*campaign)
			return changed, nil
		}
	}
	for index := range campaign.Executions {
		execution := &campaign.Executions[index]
		if execution.Status != ExecutionPlanned {
			continue
		}
		child, err := m.tasks.Create(
			ctx,
			execution.TaskRequest,
			childIdempotencyKey(campaign.ID, *execution),
		)
		if err != nil {
			execution.Status = ExecutionFailed
			execution.Error = err.Error()
			continue
		}
		execution.TaskID = child.ID
		execution.Status = executionStatus(child.Status)
		campaign.Status = StatusRunning
		campaign.UpdatedAt = m.now().UTC()
		campaign.Summary = summarize(*campaign)
		return true, nil
	}
	now := m.now().UTC()
	campaign.Summary = summarize(*campaign)
	campaign.Status = StatusCompleted
	if campaign.Preview.IneligibleGroups > 0 || campaign.Summary.FailedGroups > 0 ||
		campaign.Summary.EvaluatedGroups < campaign.Preview.TotalGroups {
		campaign.Status = StatusCompletedWithGaps
	}
	campaign.UpdatedAt = now
	campaign.CompletedAt = &now
	return true, nil
}

func syncExecution(
	execution *Execution,
	child experimenttask.Task,
	recipe Recipe,
	catalog dashboard.Catalog,
) bool {
	previous := *execution
	switch child.Status {
	case experimenttask.StatusQueued:
		execution.Status = ExecutionQueued
	case experimenttask.StatusRunning:
		execution.Status = ExecutionRunning
	case experimenttask.StatusCompleted:
		if !applyResult(execution, child, recipe, catalog) {
			execution.Status = ExecutionResultPending
		} else {
			execution.Status = ExecutionCompleted
		}
	case experimenttask.StatusCancelled:
		execution.Status = ExecutionCancelled
	case experimenttask.StatusFailed, experimenttask.StatusNeedsMoreRounds:
		execution.Status = ExecutionFailed
		execution.Error = child.Error
	}
	return !executionsEqual(previous, *execution)
}

func applyResult(
	execution *Execution,
	child experimenttask.Task,
	recipe Recipe,
	catalog dashboard.Catalog,
) bool {
	run, exists := selectResultRun(child.RunIDs, recipe, catalog.Runs)
	if !exists {
		return false
	}
	for _, trial := range run.Detail.Trials {
		if trial.BenchmarkID != "knowledge-search-get-qa" || trial.Result == nil {
			continue
		}
		accuracy, accuracyOK := metricValue(trial.Result.Metrics, "answer_accuracy")
		caseCount, countOK := metricValue(trial.Result.Metrics, "case_count")
		if !accuracyOK || !countOK {
			continue
		}
		execution.RunID = run.Detail.Run.ID
		execution.EvaluatedQuestions = int(math.Round(caseCount))
		execution.CorrectQuestions = int(math.Round(accuracy * caseCount))
		execution.Accuracy = accuracy
		return true
	}
	return false
}

func selectResultRun(
	runIDs []string,
	recipe Recipe,
	runs []dashboard.Run,
) (dashboard.Run, bool) {
	wanted := make(map[string]struct{}, len(runIDs))
	for _, runID := range runIDs {
		wanted[runID] = struct{}{}
	}
	wantMaintainer := recipe.Mode == experimenttask.ModeMaintainer
	var fallback dashboard.Run
	fallbackExists := false
	for _, run := range runs {
		if _, exists := wanted[run.Detail.Run.ID]; !exists {
			continue
		}
		fallback = run
		fallbackExists = true
		isMaintainer := run.SolutionVersion.BuilderID == "llmwiki-maintainer"
		if isMaintainer == wantMaintainer {
			return run, true
		}
	}
	return fallback, fallbackExists
}

func (m *Manager) plan(
	ctx context.Context,
	request Request,
) (Preview, []Execution, Request, error) {
	request = normalizeRequest(request)
	if strings.TrimSpace(request.Name) == "" || len(request.Selections) == 0 {
		return Preview{}, nil, Request{}, fmt.Errorf(
			"%w: cohort name and dataset selections are required",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	catalog, err := m.catalog.Load(ctx)
	if err != nil {
		return Preview{}, nil, Request{}, fmt.Errorf("load cohort catalog: %w", err)
	}
	selected := make(map[string]DatasetSelection, len(request.Selections))
	for _, selection := range request.Selections {
		if selection.Dataset == "" || selection.Partition == "" {
			return Preview{}, nil, Request{}, fmt.Errorf(
				"%w: cohort dataset and partition are required",
				knowledgeeval.ErrInvalidRecord,
			)
		}
		selected[selectionKey(selection.Dataset, selection.Partition)] = selection
	}
	datasets := slices.Clone(catalog.Datasets)
	sort.Slice(datasets, func(left, right int) bool {
		return datasetKey(datasets[left]) < datasetKey(datasets[right])
	})
	preview := Preview{}
	var executions []Execution
	previews := make(map[string]*DatasetPreview, len(selected))
	for _, dataset := range datasets {
		key := selectionKey(dataset.Name, dataset.Partition)
		if _, exists := selected[key]; !exists {
			continue
		}
		datasetPreview := previews[key]
		if datasetPreview == nil {
			datasetPreview = &DatasetPreview{Dataset: dataset.Name, Partition: dataset.Partition}
			previews[key] = datasetPreview
		}
		childRequest := experimenttask.Request{
			Dataset: dataset.Name, Partition: dataset.Partition, GroupID: dataset.CaseID,
			Mode: request.Recipe.Mode, Model: request.Recipe.Model,
			ReaderModel: request.Recipe.ReaderModel, MaxRounds: request.Recipe.MaxRounds,
			QuestionLimit: dataset.EvaluationCases, ConfirmPaid: request.ConfirmPaid,
		}
		childPreview, previewErr := m.tasks.Preview(ctx, childRequest)
		if previewErr != nil {
			return Preview{}, nil, Request{}, fmt.Errorf(
				"preview cohort group %s/%s/%s: %w",
				dataset.Name, dataset.Partition, dataset.CaseID, previewErr,
			)
		}
		execution := Execution{
			Dataset: dataset.Name, Partition: dataset.Partition, GroupID: dataset.CaseID,
			Questions: dataset.EvaluationCases, Status: ExecutionPlanned,
			TaskRequest: childRequest,
		}
		preview.TotalGroups++
		preview.TotalQuestions += dataset.EvaluationCases
		datasetPreview.TotalGroups++
		datasetPreview.TotalQuestions += dataset.EvaluationCases
		if !childPreview.Eligible {
			execution.Status = ExecutionIneligible
			execution.IneligibleReason = childPreview.IneligibleReason
			preview.IneligibleGroups++
			datasetPreview.IneligibleGroups++
			preview.Issues = append(preview.Issues, Issue{
				Dataset: dataset.Name, Partition: dataset.Partition,
				GroupID: dataset.CaseID, Reason: childPreview.IneligibleReason,
			})
		} else {
			preview.EligibleGroups++
			preview.PlannedTasks++
			preview.PlannedQuestions += childPreview.SelectedQuestions
			preview.MaxLLMCalls += childPreview.MaxLLMCalls
			preview.Paid = preview.Paid || childPreview.Paid
			datasetPreview.EligibleGroups++
			datasetPreview.PlannedQuestions += childPreview.SelectedQuestions
			datasetPreview.MaxLLMCalls += childPreview.MaxLLMCalls
		}
		executions = append(executions, execution)
	}
	selectionKeys := make([]string, 0, len(selected))
	for key := range selected {
		selectionKeys = append(selectionKeys, key)
	}
	sort.Strings(selectionKeys)
	for _, key := range selectionKeys {
		datasetPreview := previews[key]
		if datasetPreview == nil {
			selection := selected[key]
			preview.Issues = append(preview.Issues, Issue{
				Dataset: selection.Dataset, Partition: selection.Partition,
				Reason: "dataset selection contains no groups",
			})
			continue
		}
		preview.Datasets = append(preview.Datasets, *datasetPreview)
	}
	preview.Eligible = preview.EligibleGroups > 0
	return preview, executions, request, nil
}

func normalizeRequest(request Request) Request {
	request.Name = strings.TrimSpace(request.Name)
	request.Recipe.Mode = strings.ToLower(strings.TrimSpace(request.Recipe.Mode))
	request.Recipe.Model = strings.TrimSpace(request.Recipe.Model)
	request.Recipe.ReaderModel = strings.TrimSpace(request.Recipe.ReaderModel)
	if request.Recipe.Mode == "" {
		request.Recipe.Mode = experimenttask.ModeMaintainer
	}
	normalized := make([]DatasetSelection, 0, len(request.Selections))
	seen := make(map[string]struct{}, len(request.Selections))
	for _, selection := range request.Selections {
		selection.Dataset = strings.TrimSpace(selection.Dataset)
		selection.Partition = strings.TrimSpace(selection.Partition)
		key := selectionKey(selection.Dataset, selection.Partition)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, selection)
	}
	sort.Slice(normalized, func(left, right int) bool {
		return selectionKey(normalized[left].Dataset, normalized[left].Partition) <
			selectionKey(normalized[right].Dataset, normalized[right].Partition)
	})
	request.Selections = normalized
	return request
}

func summarize(campaign Campaign) Summary {
	summary := initialSummary(campaign.Preview)
	macroTotal := 0.0
	for _, execution := range campaign.Executions {
		switch execution.Status {
		case ExecutionCompleted:
			if execution.EvaluatedQuestions > 0 {
				summary.EvaluatedGroups++
				summary.EvaluatedQuestions += execution.EvaluatedQuestions
				summary.CorrectQuestions += execution.CorrectQuestions
				macroTotal += execution.Accuracy
			}
		case ExecutionFailed, ExecutionCancelled:
			summary.FailedGroups++
		}
	}
	if summary.EvaluatedQuestions > 0 {
		summary.MicroAccuracy = float64(summary.CorrectQuestions) /
			float64(summary.EvaluatedQuestions)
	}
	if summary.EvaluatedGroups > 0 {
		summary.MacroAccuracy = macroTotal / float64(summary.EvaluatedGroups)
	}
	if summary.TotalGroups > 0 {
		summary.GroupCoverage = float64(summary.EvaluatedGroups) / float64(summary.TotalGroups)
	}
	if summary.TotalQuestions > 0 {
		summary.QuestionCoverage = float64(summary.EvaluatedQuestions) /
			float64(summary.TotalQuestions)
	}
	return summary
}

func initialSummary(preview Preview) Summary {
	return Summary{
		TotalGroups: preview.TotalGroups, EligibleGroups: preview.EligibleGroups,
		TotalQuestions: preview.TotalQuestions,
	}
}

func metricValue(metrics []knowledgeeval.Metric, name string) (float64, bool) {
	for _, metric := range metrics {
		if metric.Name == name {
			return metric.Value, true
		}
	}
	return 0, false
}

func executionStatus(taskStatus string) string {
	if taskStatus == experimenttask.StatusRunning {
		return ExecutionRunning
	}
	return ExecutionQueued
}

func isActiveExecution(status string) bool {
	return status == ExecutionQueued || status == ExecutionRunning ||
		status == ExecutionResultPending
}

func isTerminalCampaign(status string) bool {
	return status == StatusCompleted || status == StatusCompletedWithGaps ||
		status == StatusCancelled
}

func executionsEqual(left, right Execution) bool {
	left.TaskRequest = experimenttask.Request{}
	right.TaskRequest = experimenttask.Request{}
	return left == right
}

func selectionKey(dataset, partition string) string {
	return dataset + "\x00" + partition
}

func datasetKey(dataset dashboard.Dataset) string {
	return selectionKey(dataset.Name, dataset.Partition) + "\x00" + dataset.CaseID
}

func childIdempotencyKey(campaignID string, execution Execution) string {
	return strings.Join([]string{
		campaignID, execution.Dataset, execution.Partition, execution.GroupID,
	}, ":")
}

func requestDigest(request Request) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode cohort request: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func randomCampaignID() (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "cohort-" + hex.EncodeToString(random[:]), nil
}

func cloneCampaign(campaign Campaign) Campaign {
	campaign.Request.Selections = slices.Clone(campaign.Request.Selections)
	campaign.Preview.Datasets = slices.Clone(campaign.Preview.Datasets)
	campaign.Preview.Issues = slices.Clone(campaign.Preview.Issues)
	campaign.Executions = slices.Clone(campaign.Executions)
	return campaign
}

func (m *Manager) work() {
	defer close(m.done)
	ticker := time.NewTicker(m.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if err := m.Reconcile(m.ctx); err != nil {
				continue
			}
		}
	}
}

func (m *Manager) load() error {
	encoded, err := os.ReadFile(filepath.Join(m.directory, campaignStoreFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read cohort store: %w", err)
	}
	var campaigns []Campaign
	if err := json.Unmarshal(encoded, &campaigns); err != nil {
		return fmt.Errorf("decode cohort store: %w", err)
	}
	for index := range campaigns {
		campaign := cloneCampaign(campaigns[index])
		m.campaigns[campaign.ID] = &campaign
		m.order = append(m.order, campaign.ID)
		m.idempotency[campaign.IdempotencyKey] = campaign.ID
	}
	sort.SliceStable(m.order, func(left, right int) bool {
		return m.campaigns[m.order[left]].CreatedAt.Before(
			m.campaigns[m.order[right]].CreatedAt,
		)
	})
	return nil
}

func (m *Manager) persistLocked() (returnedErr error) {
	campaigns := make([]Campaign, 0, len(m.order))
	for _, campaignID := range m.order {
		campaigns = append(campaigns, cloneCampaign(*m.campaigns[campaignID]))
	}
	encoded, err := json.MarshalIndent(campaigns, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cohort store: %w", err)
	}
	temporary, err := os.CreateTemp(m.directory, ".campaigns-*.json")
	if err != nil {
		return fmt.Errorf("create temporary cohort store: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := temporary.Close(); closeErr != nil {
				returnedErr = errors.Join(returnedErr, fmt.Errorf("close cohort store: %w", closeErr))
			}
		}
		if removeErr := os.Remove(temporaryPath); removeErr != nil &&
			!errors.Is(removeErr, os.ErrNotExist) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("clean cohort store: %w", removeErr))
		}
	}()
	if _, err := temporary.Write(encoded); err != nil {
		return fmt.Errorf("write cohort store: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync cohort store: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close cohort store before rename: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryPath, filepath.Join(m.directory, campaignStoreFile)); err != nil {
		return fmt.Errorf("replace cohort store: %w", err)
	}
	return nil
}
