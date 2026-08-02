package agentsessions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type Persona struct {
	UserID    string `json:"user_id"`
	Role      string `json:"role"`
	Tone      string `json:"tone"`
	Style     string `json:"style"`
	Expertise string `json:"expertise"`
}

type Action struct {
	Type       string   `json:"type"`
	SourceMsgs []string `json:"source_msgs"`
	Freeform   bool     `json:"freeform"`
	Fallback   bool     `json:"fallback"`
	Content    string   `json:"content"`
}

type FillStats struct{ Calls, CacheHits, Fallbacks int }

type Filler struct {
	Client llm.ChatClient
	Model  string
	Cache  *DiskCache
	mu     sync.Mutex
	stats  FillStats
}

func PersonaOf(msgs []Msg, user string) Persona {
	for _, m := range msgs {
		if m.Author == user {
			return Persona{UserID: user, Role: m.Role, Tone: m.Tone,
				Style: m.Style, Expertise: m.Expertise}
		}
	}
	return Persona{UserID: user}
}

type fillResponse struct {
	Actions []struct {
		Index   int    `json:"index"`
		Content string `json:"content"`
	} `json:"actions"`
}

const fillSystemTemplate = `You are the personal work agent of %s (role: %s).
Voice: tone=%s, style=%s, expertise=%s.
You read your principal's team channels and produce work notes.
Reply with JSON only: {"actions":[{"index":<int>,"content":"<text>"}]}.
One entry per requested action index. Content is 1-3 sentences, concrete,
and preserves names, dates, and decisions verbatim from the source.`

func (f *Filler) FillSession(ctx context.Context, w Window, persona Persona, specs []ActionSpec) ([]Action, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	byNode := map[string]Msg{}
	for _, m := range w.Msgs {
		byNode[m.NodeID] = m
	}
	prompt := buildFillPrompt(w, specs, byNode)
	system := fmt.Sprintf(fillSystemTemplate, persona.UserID, persona.Role,
		persona.Tone, persona.Style, persona.Expertise)
	key := CacheKey(w.SessionID(), system+"\x00"+prompt)

	raw, hit := "", false
	if f.Cache != nil {
		raw, hit = f.Cache.Get(key)
	}
	if hit {
		f.bump(func(s *FillStats) { s.CacheHits++ })
	} else {
		f.bump(func(s *FillStats) { s.Calls++ })
		decoded, err := llm.CompleteJSON[fillResponse](ctx, f.Client, llm.ChatRequest{
			Model: f.Model,
			Messages: []llm.ChatMessage{
				{Role: "system", Content: system},
				{Role: "user", Content: prompt},
			},
		}, 2)
		if err != nil {
			return f.fallbackActions(specs, byNode), nil //nolint:nilerr // LLM 失败按设计走模板兜底,不上抛
		}
		encoded, marshalErr := json.Marshal(decoded)
		if marshalErr != nil {
			return nil, marshalErr
		}
		raw = string(encoded)
		if f.Cache != nil {
			if err := f.Cache.Put(key, raw); err != nil {
				return nil, fmt.Errorf("cache put %s: %w", w.SessionID(), err)
			}
		}
	}
	var decoded fillResponse
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return f.fallbackActions(specs, byNode), nil //nolint:nilerr // 缓存 JSON 损坏按设计走模板兜底,不上抛
	}
	contents := map[int]string{}
	for _, a := range decoded.Actions {
		contents[a.Index] = a.Content
	}
	actions := make([]Action, 0, len(specs))
	for i, spec := range specs {
		action := Action{Type: spec.Type, Freeform: spec.Freeform}
		if spec.SourceMsg != "" {
			action.SourceMsgs = []string{spec.SourceMsg}
		}
		if content, ok := contents[i]; ok && strings.TrimSpace(content) != "" {
			action.Content = content
		} else {
			action = f.fallbackAction(spec, byNode)
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func buildFillPrompt(w Window, specs []ActionSpec, byNode map[string]Msg) string {
	var b strings.Builder
	b.WriteString("Channel messages observed today:\n")
	for _, m := range w.Msgs {
		fmt.Fprintf(&b, "[%s | %s | %s in #%s] %s\n",
			m.NodeID, m.At.Format("15:04"), m.Author, m.Channel, m.Content)
	}
	b.WriteString("\nWrite content for these actions:\n")
	for i, s := range specs {
		if s.Freeform {
			fmt.Fprintf(&b, "%d. %s (free choice: note the most important "+
				"remaining fact from today)\n", i, s.Type)
			continue
		}
		fmt.Fprintf(&b, "%d. %s based on %s\n", i, s.Type, s.SourceMsg)
	}
	return b.String()
}

func (f *Filler) fallbackActions(specs []ActionSpec, byNode map[string]Msg) []Action {
	actions := make([]Action, 0, len(specs))
	for _, spec := range specs {
		actions = append(actions, f.fallbackAction(spec, byNode))
	}
	return actions
}

func (f *Filler) fallbackAction(spec ActionSpec, byNode map[string]Msg) Action {
	f.bump(func(s *FillStats) { s.Fallbacks++ })
	action := Action{Type: spec.Type, Freeform: spec.Freeform, Fallback: true}
	if spec.SourceMsg != "" {
		action.SourceMsgs = []string{spec.SourceMsg}
		if m, ok := byNode[spec.SourceMsg]; ok {
			excerpt := m.Content
			if len(excerpt) > 200 {
				excerpt = excerpt[:200]
			}
			action.Content = fmt.Sprintf("Noted from #%s (%s): %s",
				m.Channel, m.Author, excerpt)
			return action
		}
	}
	action.Content = "Reviewed today's channel activity."
	return action
}

func (f *Filler) bump(update func(*FillStats)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	update(&f.stats)
}

func (f *Filler) Stats() FillStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stats
}
