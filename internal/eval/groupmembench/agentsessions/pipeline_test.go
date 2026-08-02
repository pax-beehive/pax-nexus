package agentsessions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writePipelineFixture(t *testing.T, dir string) (domainFile, questionsDir string) {
	t.Helper()
	domain := map[string][]map[string]any{"ChannelA": {
		{"msg_node": "Msg_1", "author": "User_1", "role": "Analyst",
			"content": "kickoff", "timestamp": "2025-07-19T01:00:00"},
		{"msg_node": "Msg_2", "author": "User_2", "role": "Officer",
			"content": "the ESG deadline is 2025-07-18", "timestamp": "2025-07-19T02:00:00"},
	}}
	domainFile = filepath.Join(dir, "domain.json")
	data, err := json.Marshal(domain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(domainFile, data, 0o644); err != nil {
		t.Fatal(err)
	}
	questionsDir = filepath.Join(dir, "questions")
	if err := os.MkdirAll(questionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"multi_hop.jsonl":        `{"id":"q1","question":"ESG deadline?","answer":"2025-07-18","asking_user_id":"User_1"}` + "\n",
		"knowledge_update.jsonl": "", "temporal.jsonl": "",
		"term_ambiguity.jsonl": "", "user_implicit.jsonl": "",
		"abstention.jsonl": `{"id":"a1","question":"?","answer":"Unknown","asking_user_id":"User_1"}` + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(questionsDir, name),
			[]byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return domainFile, questionsDir
}

func TestRunPipelineEndToEnd(t *testing.T) {
	dir := t.TempDir()
	domainFile, questionsDir := writePipelineFixture(t, dir)
	outDir := filepath.Join(dir, "out")
	client := &cannedClient{content: `{"actions":[{"index":0,"content":"noted"}],` +
		`"evidence_msg_ids":["Msg_2"],"confident":true}`}
	report, err := RunPipeline(context.Background(), PipelineConfig{
		DomainFile: domainFile, QuestionsDir: questionsDir, OutDir: outDir,
		Model: "m", Concurrency: 2, MaxObs: 60, TopK: 5, Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Sessions == 0 || report.Users != 2 {
		t.Fatalf("bad report: %+v", report)
	}
	for _, name := range []string{"sessions.jsonl", "messages.jsonl",
		"questions_enhanced.jsonl", "session_batches.json",
		"coverage_exceptions.json", "report.json"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("missing output %s: %v", name, err)
		}
	}
}
