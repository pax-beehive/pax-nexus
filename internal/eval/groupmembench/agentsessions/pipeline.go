package agentsessions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
	"github.com/pax-beehive/pax-nexus/internal/session"
)

type PipelineConfig struct {
	DomainFile   string
	QuestionsDir string
	OutDir       string
	CacheDir     string
	Model        string
	Concurrency  int
	MaxObs       int
	TopK         int
	Client       llm.ChatClient
}

type PipelineReport struct {
	Sessions   int                 `json:"sessions"`
	Users      int                 `json:"users"`
	Fill       FillStats           `json:"fill"`
	Violations int                 `json:"violations"`
	Exceptions []CoverageException `json:"exceptions"`
}

// RunPipeline 全流程:normalize → anchors → windows → skeleton → fill(并发
// Concurrency,信号量限流)→ assemble → attach evidence → convert → verify,
// 输出写 OutDir:sessions.jsonl / messages.jsonl / questions_enhanced.jsonl /
// session_batches.json / coverage_exceptions.json / report.json。
func RunPipeline(ctx context.Context, config PipelineConfig) (PipelineReport, error) {
	report := PipelineReport{Exceptions: []CoverageException{}}
	raw, err := groupmembench.LoadConversation(config.DomainFile)
	if err != nil {
		return report, err
	}
	msgs, violations, err := Normalize(raw)
	if err != nil {
		return report, err
	}
	report.Violations = len(violations)
	questions, err := groupmembench.LoadQuestions(config.QuestionsDir)
	if err != nil {
		return report, err
	}
	enhanced, err := RecoverAnchors(ctx, config.Client, config.Model,
		questions, msgs, config.TopK)
	if err != nil {
		return report, err
	}
	anchors := map[string]bool{}
	for _, q := range enhanced {
		for _, node := range q.EvidenceMsgIDs {
			anchors[node] = true
		}
	}
	parentAuthor := map[string]string{}
	msgText := map[string]string{}
	for _, m := range msgs {
		parentAuthor[m.NodeID] = m.Author
		msgText[m.NodeID] = m.Content
	}
	channels := UserChannels(msgs)
	users := make([]string, 0, len(channels))
	for user := range channels {
		users = append(users, user)
	}
	sort.Strings(users)
	report.Users = len(users)

	filler := &Filler{Client: config.Client, Model: config.Model,
		Cache: &DiskCache{Dir: config.CacheDir}}
	jobs := buildFillJobs(msgs, channels, users, parentAuthor, anchors, config.MaxObs)

	sessions, err := runFillJobs(ctx, filler, jobs, config.Concurrency)
	if err != nil {
		return report, err
	}
	report.Sessions = len(sessions)
	report.Fill = filler.Stats()

	enhanced = AttachEvidenceSessions(enhanced, sessions)
	report.Exceptions = VerifyCoverage(enhanced, sessions)
	if report.Exceptions == nil {
		report.Exceptions = []CoverageException{}
	}

	batches := make([]session.SessionBatch, 0, len(sessions))
	for _, s := range sessions {
		batch, err := ToSessionBatch(s, msgText)
		if err != nil {
			return report, err
		}
		batches = append(batches, batch)
	}
	if err := writeOutputs(config.OutDir, sessions, msgs, enhanced, batches, report); err != nil {
		return report, err
	}
	return report, nil
}

type fillJob struct {
	window  Window
	persona Persona
	specs   []ActionSpec
}

// buildFillJobs 按用户展开可见窗口,拼出待填充的 fill job 列表;PersonaOf
// 每用户只调用一次(窗口循环之外),以保留下游 LLM prefix cache 的复用价值。
func buildFillJobs(msgs []Msg, channels map[string][]string, users []string,
	parentAuthor map[string]string, anchors map[string]bool, maxObs int) []fillJob {
	var jobs []fillJob
	for _, user := range users {
		visible := VisibleTo(msgs, channels[user])
		persona := PersonaOf(msgs, user)
		for _, w := range Windows(user, visible, maxObs) {
			jobs = append(jobs, fillJob{window: w, persona: persona,
				specs: BuildSkeleton(w, parentAuthor, anchors)})
		}
	}
	return jobs
}

// runFillJobs 以信号量限流并发执行 fill job,首个错误短路并等待所有
// goroutine 退出后返回。
func runFillJobs(ctx context.Context, filler *Filler, jobs []fillJob, concurrency int) ([]Session, error) {
	if concurrency <= 0 {
		concurrency = 1
	}
	sessions := make([]Session, len(jobs))
	semaphore := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	for i, item := range jobs {
		wg.Add(1)
		go func(i int, item fillJob) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				errOnce.Do(func() { firstErr = ctx.Err() })
				return
			}
			defer func() { <-semaphore }()
			if ctx.Err() != nil {
				errOnce.Do(func() { firstErr = ctx.Err() })
				return
			}
			actions, err := filler.FillSession(ctx, item.window, item.persona,
				item.specs)
			if err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
			sessions[i] = BuildSession(item.window, item.persona, actions)
		}(i, item)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return sessions, nil
}

// writeOutputs 落盘 pipeline 的全部产出文件:sessions/messages/
// questions_enhanced 三个 JSONL,以及 session_batches/coverage_exceptions/
// report 三个 JSON。
func writeOutputs(outDir string, sessions []Session, msgs []Msg,
	enhanced []EnhancedQuestion, batches []session.SessionBatch,
	report PipelineReport) error {
	if err := WriteJSONL(filepath.Join(outDir, "sessions.jsonl"),
		sessions); err != nil {
		return err
	}
	if err := WriteJSONL(filepath.Join(outDir, "messages.jsonl"),
		MessageRows(msgs)); err != nil {
		return err
	}
	if err := WriteJSONL(filepath.Join(outDir, "questions_enhanced.jsonl"),
		enhanced); err != nil {
		return err
	}
	for name, value := range map[string]any{
		"session_batches.json":     batches,
		"coverage_exceptions.json": report.Exceptions,
		"report.json":              report,
	} {
		data, err := json.MarshalIndent(value, "", " ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, name), data,
			0o644); err != nil {
			return err
		}
	}
	return nil
}
