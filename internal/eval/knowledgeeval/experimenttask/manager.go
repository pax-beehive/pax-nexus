package experimenttask

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/knowledgeeval"
)

const taskStoreFile = "tasks.json"

type Previewer interface {
	Preview(context.Context, Request) (Preview, error)
}

type ManagerConfig struct {
	Directory   string
	Previewer   Previewer
	Executor    Executor
	Now         func() time.Time
	IDGenerator func() (string, error)
}

type Manager struct {
	mu          sync.Mutex
	directory   string
	previewer   Previewer
	executor    Executor
	now         func() time.Time
	idGenerator func() (string, error)
	tasks       map[string]*Task
	order       []string
	idempotency map[string]string
	cancels     map[string]context.CancelFunc
	queue       chan string
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Previewer == nil || config.Executor == nil {
		return nil, fmt.Errorf(
			"%w: task previewer and executor are required",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	if config.Directory == "" {
		return nil, fmt.Errorf(
			"%w: task directory is required",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	directory, err := filepath.Abs(config.Directory)
	if err != nil {
		return nil, fmt.Errorf("resolve task directory: %w", err)
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create task directory: %w", err)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.IDGenerator == nil {
		config.IDGenerator = randomTaskID
	}
	managerContext, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		directory: directory, previewer: config.Previewer, executor: config.Executor,
		now: config.Now, idGenerator: config.IDGenerator,
		tasks: make(map[string]*Task), idempotency: make(map[string]string),
		cancels: make(map[string]context.CancelFunc), queue: make(chan string, 256),
		ctx: managerContext, cancel: cancel, done: make(chan struct{}),
	}
	queued, err := manager.load()
	if err != nil {
		cancel()
		return nil, err
	}
	go manager.work()
	for _, taskID := range queued {
		manager.queue <- taskID
	}
	return manager, nil
}

func (m *Manager) Close() {
	m.cancel()
	<-m.done
}

func (m *Manager) Preview(ctx context.Context, request Request) (Preview, error) {
	return m.previewer.Preview(ctx, NormalizeRequest(request))
}

func (m *Manager) Create(
	ctx context.Context,
	request Request,
	idempotencyKey string,
) (Task, error) {
	return m.create(ctx, request, idempotencyKey, taskLineage{})
}

func (m *Manager) Retry(
	ctx context.Context,
	taskID string,
	idempotencyKey string,
) (Task, error) {
	source, err := m.Get(taskID)
	if err != nil {
		return Task{}, err
	}
	if source.Status != StatusFailed && source.Status != StatusCancelled &&
		source.Status != StatusNeedsMoreRounds {
		return Task{}, fmt.Errorf(
			"%w: task %s is %s; only failed or cancelled tasks can be retried",
			ErrConflict,
			taskID,
			source.Status,
		)
	}
	request := source.Request
	request.ContinueFromTaskID = ""
	return m.create(ctx, request, idempotencyKey, taskLineage{RetryOfTaskID: source.ID})
}

type ContinueOptions struct {
	AdditionalRounds    int
	AdditionalQuestions int
}

func (m *Manager) Continue(
	ctx context.Context,
	taskID string,
	options ContinueOptions,
	idempotencyKey string,
) (Task, error) {
	source, err := m.Get(taskID)
	if err != nil {
		return Task{}, err
	}
	if options.AdditionalRounds > 0 && options.AdditionalQuestions > 0 {
		return Task{}, fmt.Errorf(
			"%w: continue either build rounds or questions, not both",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	if options.AdditionalQuestions > 0 {
		return m.continueQuestions(ctx, source, options.AdditionalQuestions, idempotencyKey)
	}
	if (source.Status != StatusFailed && source.Status != StatusNeedsMoreRounds) ||
		source.Request.Mode != ModeMaintainer {
		return Task{}, fmt.Errorf(
			"%w: only failed maintainer tasks can continue repair",
			ErrConflict,
		)
	}
	if options.AdditionalRounds <= 0 {
		options.AdditionalRounds = 10
	}
	if options.AdditionalRounds > 200 {
		return Task{}, fmt.Errorf(
			"%w: additional rounds must be between 1 and 200",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	request := source.Request
	request.MaxRounds = options.AdditionalRounds
	request.ContinueFromTaskID = source.ID
	request.ReuseArtifactFromTaskID = ""
	return m.create(ctx, request, idempotencyKey, taskLineage{ContinuedFromTaskID: source.ID})
}

func (m *Manager) continueQuestions(
	ctx context.Context,
	source Task,
	additionalQuestions int,
	idempotencyKey string,
) (Task, error) {
	if source.Status != StatusCompleted || source.Request.Mode != ModeMaintainer {
		return Task{}, fmt.Errorf(
			"%w: only completed maintainer tasks can reuse an artifact for more questions",
			ErrConflict,
		)
	}
	if additionalQuestions <= 0 {
		return Task{}, fmt.Errorf(
			"%w: additional questions must be positive",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	request := source.Request
	request.QuestionOffset = source.Request.QuestionOffset + source.Preview.SelectedQuestions
	request.QuestionLimit = additionalQuestions
	request.MaxRounds = 0
	request.ContinueFromTaskID = ""
	request.ReuseArtifactFromTaskID = source.ID
	return m.create(
		ctx,
		request,
		idempotencyKey,
		taskLineage{ContinuedFromTaskID: source.ID},
	)
}

type taskLineage struct {
	RetryOfTaskID       string
	ContinuedFromTaskID string
}

func (m *Manager) create(
	ctx context.Context,
	request Request,
	idempotencyKey string,
	lineage taskLineage,
) (Task, error) {
	request = NormalizeRequest(request)
	if idempotencyKey == "" {
		return Task{}, fmt.Errorf(
			"%w: Idempotency-Key is required",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	preview, err := m.previewer.Preview(ctx, request)
	if err != nil {
		return Task{}, err
	}
	if !preview.Eligible {
		return Task{}, fmt.Errorf(
			"%w: %s",
			knowledgeeval.ErrInvalidRecord,
			preview.IneligibleReason,
		)
	}
	if preview.Paid && !request.ConfirmPaid {
		return Task{}, fmt.Errorf(
			"%w: paid LLM execution requires explicit confirmation",
			knowledgeeval.ErrInvalidRecord,
		)
	}
	requestDigest, err := digestRequest(request)
	if err != nil {
		return Task{}, err
	}

	m.mu.Lock()
	if existingID, exists := m.idempotency[idempotencyKey]; exists {
		existing := m.tasks[existingID]
		if existing.RequestDigest != requestDigest {
			m.mu.Unlock()
			return Task{}, fmt.Errorf(
				"%w: Idempotency-Key was already used for a different request",
				ErrConflict,
			)
		}
		result := cloneTask(*existing)
		m.mu.Unlock()
		return result, nil
	}
	taskID, err := m.idGenerator()
	if err != nil {
		m.mu.Unlock()
		return Task{}, fmt.Errorf("generate task ID: %w", err)
	}
	if _, exists := m.tasks[taskID]; exists {
		m.mu.Unlock()
		return Task{}, fmt.Errorf("%w: duplicate task ID %s", ErrConflict, taskID)
	}
	now := m.now().UTC()
	task := &Task{
		ID: taskID, Request: request, Preview: preview, Status: StatusQueued,
		CreatedAt: now, UpdatedAt: now, IdempotencyKey: idempotencyKey,
		RequestDigest:       requestDigest,
		RetryOfTaskID:       lineage.RetryOfTaskID,
		ContinuedFromTaskID: lineage.ContinuedFromTaskID,
		Events:              []Event{{Status: StatusQueued, Message: "Task queued.", CreatedAt: now}},
	}
	m.tasks[taskID] = task
	m.order = append(m.order, taskID)
	m.idempotency[idempotencyKey] = taskID
	if err := m.persistLocked(); err != nil {
		delete(m.tasks, taskID)
		delete(m.idempotency, idempotencyKey)
		m.order = m.order[:len(m.order)-1]
		m.mu.Unlock()
		return Task{}, err
	}
	result := cloneTask(*task)
	m.mu.Unlock()
	m.queue <- taskID
	return result, nil
}

func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Task, 0, len(m.order))
	for index := len(m.order) - 1; index >= 0; index-- {
		result = append(result, cloneTask(*m.tasks[m.order[index]]))
	}
	return result
}

func (m *Manager) Get(taskID string) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return Task{}, fmt.Errorf("%w: task %s", knowledgeeval.ErrNotFound, taskID)
	}
	return cloneTask(*task), nil
}

func (m *Manager) Cancel(taskID string) (Task, error) {
	m.mu.Lock()
	task, exists := m.tasks[taskID]
	if !exists {
		m.mu.Unlock()
		return Task{}, fmt.Errorf("%w: task %s", knowledgeeval.ErrNotFound, taskID)
	}
	now := m.now().UTC()
	switch task.Status {
	case StatusQueued:
		task.Status = StatusCancelled
		task.CompletedAt = &now
		task.UpdatedAt = now
		task.Events = append(task.Events, Event{
			Status: StatusCancelled, Message: "Queued task cancelled.", CreatedAt: now,
		})
	case StatusRunning:
		task.CancellationRequested = true
		task.UpdatedAt = now
		task.Events = append(task.Events, Event{
			Status: StatusRunning, Message: "Cancellation requested.", CreatedAt: now,
		})
		if cancel := m.cancels[taskID]; cancel != nil {
			cancel()
		}
	default:
		result := cloneTask(*task)
		m.mu.Unlock()
		return result, fmt.Errorf(
			"%w: task %s is already %s",
			ErrConflict,
			taskID,
			task.Status,
		)
	}
	if err := m.persistLocked(); err != nil {
		m.mu.Unlock()
		return Task{}, err
	}
	result := cloneTask(*task)
	m.mu.Unlock()
	return result, nil
}

func (m *Manager) work() {
	defer close(m.done)
	for {
		select {
		case <-m.ctx.Done():
			return
		case taskID := <-m.queue:
			m.execute(taskID)
		}
	}
}

func (m *Manager) execute(taskID string) {
	m.mu.Lock()
	task, exists := m.tasks[taskID]
	if !exists || task.Status != StatusQueued {
		m.mu.Unlock()
		return
	}
	startedAt := m.now().UTC()
	task.Status = StatusRunning
	task.StartedAt = &startedAt
	task.UpdatedAt = startedAt
	task.Events = append(task.Events, Event{
		Status: StatusRunning, Message: "Task started.", CreatedAt: startedAt,
	})
	taskContext, cancel := context.WithCancel(m.ctx)
	m.cancels[taskID] = cancel
	request := task.Request
	if err := m.persistLocked(); err != nil {
		task.Status = StatusFailed
		task.Error = err.Error()
	}
	m.mu.Unlock()

	result, runErr := m.executor.Execute(taskContext, taskID, request)
	cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	task = m.tasks[taskID]
	delete(m.cancels, taskID)
	completedAt := m.now().UTC()
	task.UpdatedAt = completedAt
	task.CompletedAt = &completedAt
	task.RunIDs = slices.Clone(result.RunIDs)
	task.ArtifactIDs = slices.Clone(result.ArtifactIDs)
	task.ResultPath = result.ResultPath
	switch {
	case task.CancellationRequested || errors.Is(runErr, context.Canceled):
		task.Status = StatusCancelled
		task.Error = ""
		task.Events = append(task.Events, Event{
			Status: StatusCancelled, Message: "Task cancelled.", CreatedAt: completedAt,
		})
	case runErr != nil:
		if errors.Is(runErr, ErrRoundLimitReached) {
			task.Status = StatusNeedsMoreRounds
			task.Error = runErr.Error()
			task.Events = append(task.Events, Event{
				Status:    StatusNeedsMoreRounds,
				Message:   "Round limit reached; the task can continue from its saved workspace.",
				CreatedAt: completedAt,
			})
		} else {
			task.Status = StatusFailed
			task.Error = runErr.Error()
			task.Events = append(task.Events, Event{
				Status: StatusFailed, Message: runErr.Error(), CreatedAt: completedAt,
			})
		}
	default:
		task.Status = StatusCompleted
		task.Error = ""
		task.Events = append(task.Events, Event{
			Status: StatusCompleted, Message: "Task completed.", CreatedAt: completedAt,
		})
	}
	if err := m.persistLocked(); err != nil {
		task.Status = StatusFailed
		task.Error = err.Error()
	}
}

func (m *Manager) load() ([]string, error) {
	path := filepath.Join(m.directory, taskStoreFile)
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read task store: %w", err)
	}
	var tasks []Task
	if err := json.Unmarshal(encoded, &tasks); err != nil {
		return nil, fmt.Errorf("decode task store: %w", err)
	}
	var queued []string
	now := m.now().UTC()
	for index := range tasks {
		task := cloneTask(tasks[index])
		if task.Status == StatusRunning {
			task.Status = StatusFailed
			task.UpdatedAt = now
			task.CompletedAt = &now
			task.Error = "API process stopped while task was running."
			task.Events = append(task.Events, Event{
				Status: StatusFailed, Message: task.Error, CreatedAt: now,
			})
		}
		if task.Status == StatusFailed && isPersistedRoundLimit(task.Error) {
			task.Status = StatusNeedsMoreRounds
			task.Events = append(task.Events, Event{
				Status:    StatusNeedsMoreRounds,
				Message:   "Reclassified round-limit outcome as awaiting continuation.",
				CreatedAt: now,
			})
		}
		m.tasks[task.ID] = &task
		m.order = append(m.order, task.ID)
		m.idempotency[task.IdempotencyKey] = task.ID
		if task.Status == StatusQueued {
			queued = append(queued, task.ID)
		}
	}
	sort.SliceStable(m.order, func(left, right int) bool {
		return m.tasks[m.order[left]].CreatedAt.Before(m.tasks[m.order[right]].CreatedAt)
	})
	if err := m.persistLocked(); err != nil {
		return nil, err
	}
	return queued, nil
}

func isPersistedRoundLimit(message string) bool {
	return strings.Contains(message, "agent exhausted ") &&
		strings.Contains(message, " rounds with invalid Wiki:")
}

func (m *Manager) persistLocked() (returnedErr error) {
	tasks := make([]Task, 0, len(m.order))
	for _, taskID := range m.order {
		tasks = append(tasks, cloneTask(*m.tasks[taskID]))
	}
	encoded, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return fmt.Errorf("encode task store: %w", err)
	}
	temporary, err := os.CreateTemp(m.directory, ".tasks-*.json")
	if err != nil {
		return fmt.Errorf("create temporary task store: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := temporary.Close(); closeErr != nil {
				returnedErr = errors.Join(returnedErr, fmt.Errorf("close task store: %w", closeErr))
			}
		}
		// This is best-effort cleanup after an atomic rename or failed write.
		if removeErr := os.Remove(temporaryPath); removeErr != nil &&
			!errors.Is(removeErr, os.ErrNotExist) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("clean task store: %w", removeErr))
		}
	}()
	if _, err := temporary.Write(encoded); err != nil {
		return fmt.Errorf("write task store: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync task store: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close task store before rename: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryPath, filepath.Join(m.directory, taskStoreFile)); err != nil {
		return fmt.Errorf("replace task store: %w", err)
	}
	return nil
}

func randomTaskID() (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "task-" + hex.EncodeToString(random[:]), nil
}

func digestRequest(request Request) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode task request: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func cloneTask(task Task) Task {
	task.Events = slices.Clone(task.Events)
	task.RunIDs = slices.Clone(task.RunIDs)
	task.ArtifactIDs = slices.Clone(task.ArtifactIDs)
	task.Preview.Benchmarks = slices.Clone(task.Preview.Benchmarks)
	return task
}
