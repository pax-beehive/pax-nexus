package agentsessions

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type EnhancedQuestion struct {
	groupmembench.Question
	Category           string   `json:"category"`
	EvidenceMsgIDs     []string `json:"evidence_msg_ids"`
	EvidenceSessionIDs []string `json:"evidence_session_ids,omitempty"`
	Confidence         string   `json:"confidence"`
}

type anchorVerdict struct {
	EvidenceMsgIDs []string `json:"evidence_msg_ids"`
	Confident      bool     `json:"confident"`
}

const anchorSystemPrompt = `You identify which chat messages contain the
evidence for a question's answer. Reply with JSON only:
{"evidence_msg_ids":["Msg_..."],"confident":true|false}.
Pick only from the provided candidates. Multiple ids are allowed for
multi-hop evidence. If none of the candidates contain the evidence,
return an empty list with confident=false.`

func RecoverAnchors(ctx context.Context, client llm.ChatClient, model string,
	questions map[string][]groupmembench.Question, msgs []Msg, topK int) ([]EnhancedQuestion, error) {
	docs := make(map[string]string, len(msgs))
	byNode := make(map[string]Msg, len(msgs))
	for _, m := range msgs {
		docs[m.NodeID] = m.Content
		byNode[m.NodeID] = m
	}
	index := NewBM25(docs)

	categories := make([]string, 0, len(questions))
	for category := range questions {
		categories = append(categories, category)
	}
	sort.Strings(categories)

	var out []EnhancedQuestion
	for _, category := range categories {
		for _, q := range questions[category] {
			enhanced := EnhancedQuestion{Question: q, Category: category,
				EvidenceMsgIDs: []string{}}
			if category == "abstention" {
				enhanced.Confidence = "none"
				out = append(out, enhanced)
				continue
			}
			candidates := index.TopK(q.Question+" "+q.Answer, topK)
			verdict, err := judgeAnchor(ctx, client, model, q, candidates, byNode)
			switch {
			case err == nil && verdict.Confident && len(verdict.EvidenceMsgIDs) > 0:
				enhanced.Confidence = "high"
				enhanced.EvidenceMsgIDs = verdict.EvidenceMsgIDs
			default:
				enhanced.Confidence = "low"
				if len(candidates) > 0 {
					enhanced.EvidenceMsgIDs = candidates[:1]
				}
			}
			out = append(out, enhanced)
		}
	}
	return out, nil
}

func judgeAnchor(ctx context.Context, client llm.ChatClient, model string,
	q groupmembench.Question, candidates []string, byNode map[string]Msg) (anchorVerdict, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Question (asked by %s): %s\nGold answer: %s\n\nCandidates:\n",
		q.AskingUserID, q.Question, q.Answer)
	for _, id := range candidates {
		fmt.Fprintf(&b, "[%s] %s\n", id, byNode[id].Content)
	}
	allowed := map[string]bool{}
	for _, id := range candidates {
		allowed[id] = true
	}
	return llm.CompleteJSONAs(ctx, client, llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: anchorSystemPrompt},
			{Role: "user", Content: b.String()},
		},
	}, 2, func(v anchorVerdict) (anchorVerdict, error) {
		for _, id := range v.EvidenceMsgIDs {
			if !allowed[id] {
				return anchorVerdict{}, fmt.Errorf("evidence %s not in candidates", id)
			}
		}
		return v, nil
	})
}
