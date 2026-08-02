# GroupMemBench Agent Sessions 数据集管线 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 GroupMemBench Finance 域群聊数据加工成 per-user agent 工作 session 数据集(规则骨架 + deepseek-v4-flash 填内容),附 anchor 恢复、SessionBatch 转换器与覆盖率验证。

**Architecture:** 新包 `internal/eval/groupmembench/agentsessions/` 承载全部业务逻辑(normalize → visibility/window → skeleton → anchor → fill → assemble → convert → verify),`cmd/groupmembench-sessions/` 是薄 CLI。LLM 调用经 RetryingChatClient(退避+熔断)包装后走现有 `llm.CompleteJSON` 护栏,响应按 `(session_id, prompt_hash)` 落盘缓存。

**Tech Stack:** Go(仓库既有栈),`internal/platform/llm`(DeepSeek 客户端 + JSON 护栏),`internal/eval/groupmembench`(已有 loader),`internal/session`(ingest 契约)。标准库 testing。

**Spec:** `docs/superpowers/specs/2026-08-01-groupmembench-agent-sessions-design.md`

**范围说明:** spec §8.4 的 `opencode-replay` split 依赖 `evals/opencode/` docker 与 paxm 捕获链路,是独立子系统,**不在本计划内**,另出第二个计划。

## Global Constraints

- 模块路径 `github.com/pax-beehive/pax-nexus`;新代码 gofmt,表驱动测试,注释风格与包内既有代码一致。
- 规则层零随机:任何顺序都由 (timestamp, msg_node) 决定;LLM 层随机性被缓存吸收。
- 每 session 动作上限 **8**;observation 上限 **60**;并发默认 **28**(24-32 区间中值);LLM 重试最多 **5** 次传输重试 + **2** 次 JSON 尝试(CompleteJSON attempts=2);熔断阈值:窗口 50 个样本、失败率 >20%、开路 5 分钟。
- 时间戳格式 `2006-01-02T15:04:05`(数据无时区,按 UTC 解析)。
- session_id 格式:`<user>/<YYYY-MM-DD>/s<N>`,N 从 1 起。
- 动作类型字符串:`memory_write`、`draft_reply`、`todo`(freeform 动作也落在这三类之一,仅 `freeform: true` 标记)。
- 提交信息用中文,风格 `feat(eval): ...` / `test(eval): ...`,尾行 `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`。
- 每个 Task 结束跑 `go build ./... && go vet ./...` 通过再提交。

---

### Task 1: 扩展 groupmembench.Message 持久化 persona/message_type 字段

**Files:**
- Modify: `internal/eval/groupmembench/selector.go:31-45`(Message struct)
- Test: `internal/eval/groupmembench/selector_message_test.go`(新建)

**Interfaces:**
- Consumes: 现有 `groupmembench.Message`(json tag 见 selector.go:31)。
- Produces: `Message` 新增字段 `Tone, Style, Expertise, MessageType string`(json: `tone`,`style`,`expertise`,`message_type`),后续任务用它们构造 Persona 与骨架规则。

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/selector_message_test.go
package groupmembench

import (
	"encoding/json"
	"testing"
)

func TestMessageDecodesPersonaFields(t *testing.T) {
	raw := `{"msg_node":"Msg_1","author":"User_1","role":"Business Analyst",
"content":"x","timestamp":"2025-07-19T00:14:03","tone":"anxious",
"style":"anecdotal","expertise":"expert","message_type":"post"}`
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Tone != "anxious" || m.Style != "anecdotal" ||
		m.Expertise != "expert" || m.MessageType != "post" {
		t.Fatalf("persona fields not decoded: %+v", m)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/ -run TestMessageDecodesPersonaFields -v`
Expected: 编译失败 `m.Tone undefined`

- [ ] **Step 3: 在 Message struct 里加四个字段**

在 `selector.go` 的 `Message` struct 中 `IsNoise` 之前插入:

```go
	Tone        string `json:"tone,omitempty"`
	Style       string `json:"style,omitempty"`
	Expertise   string `json:"expertise,omitempty"`
	MessageType string `json:"message_type,omitempty"`
```

- [ ] **Step 4: 跑测试确认通过,且包内既有测试不回归**

Run: `go test ./internal/eval/groupmembench/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/
git commit -m "feat(eval): GroupMemBench Message 增加 persona 与 message_type 字段"
```

---

### Task 2: agentsessions 包 — 归一化(解析、排序、reply 因果夹逼)

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/normalize.go`
- Test: `internal/eval/groupmembench/agentsessions/normalize_test.go`

**Interfaces:**
- Consumes: `groupmembench.Message`(Task 1 之后的形态)、`groupmembench.LoadConversation(path)`。
- Produces:

```go
package agentsessions

type Msg struct {
	groupmembench.Message
	At time.Time
}

// Normalize 解析时间戳、全局按 (At, NodeID) 排序;reply 早于父消息时把
// At 夹逼为父消息时间并记录违例。时间戳不可解析返回 error。
func Normalize(messages []groupmembench.Message) (msgs []Msg, violations []string, err error)
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/normalize_test.go
package agentsessions

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func msg(node, ts, replyTo string) groupmembench.Message {
	return groupmembench.Message{NodeID: node, Timestamp: ts, ReplyTo: replyTo,
		Author: "User_1", Channel: "C", Content: "x"}
}

func TestNormalizeSortsByTimestampThenNode(t *testing.T) {
	out, _, err := Normalize([]groupmembench.Message{
		msg("Msg_2", "2025-07-19T00:20:00", ""),
		msg("Msg_1", "2025-07-19T00:10:00", ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].NodeID != "Msg_1" || out[1].NodeID != "Msg_2" {
		t.Fatalf("wrong order: %s, %s", out[0].NodeID, out[1].NodeID)
	}
}

func TestNormalizeClampsReplyBeforeParent(t *testing.T) {
	out, violations, err := Normalize([]groupmembench.Message{
		msg("Msg_p", "2025-07-19T01:00:00", ""),
		msg("Msg_c", "2025-07-19T00:30:00", "Msg_p"), // reply 早于父消息
	})
	if err != nil {
		t.Fatal(err)
	}
	byNode := map[string]Msg{}
	for _, m := range out {
		byNode[m.NodeID] = m
	}
	if !byNode["Msg_c"].At.Equal(byNode["Msg_p"].At) {
		t.Fatalf("child not clamped: %v vs %v", byNode["Msg_c"].At, byNode["Msg_p"].At)
	}
	if len(violations) != 1 {
		t.Fatalf("want 1 violation, got %d", len(violations))
	}
}

func TestNormalizeRejectsBadTimestamp(t *testing.T) {
	_, _, err := Normalize([]groupmembench.Message{msg("Msg_1", "not-a-time", "")})
	if err == nil {
		t.Fatal("want error for bad timestamp")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -v`
Expected: 编译失败(包不存在)

- [ ] **Step 3: 实现 normalize.go**

```go
// Package agentsessions 把 GroupMemBench 群聊重组为 per-user agent 工作
// session:规则骨架决定动作,LLM 只负责动作的自然语言内容。
package agentsessions

import (
	"fmt"
	"sort"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

const timestampLayout = "2006-01-02T15:04:05"

type Msg struct {
	groupmembench.Message
	At time.Time
}

func Normalize(messages []groupmembench.Message) ([]Msg, []string, error) {
	msgs := make([]Msg, 0, len(messages))
	for _, m := range messages {
		at, err := time.ParseInLocation(timestampLayout, m.Timestamp, time.UTC)
		if err != nil {
			return nil, nil, fmt.Errorf("parse timestamp of %s: %w", m.NodeID, err)
		}
		msgs = append(msgs, Msg{Message: m, At: at})
	}
	byNode := make(map[string]int, len(msgs))
	for i, m := range msgs {
		byNode[m.NodeID] = i
	}
	var violations []string
	for i, m := range msgs {
		if m.ReplyTo == "" {
			continue
		}
		parent, ok := byNode[m.ReplyTo]
		if !ok {
			violations = append(violations,
				fmt.Sprintf("%s: reply_to %s not found", m.NodeID, m.ReplyTo))
			continue
		}
		if m.At.Before(msgs[parent].At) {
			violations = append(violations, fmt.Sprintf(
				"%s: reply at %s before parent %s at %s, clamped",
				m.NodeID, m.At, m.ReplyTo, msgs[parent].At))
			msgs[i].At = msgs[parent].At
		}
	}
	sort.Slice(msgs, func(i, j int) bool {
		if !msgs[i].At.Equal(msgs[j].At) {
			return msgs[i].At.Before(msgs[j].At)
		}
		return msgs[i].NodeID < msgs[j].NodeID
	})
	return msgs, violations, nil
}
```

注意:夹逼在排序前完成,父消息若自身也被夹逼,链式情况按输入序一轮即可
(测试只要求一层;链式 reply 在真实数据中先记违例,不追求闭包)。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions 归一化——排序与 reply 因果夹逼"
```

---

### Task 3: 可见性解析与 session 切窗

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/window.go`
- Test: `internal/eval/groupmembench/agentsessions/window_test.go`

**Interfaces:**
- Consumes: `Msg`(Task 2)。
- Produces:

```go
// UserChannels: user → 其发过 ≥1 条消息的 channel(排序去重)。
func UserChannels(msgs []Msg) map[string][]string

// VisibleTo: 按可见 channel 过滤(msgs 已全局有序,结果保持有序)。
func VisibleTo(msgs []Msg, channels []string) []Msg

type Window struct {
	User string
	Date string // "2025-07-19"
	Part int    // 1 起
	Msgs []Msg
}

func (w Window) SessionID() string // "User_1/2025-07-19/s1"

// Windows: user × 自然日切窗,每窗 observation 上限 maxObs,超量按序拆段。
func Windows(user string, visible []Msg, maxObs int) []Window
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/window_test.go
package agentsessions

import (
	"fmt"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func tmsg(node, channel, author, ts string) Msg {
	m, _, err := Normalize([]groupmembench.Message{{NodeID: node, Channel: channel,
		Author: author, Timestamp: ts, Content: "x"}})
	if err != nil {
		panic(err)
	}
	return m[0]
}

func TestUserChannelsOnlyWhereAuthored(t *testing.T) {
	msgs := []Msg{
		tmsg("Msg_1", "A", "User_1", "2025-07-19T01:00:00"),
		tmsg("Msg_2", "B", "User_2", "2025-07-19T02:00:00"),
	}
	got := UserChannels(msgs)
	if len(got["User_1"]) != 1 || got["User_1"][0] != "A" {
		t.Fatalf("User_1 channels = %v, want [A]", got["User_1"])
	}
}

func TestWindowsSplitByDayAndCap(t *testing.T) {
	var visible []Msg
	for i := 0; i < 3; i++ { // 同一天 3 条,cap=2 → s1(2条)+s2(1条)
		visible = append(visible, tmsg(fmt.Sprintf("Msg_%d", i), "A", "User_1",
			fmt.Sprintf("2025-07-19T0%d:00:00", i)))
	}
	visible = append(visible, tmsg("Msg_9", "A", "User_1", "2025-07-20T01:00:00"))
	wins := Windows("User_1", visible, 2)
	if len(wins) != 3 {
		t.Fatalf("want 3 windows, got %d", len(wins))
	}
	if wins[0].SessionID() != "User_1/2025-07-19/s1" ||
		wins[1].SessionID() != "User_1/2025-07-19/s2" ||
		wins[2].SessionID() != "User_1/2025-07-20/s1" {
		t.Fatalf("bad ids: %s %s %s",
			wins[0].SessionID(), wins[1].SessionID(), wins[2].SessionID())
	}
	if len(wins[0].Msgs) != 2 || len(wins[1].Msgs) != 1 {
		t.Fatal("cap not applied")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run 'TestUserChannels|TestWindows' -v`
Expected: 编译失败

- [ ] **Step 3: 实现 window.go**

```go
package agentsessions

import (
	"fmt"
	"slices"
	"sort"
)

func UserChannels(msgs []Msg) map[string][]string {
	set := map[string]map[string]bool{}
	for _, m := range msgs {
		if set[m.Author] == nil {
			set[m.Author] = map[string]bool{}
		}
		set[m.Author][m.Channel] = true
	}
	out := make(map[string][]string, len(set))
	for user, channels := range set {
		for c := range channels {
			out[user] = append(out[user], c)
		}
		sort.Strings(out[user])
	}
	return out
}

func VisibleTo(msgs []Msg, channels []string) []Msg {
	var out []Msg
	for _, m := range msgs {
		if slices.Contains(channels, m.Channel) {
			out = append(out, m)
		}
	}
	return out
}

type Window struct {
	User string
	Date string
	Part int
	Msgs []Msg
}

func (w Window) SessionID() string {
	return fmt.Sprintf("%s/%s/s%d", w.User, w.Date, w.Part)
}

func Windows(user string, visible []Msg, maxObs int) []Window {
	var wins []Window
	var current *Window
	for _, m := range visible {
		date := m.At.Format("2006-01-02")
		if current == nil || current.Date != date || len(current.Msgs) >= maxObs {
			part := 1
			if current != nil && current.Date == date {
				part = current.Part + 1
			}
			wins = append(wins, Window{User: user, Date: date, Part: part})
			current = &wins[len(wins)-1]
		}
		current.Msgs = append(current.Msgs, m)
	}
	return wins
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions 可见性解析与 user×日切窗"
```

---

### Task 4: 动作骨架规则

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/skeleton.go`
- Test: `internal/eval/groupmembench/agentsessions/skeleton_test.go`

**Interfaces:**
- Consumes: `Window`(Task 3)。
- Produces:

```go
const maxActionsPerSession = 8

type ActionSpec struct {
	Type      string // "memory_write" | "draft_reply" | "todo"
	SourceMsg string // 触发消息 NodeID;freeform 时为空
	Freeform  bool
	priority  int
}

// BuildSkeleton 按 spec §5 优先级生成动作,cap 8,同一消息只取最高优先级,
// is_noise 消息不触发 1-4。parentAuthor: NodeID → 作者(跨窗全域构建)。
// anchors: NodeID → true 表示该消息是某题证据。
func BuildSkeleton(w Window, parentAuthor map[string]string, anchors map[string]bool) []ActionSpec
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/skeleton_test.go
package agentsessions

import (
	"fmt"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func smsg(node, author, content string, opts func(*groupmembench.Message)) Msg {
	base := groupmembench.Message{NodeID: node, Channel: "A", Author: author,
		Timestamp: "2025-07-19T01:00:00", Content: content}
	if opts != nil {
		opts(&base)
	}
	out, _, err := Normalize([]groupmembench.Message{base})
	if err != nil {
		panic(err)
	}
	return out[0]
}

func TestSkeletonPriorities(t *testing.T) {
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: []Msg{
		smsg("Msg_anchor", "User_2", "the fee cap is 3%", nil),
		smsg("Msg_rev", "User_3", "reversing the earlier call",
			func(m *groupmembench.Message) {
				m.DecisionChangeMetadata = map[string]any{"reversal_of": "Msg_0"}
			}),
		smsg("Msg_mention", "User_4", "User_1 please confirm scope", nil),
		smsg("Msg_todo", "User_5", "assessment due 2025-07-16", nil),
		smsg("Msg_plain", "User_6", "nothing interesting", nil),
	}}
	specs := BuildSkeleton(w, map[string]string{},
		map[string]bool{"Msg_anchor": true})
	want := map[string]string{
		"Msg_anchor":  "memory_write",
		"Msg_rev":     "memory_write",
		"Msg_mention": "draft_reply",
		"Msg_todo":    "todo",
	}
	got := map[string]string{}
	freeform := 0
	for _, s := range specs {
		if s.Freeform {
			freeform++
			continue
		}
		got[s.SourceMsg] = s.Type
	}
	for node, typ := range want {
		if got[node] != typ {
			t.Fatalf("%s: got %q want %q", node, got[node], typ)
		}
	}
	if _, ok := got["Msg_plain"]; ok {
		t.Fatal("plain message should not trigger")
	}
	if freeform != 1 {
		t.Fatalf("want 1 freeform slot, got %d", freeform)
	}
}

func TestSkeletonNoiseAndCap(t *testing.T) {
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1}
	w.Msgs = append(w.Msgs, smsg("Msg_noise", "User_2", "due 2025-07-16",
		func(m *groupmembench.Message) { m.IsNoise = true }))
	for i := 0; i < 10; i++ {
		node := fmt.Sprintf("Msg_a%d", i)
		w.Msgs = append(w.Msgs, smsg(node, "User_2", "evidence", nil))
	}
	anchors := map[string]bool{}
	for i := 0; i < 10; i++ {
		anchors[fmt.Sprintf("Msg_a%d", i)] = true
	}
	specs := BuildSkeleton(w, map[string]string{}, anchors)
	if len(specs) > maxActionsPerSession {
		t.Fatalf("cap exceeded: %d", len(specs))
	}
	for _, s := range specs {
		if s.SourceMsg == "Msg_noise" {
			t.Fatal("noise message triggered an action")
		}
	}
}

func TestSkeletonReplyToUserTriggersDraftReply(t *testing.T) {
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: []Msg{
		smsg("Msg_c", "User_2", "responding to your point",
			func(m *groupmembench.Message) { m.ReplyTo = "Msg_mine" }),
	}}
	specs := BuildSkeleton(w, map[string]string{"Msg_mine": "User_1"}, nil)
	if len(specs) == 0 || specs[0].Type != "draft_reply" || specs[0].SourceMsg != "Msg_c" {
		t.Fatalf("want draft_reply for Msg_c, got %+v", specs)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestSkeleton -v`
Expected: 编译失败

- [ ] **Step 3: 实现 skeleton.go**

```go
package agentsessions

import (
	"regexp"
	"sort"
)

const maxActionsPerSession = 8

type ActionSpec struct {
	Type      string
	SourceMsg string
	Freeform  bool
	priority  int
}

var (
	deadlinePattern = regexp.MustCompile(
		`(?i)\b(deadline|due)\b|\bby \d{4}-\d{2}-\d{2}\b|\b\d{4}-\d{2}-\d{2}\b`)
	userToken = regexp.MustCompile(`\bUser_\d+\b`)
)

func BuildSkeleton(w Window, parentAuthor map[string]string, anchors map[string]bool) []ActionSpec {
	var specs []ActionSpec
	for _, m := range w.Msgs {
		if m.Author == w.User || m.IsNoise {
			continue
		}
		switch {
		case anchors[m.NodeID]:
			specs = append(specs, ActionSpec{Type: "memory_write",
				SourceMsg: m.NodeID, priority: 1})
		case len(m.DecisionChangeMetadata) > 0:
			specs = append(specs, ActionSpec{Type: "memory_write",
				SourceMsg: m.NodeID, priority: 2})
		case mentionsUser(m.Content, w.User) ||
			(m.ReplyTo != "" && parentAuthor[m.ReplyTo] == w.User):
			specs = append(specs, ActionSpec{Type: "draft_reply",
				SourceMsg: m.NodeID, priority: 3})
		case deadlinePattern.MatchString(m.Content):
			specs = append(specs, ActionSpec{Type: "todo",
				SourceMsg: m.NodeID, priority: 4})
		}
	}
	sort.SliceStable(specs, func(i, j int) bool {
		return specs[i].priority < specs[j].priority
	})
	// 留 1 个 freeform 槽位(≈ cap 的 15%),只要有 observation 就给。
	limit := maxActionsPerSession - 1
	if len(specs) > limit {
		specs = specs[:limit]
	}
	if len(w.Msgs) > 0 {
		specs = append(specs, ActionSpec{Type: "memory_write", Freeform: true,
			priority: 5})
	}
	return specs
}

func mentionsUser(content, user string) bool {
	for _, token := range userToken.FindAllString(content, -1) {
		if token == user {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions 确定性动作骨架规则"
```

---

### Task 5: BM25 检索(anchor 恢复的候选生成)

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/bm25.go`
- Test: `internal/eval/groupmembench/agentsessions/bm25_test.go`

**Interfaces:**
- Produces:

```go
// NewBM25 建索引;docs: docID → 文本。k1=1.5, b=0.75,分词为小写字母数字段。
func NewBM25(docs map[string]string) *BM25
// TopK 返回按分数降序的 docID,分数相同按 docID 升序保证确定性。
func (b *BM25) TopK(query string, k int) []string
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/bm25_test.go
package agentsessions

import "testing"

func TestBM25RanksExactTermMatchFirst(t *testing.T) {
	idx := NewBM25(map[string]string{
		"d1": "the ESG policy deadline is 2025-07-18 for reporting",
		"d2": "lunch menu discussion pizza",
		"d3": "reporting cadence weekly sync",
	})
	got := idx.TopK("ESG policy deadline 2025-07-18", 2)
	if len(got) == 0 || got[0] != "d1" {
		t.Fatalf("want d1 first, got %v", got)
	}
}

func TestBM25DeterministicTieBreak(t *testing.T) {
	idx := NewBM25(map[string]string{"b": "same words", "a": "same words"})
	got := idx.TopK("same words", 2)
	if got[0] != "a" || got[1] != "b" {
		t.Fatalf("tie-break not deterministic: %v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestBM25 -v`
Expected: 编译失败

- [ ] **Step 3: 实现 bm25.go**

```go
package agentsessions

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

type BM25 struct {
	docs   map[string][]string // docID → tokens
	df     map[string]int
	avgLen float64
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func NewBM25(docs map[string]string) *BM25 {
	b := &BM25{docs: map[string][]string{}, df: map[string]int{}}
	var total int
	for id, text := range docs {
		tokens := tokenize(text)
		b.docs[id] = tokens
		total += len(tokens)
		seen := map[string]bool{}
		for _, tok := range tokens {
			if !seen[tok] {
				b.df[tok]++
				seen[tok] = true
			}
		}
	}
	if len(docs) > 0 {
		b.avgLen = float64(total) / float64(len(docs))
	}
	return b
}

func (b *BM25) TopK(query string, k int) []string {
	n := float64(len(b.docs))
	type scored struct {
		id    string
		score float64
	}
	var results []scored
	queryTokens := tokenize(query)
	for id, tokens := range b.docs {
		tf := map[string]int{}
		for _, tok := range tokens {
			tf[tok]++
		}
		var score float64
		for _, q := range queryTokens {
			f := float64(tf[q])
			if f == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(b.df[q])+0.5)/(float64(b.df[q])+0.5))
			denom := f + bm25K1*(1-bm25B+bm25B*float64(len(tokens))/b.avgLen)
			score += idf * f * (bm25K1 + 1) / denom
		}
		if score > 0 {
			results = append(results, scored{id, score})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].id < results[j].id
	})
	if len(results) > k {
		results = results[:k]
	}
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.id
	}
	return out
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestBM25 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions 内置 BM25 检索"
```

---

### Task 6: RetryingChatClient(退避 + 抖动 + 熔断)

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/retry.go`
- Test: `internal/eval/groupmembench/agentsessions/retry_test.go`

**Interfaces:**
- Consumes: `llm.ChatClient`(`internal/platform/llm/chat.go`)。
- Produces:

```go
// RetryingChatClient 实现 llm.ChatClient。传输错误按指数退避+全抖动重试
// (base 1s, cap 60s, 最多 MaxAttempts 次);滑动窗口 50 个样本中失败率
// >20%(且样本 ≥10)时开路 5 分钟。Sleep/Rand/Now 可注入以便测试。
type RetryingChatClient struct {
	Inner       llm.ChatClient
	MaxAttempts int                  // 0 → 5
	Sleep       func(time.Duration)  // nil → time.Sleep
	Rand        func() float64       // nil → rand.Float64
	Now         func() time.Time     // nil → time.Now
	// 内部熔断状态,零值可用
	mu        sync.Mutex
	outcomes  []bool // 最近 50 次,true=失败
	openUntil time.Time
}

var ErrCircuitOpen = errors.New("llm circuit open")

func (r *RetryingChatClient) Complete(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/retry_test.go
package agentsessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type scriptedClient struct {
	errs  []error // 每次调用弹出一个;nil 表示成功
	calls int
}

func (s *scriptedClient) Complete(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	s.calls++
	if len(s.errs) == 0 {
		return llm.ChatResponse{Message: llm.ChatMessage{Content: "ok"}}, nil
	}
	err := s.errs[0]
	s.errs = s.errs[1:]
	if err == nil {
		return llm.ChatResponse{Message: llm.ChatMessage{Content: "ok"}}, nil
	}
	return llm.ChatResponse{}, err
}

func newTestRetrier(inner llm.ChatClient) (*RetryingChatClient, *[]time.Duration) {
	var slept []time.Duration
	now := time.Unix(0, 0)
	r := &RetryingChatClient{Inner: inner,
		Sleep: func(d time.Duration) { slept = append(slept, d); now = now.Add(d) },
		Rand:  func() float64 { return 1.0 },
		Now:   func() time.Time { return now }}
	return r, &slept
}

func TestRetrySucceedsAfterTransientErrors(t *testing.T) {
	inner := &scriptedClient{errs: []error{errors.New("timeout"),
		errors.New("status 503"), nil}}
	r, slept := newTestRetrier(inner)
	resp, err := r.Complete(context.Background(), llm.ChatRequest{})
	if err != nil || resp.Message.Content != "ok" {
		t.Fatalf("want ok, got %v %v", resp, err)
	}
	if inner.calls != 3 || len(*slept) != 2 {
		t.Fatalf("calls=%d sleeps=%d", inner.calls, len(*slept))
	}
	if (*slept)[0] != 1*time.Second || (*slept)[1] != 2*time.Second {
		t.Fatalf("backoff wrong: %v", *slept) // Rand=1.0 → 全额退避 1s, 2s
	}
}

func TestRetryGivesUpAfterMaxAttempts(t *testing.T) {
	inner := &scriptedClient{errs: []error{errors.New("e"), errors.New("e"),
		errors.New("e"), errors.New("e"), errors.New("e")}}
	r, _ := newTestRetrier(inner)
	_, err := r.Complete(context.Background(), llm.ChatRequest{})
	if err == nil || inner.calls != 5 {
		t.Fatalf("want failure after 5 attempts, calls=%d err=%v", inner.calls, err)
	}
}

func TestCircuitOpensAndRecovers(t *testing.T) {
	inner := &scriptedClient{}
	r, _ := newTestRetrier(inner)
	r.MaxAttempts = 1
	// 10 个样本里 3 个失败 → 30% > 20% → 开路
	for i := 0; i < 7; i++ {
		if _, err := r.Complete(context.Background(), llm.ChatRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		inner.errs = []error{errors.New("boom")}
		r.Complete(context.Background(), llm.ChatRequest{}) //nolint:errcheck
	}
	if _, err := r.Complete(context.Background(), llm.ChatRequest{}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("want ErrCircuitOpen, got %v", err)
	}
	// 5 分钟后半开恢复
	base := r.Now()
	r.Now = func() time.Time { return base.Add(5*time.Minute + time.Second) }
	if _, err := r.Complete(context.Background(), llm.ChatRequest{}); err != nil {
		t.Fatalf("want recovery after open window, got %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run 'TestRetry|TestCircuit' -v`
Expected: 编译失败

- [ ] **Step 3: 实现 retry.go**

```go
package agentsessions

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

const (
	retryBaseDelay      = 1 * time.Second
	retryMaxDelay       = 60 * time.Second
	defaultMaxAttempts  = 5
	breakerWindow       = 50
	breakerMinSamples   = 10
	breakerFailureRate  = 0.20
	breakerOpenDuration = 5 * time.Minute
)

var ErrCircuitOpen = errors.New("llm circuit open")

type RetryingChatClient struct {
	Inner       llm.ChatClient
	MaxAttempts int
	Sleep       func(time.Duration)
	Rand        func() float64
	Now         func() time.Time

	mu        sync.Mutex
	outcomes  []bool
	openUntil time.Time
}

func (r *RetryingChatClient) Complete(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	sleep, random, now := r.Sleep, r.Rand, r.Now
	if sleep == nil {
		sleep = time.Sleep
	}
	if random == nil {
		random = rand.Float64
	}
	if now == nil {
		now = time.Now
	}
	attempts := r.MaxAttempts
	if attempts <= 0 {
		attempts = defaultMaxAttempts
	}
	if err := r.checkBreaker(now()); err != nil {
		return llm.ChatResponse{}, err
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay << (attempt - 1)
			if delay > retryMaxDelay {
				delay = retryMaxDelay
			}
			sleep(time.Duration(random() * float64(delay))) // 全抖动
		}
		resp, err := r.Inner.Complete(ctx, req)
		r.record(err != nil, now())
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return llm.ChatResponse{}, lastErr
}

func (r *RetryingChatClient) checkBreaker(now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Before(r.openUntil) {
		return ErrCircuitOpen
	}
	return nil
}

func (r *RetryingChatClient) record(failed bool, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes = append(r.outcomes, failed)
	if len(r.outcomes) > breakerWindow {
		r.outcomes = r.outcomes[len(r.outcomes)-breakerWindow:]
	}
	if len(r.outcomes) < breakerMinSamples {
		return
	}
	failures := 0
	for _, f := range r.outcomes {
		if f {
			failures++
		}
	}
	if float64(failures)/float64(len(r.outcomes)) > breakerFailureRate {
		r.openUntil = now.Add(breakerOpenDuration)
		r.outcomes = nil // 半开:窗口清零重新计数
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -race -run 'TestRetry|TestCircuit' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions LLM 重试客户端——指数退避+全抖动+熔断"
```

---

### Task 7: 磁盘缓存(断点续跑)

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/cache.go`
- Test: `internal/eval/groupmembench/agentsessions/cache_test.go`

**Interfaces:**
- Produces:

```go
type DiskCache struct{ Dir string } // Dir 为空 → 全部 miss、Put 为 no-op

// Key = hex(sha256(scope + "\x00" + prompt))
func CacheKey(scope, prompt string) string
func (c *DiskCache) Get(key string) (string, bool)
func (c *DiskCache) Put(key, value string) error
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/cache_test.go
package agentsessions

import "testing"

func TestDiskCacheRoundTrip(t *testing.T) {
	c := &DiskCache{Dir: t.TempDir()}
	key := CacheKey("User_1/2025-07-19/s1", "prompt text")
	if _, ok := c.Get(key); ok {
		t.Fatal("want miss before put")
	}
	if err := c.Put(key, `{"actions":[]}`); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get(key)
	if !ok || got != `{"actions":[]}` {
		t.Fatalf("want hit, got %q ok=%v", got, ok)
	}
}

func TestDiskCacheKeyChangesWithPrompt(t *testing.T) {
	if CacheKey("s", "p1") == CacheKey("s", "p2") {
		t.Fatal("key must depend on prompt")
	}
}

func TestDiskCacheEmptyDirIsNoop(t *testing.T) {
	c := &DiskCache{}
	if err := c.Put("k", "v"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("empty-dir cache must miss")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestDiskCache -v`
Expected: 编译失败

- [ ] **Step 3: 实现 cache.go**

```go
package agentsessions

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

type DiskCache struct{ Dir string }

func CacheKey(scope, prompt string) string {
	sum := sha256.Sum256([]byte(scope + "\x00" + prompt))
	return hex.EncodeToString(sum[:])
}

func (c *DiskCache) path(key string) string {
	return filepath.Join(c.Dir, key+".json")
}

func (c *DiskCache) Get(key string) (string, bool) {
	if c.Dir == "" {
		return "", false
	}
	data, err := os.ReadFile(c.path(key))
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (c *DiskCache) Put(key, value string) error {
	if c.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	tmp := c.path(key) + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path(key))
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestDiskCache -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions 磁盘缓存支持断点续跑"
```

---

### Task 8: LLM 内容填充(prompt 构造、缓存、模板兜底、统计)

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/fill.go`
- Test: `internal/eval/groupmembench/agentsessions/fill_test.go`

**Interfaces:**
- Consumes: `llm.ChatClient`、`llm.CompleteJSON`、`DiskCache`(Task 7)、`Window`/`ActionSpec`(Task 3/4)。
- Produces:

```go
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

type FillStats struct {
	Calls, CacheHits, Fallbacks int
}

type Filler struct {
	Client llm.ChatClient // 已经过 RetryingChatClient 包装
	Model  string
	Cache  *DiskCache
	mu     sync.Mutex
	stats  FillStats
}

// PersonaOf 从消息流提取某 user 的 persona(取其第一条消息的字段)。
func PersonaOf(msgs []Msg, user string) Persona

// FillSession:一次 LLM 调用为整窗骨架填内容(CompleteJSON attempts=2);
// 缓存命中跳过调用;失败时逐动作模板兜底(Fallback=true),永不返回 error
// 以外的中断——error 仅在缓存写失败等本地 IO 问题时出现。
func (f *Filler) FillSession(ctx context.Context, w Window, persona Persona, specs []ActionSpec) ([]Action, error)
func (f *Filler) Stats() FillStats
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/fill_test.go
package agentsessions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

type cannedClient struct {
	content string
	err     error
	calls   int
	lastReq llm.ChatRequest
}

func (c *cannedClient) Complete(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	c.calls++
	c.lastReq = req
	if c.err != nil {
		return llm.ChatResponse{}, c.err
	}
	return llm.ChatResponse{Message: llm.ChatMessage{Content: c.content},
		FinishReason: "stop"}, nil
}

func fillWindow() (Window, []ActionSpec) {
	m, _, _ := Normalize([]groupmembench.Message{{NodeID: "Msg_1", Channel: "A",
		Author: "User_2", Timestamp: "2025-07-19T01:00:00",
		Content: "assessment due 2025-07-16, User_7 owns it"}})
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: m}
	specs := []ActionSpec{{Type: "memory_write", SourceMsg: "Msg_1"}}
	return w, specs
}

func TestFillSessionParsesActions(t *testing.T) {
	client := &cannedClient{
		content: `{"actions":[{"index":0,"content":"User_7 owns the assessment, due 2025-07-16"}]}`}
	f := &Filler{Client: client, Model: "deepseek-v4-flash"}
	w, specs := fillWindow()
	actions, err := f.FillSession(context.Background(), w, Persona{UserID: "User_1"}, specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Fallback ||
		actions[0].Content != "User_7 owns the assessment, due 2025-07-16" ||
		actions[0].SourceMsgs[0] != "Msg_1" {
		t.Fatalf("bad actions: %+v", actions)
	}
	if client.lastReq.Model != "deepseek-v4-flash" {
		t.Fatalf("model not set: %q", client.lastReq.Model)
	}
	if !strings.Contains(client.lastReq.Messages[1].Content, "Msg_1") {
		t.Fatal("prompt must include source message")
	}
}

func TestFillSessionFallsBackOnFailure(t *testing.T) {
	f := &Filler{Client: &cannedClient{err: errors.New("boom")}, Model: "m"}
	w, specs := fillWindow()
	actions, err := f.FillSession(context.Background(), w, Persona{UserID: "User_1"}, specs)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || !actions[0].Fallback ||
		!strings.Contains(actions[0].Content, "assessment due 2025-07-16") {
		t.Fatalf("fallback missing source excerpt: %+v", actions)
	}
	if f.Stats().Fallbacks != 1 {
		t.Fatalf("stats: %+v", f.Stats())
	}
}

func TestFillSessionUsesCache(t *testing.T) {
	client := &cannedClient{content: `{"actions":[{"index":0,"content":"cached"}]}`}
	f := &Filler{Client: client, Model: "m", Cache: &DiskCache{Dir: t.TempDir()}}
	w, specs := fillWindow()
	if _, err := f.FillSession(context.Background(), w, Persona{}, specs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.FillSession(context.Background(), w, Persona{}, specs); err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 {
		t.Fatalf("cache not used, calls=%d", client.calls)
	}
	if f.Stats().CacheHits != 1 {
		t.Fatalf("stats: %+v", f.Stats())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestFill -v`
Expected: 编译失败

- [ ] **Step 3: 实现 fill.go**

```go
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
			return f.fallbackActions(specs, byNode), nil
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
		return f.fallbackActions(specs, byNode), nil
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
```

- [ ] **Step 4: 跑测试确认通过(含 -race)**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -race -run TestFill -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions LLM 填内容——缓存、JSON 护栏、模板兜底"
```

---

### Task 9: anchor 恢复(BM25 候选 + LLM 判定 + 增强问题集)

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/anchor.go`
- Test: `internal/eval/groupmembench/agentsessions/anchor_test.go`

**Interfaces:**
- Consumes: `BM25`(Task 5)、`llm.CompleteJSONAs`、`groupmembench.Question`。
- Produces:

```go
type EnhancedQuestion struct {
	groupmembench.Question
	Category           string   `json:"category"`
	EvidenceMsgIDs     []string `json:"evidence_msg_ids"`
	EvidenceSessionIDs []string `json:"evidence_session_ids,omitempty"` // Task 10 填充
	Confidence         string   `json:"confidence"` // "high" | "low" | "none"
}

// RecoverAnchors:非拒答题走 BM25 top-k 候选 + LLM 判定;abstention 类
// 直接 Confidence="none"、证据为空。判定失败(重试耗尽)→ Confidence="low"、
// 证据取 BM25 top-1,留给人工核对。
func RecoverAnchors(ctx context.Context, client llm.ChatClient, model string,
	questions map[string][]groupmembench.Question, msgs []Msg, topK int) ([]EnhancedQuestion, error)
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/anchor_test.go
package agentsessions

import (
	"context"
	"errors"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func anchorMsgs() []Msg {
	out, _, err := Normalize([]groupmembench.Message{
		{NodeID: "Msg_1", Channel: "A", Author: "User_2",
			Timestamp: "2025-07-19T01:00:00",
			Content: "the ESG policy deadline is 2025-07-18 for reporting"},
		{NodeID: "Msg_2", Channel: "A", Author: "User_3",
			Timestamp: "2025-07-19T02:00:00", Content: "pizza friday"},
	})
	if err != nil {
		panic(err)
	}
	return out
}

func TestRecoverAnchorsBindsEvidence(t *testing.T) {
	client := &cannedClient{content: `{"evidence_msg_ids":["Msg_1"],"confident":true}`}
	questions := map[string][]groupmembench.Question{
		"multi_hop": {{ID: "q1", Question: "ESG deadline?", Answer: "2025-07-18",
			AskingUserID: "User_7"}},
	}
	got, err := RecoverAnchors(context.Background(), client, "m", questions,
		anchorMsgs(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Confidence != "high" ||
		len(got[0].EvidenceMsgIDs) != 1 || got[0].EvidenceMsgIDs[0] != "Msg_1" ||
		got[0].Category != "multi_hop" {
		t.Fatalf("bad result: %+v", got)
	}
}

func TestRecoverAnchorsSkipsAbstention(t *testing.T) {
	client := &cannedClient{}
	questions := map[string][]groupmembench.Question{
		"abstention": {{ID: "a1", Question: "unknown?", Answer: "Unknown",
			AskingUserID: "User_1"}},
	}
	got, err := RecoverAnchors(context.Background(), client, "m", questions,
		anchorMsgs(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 0 {
		t.Fatal("abstention must not call LLM")
	}
	if got[0].Confidence != "none" || len(got[0].EvidenceMsgIDs) != 0 {
		t.Fatalf("bad abstention result: %+v", got)
	}
}

func TestRecoverAnchorsLowConfidenceOnLLMFailure(t *testing.T) {
	client := &cannedClient{err: errors.New("down")}
	questions := map[string][]groupmembench.Question{
		"temporal": {{ID: "t1", Question: "ESG deadline?", Answer: "2025-07-18",
			AskingUserID: "User_7"}},
	}
	got, err := RecoverAnchors(context.Background(), client, "m", questions,
		anchorMsgs(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Confidence != "low" || len(got[0].EvidenceMsgIDs) != 1 ||
		got[0].EvidenceMsgIDs[0] != "Msg_1" {
		t.Fatalf("want low-confidence BM25 top-1 fallback, got %+v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestRecoverAnchors -v`
Expected: 编译失败

- [ ] **Step 3: 实现 anchor.go**

```go
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestRecoverAnchors -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions anchor 恢复——BM25 候选+LLM 判定"
```

---

### Task 10: session 组装与 JSONL 输出(sessions/messages/questions)

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/assemble.go`
- Test: `internal/eval/groupmembench/agentsessions/assemble_test.go`

**Interfaces:**
- Consumes: `Window`/`Persona`/`Action`/`EnhancedQuestion`(Task 3/8/9)。
- Produces:

```go
type Observation struct {
	Channel   string `json:"channel"`
	MsgNode   string `json:"msg_node"`
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
}

type Session struct {
	SessionID   string        `json:"session_id"`
	Agent       Persona       `json:"agent"`
	WindowStart string        `json:"window_start"`
	WindowEnd   string        `json:"window_end"`
	Observations []Observation `json:"observations"`
	Trajectory  []Action      `json:"trajectory"`
}

func BuildSession(w Window, persona Persona, actions []Action) Session

// AttachEvidenceSessions 把每题 EvidenceMsgIDs 映射为提问者 session 的
// EvidenceSessionIDs(该 user 的 session observations 中包含证据消息者)。
func AttachEvidenceSessions(questions []EnhancedQuestion, sessions []Session) []EnhancedQuestion

// WriteJSONL 把任意 slice 逐行 json.Marshal 写文件。
func WriteJSONL[T any](path string, rows []T) error

// MessageRow / WriteMessages: 全量消息原文单独落盘。
type MessageRow struct {
	MsgNode string `json:"msg_node"`
	Channel string `json:"channel"`
	Author  string `json:"author"`
	Role    string `json:"role"`
	Timestamp string `json:"timestamp"`
	ReplyTo string `json:"reply_to,omitempty"`
	IsNoise bool   `json:"is_noise"`
	Content string `json:"content"`
}
func MessageRows(msgs []Msg) []MessageRow
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/assemble_test.go
package agentsessions

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func TestBuildSessionShapesOutput(t *testing.T) {
	m, _, _ := Normalize([]groupmembench.Message{{NodeID: "Msg_1", Channel: "A",
		Author: "User_2", Timestamp: "2025-07-19T01:00:00", Content: "x"}})
	w := Window{User: "User_1", Date: "2025-07-19", Part: 1, Msgs: m}
	s := BuildSession(w, Persona{UserID: "User_1", Role: "Business Analyst"},
		[]Action{{Type: "memory_write", SourceMsgs: []string{"Msg_1"}, Content: "n"}})
	if s.SessionID != "User_1/2025-07-19/s1" ||
		len(s.Observations) != 1 || s.Observations[0].MsgNode != "Msg_1" ||
		len(s.Trajectory) != 1 {
		t.Fatalf("bad session: %+v", s)
	}
}

func TestAttachEvidenceSessions(t *testing.T) {
	sessions := []Session{
		{SessionID: "User_7/2025-07-19/s1", Agent: Persona{UserID: "User_7"},
			Observations: []Observation{{MsgNode: "Msg_1"}}},
		{SessionID: "User_2/2025-07-19/s1", Agent: Persona{UserID: "User_2"},
			Observations: []Observation{{MsgNode: "Msg_1"}}},
	}
	questions := []EnhancedQuestion{{
		Question: groupmembench.Question{ID: "q1", AskingUserID: "User_7"},
		Category: "multi_hop", EvidenceMsgIDs: []string{"Msg_1"}}}
	got := AttachEvidenceSessions(questions, sessions)
	if len(got[0].EvidenceSessionIDs) != 1 ||
		got[0].EvidenceSessionIDs[0] != "User_7/2025-07-19/s1" {
		t.Fatalf("want asker session only, got %v", got[0].EvidenceSessionIDs)
	}
}

func TestWriteJSONLRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	if err := WriteJSONL(path, []MessageRow{{MsgNode: "Msg_1", Content: "a"},
		{MsgNode: "Msg_2", Content: "b"}}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var count int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row MessageRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("want 2 rows, got %d", count)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run 'TestBuildSession|TestAttachEvidence|TestWriteJSONL' -v`
Expected: 编译失败

- [ ] **Step 3: 实现 assemble.go**

```go
package agentsessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Observation struct {
	Channel   string `json:"channel"`
	MsgNode   string `json:"msg_node"`
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
}

type Session struct {
	SessionID    string        `json:"session_id"`
	Agent        Persona       `json:"agent"`
	WindowStart  string        `json:"window_start"`
	WindowEnd    string        `json:"window_end"`
	Observations []Observation `json:"observations"`
	Trajectory   []Action      `json:"trajectory"`
}

func BuildSession(w Window, persona Persona, actions []Action) Session {
	s := Session{SessionID: w.SessionID(), Agent: persona, Trajectory: actions}
	for i, m := range w.Msgs {
		if i == 0 {
			s.WindowStart = m.At.Format(timestampLayout)
		}
		s.WindowEnd = m.At.Format(timestampLayout)
		s.Observations = append(s.Observations, Observation{Channel: m.Channel,
			MsgNode: m.NodeID, Author: m.Author,
			Timestamp: m.At.Format(timestampLayout)})
	}
	return s
}

func AttachEvidenceSessions(questions []EnhancedQuestion, sessions []Session) []EnhancedQuestion {
	// user → msg_node → session ids(保持 session 序)
	seen := map[string]map[string][]string{}
	for _, s := range sessions {
		user := s.Agent.UserID
		if seen[user] == nil {
			seen[user] = map[string][]string{}
		}
		for _, o := range s.Observations {
			seen[user][o.MsgNode] = append(seen[user][o.MsgNode], s.SessionID)
		}
	}
	out := make([]EnhancedQuestion, len(questions))
	for i, q := range questions {
		q.EvidenceSessionIDs = nil
		unique := map[string]bool{}
		for _, node := range q.EvidenceMsgIDs {
			for _, id := range seen[q.AskingUserID][node] {
				if !unique[id] {
					unique[id] = true
					q.EvidenceSessionIDs = append(q.EvidenceSessionIDs, id)
				}
			}
		}
		out[i] = q
	}
	return out
}

func WriteJSONL[T any](path string, rows []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	encoder := json.NewEncoder(w)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return fmt.Errorf("encode %s: %w", path, err)
		}
	}
	return w.Flush()
}

type MessageRow struct {
	MsgNode   string `json:"msg_node"`
	Channel   string `json:"channel"`
	Author    string `json:"author"`
	Role      string `json:"role"`
	Timestamp string `json:"timestamp"`
	ReplyTo   string `json:"reply_to,omitempty"`
	IsNoise   bool   `json:"is_noise"`
	Content   string `json:"content"`
}

func MessageRows(msgs []Msg) []MessageRow {
	rows := make([]MessageRow, 0, len(msgs))
	for _, m := range msgs {
		rows = append(rows, MessageRow{MsgNode: m.NodeID, Channel: m.Channel,
			Author: m.Author, Role: m.Role,
			Timestamp: m.At.Format(timestampLayout), ReplyTo: m.ReplyTo,
			IsNoise: m.IsNoise, Content: m.Content})
	}
	return rows
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions session 组装与 JSONL 输出"
```

---

### Task 11: SessionBatch 转换器

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/convert.go`
- Test: `internal/eval/groupmembench/agentsessions/convert_test.go`

**Interfaces:**
- Consumes: `Session`(Task 10)、`session.SessionBatch`/`session.SessionEvent`/`session.Actor`(`internal/session/contracts.go`)。
- Produces:

```go
// ToSessionBatch:observation → type "observation" 事件(content 含
// channel/author/原文),动作 → type 同动作类型的事件;sequence 递增,
// occurred_at 用 observation 时间戳/窗末时间;actor = {user, agent_id
// "groupmembench-agent", session_id}。msgText: msg_node → 原文。
func ToSessionBatch(s Session, msgText map[string]string) (session.SessionBatch, error)
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/convert_test.go
package agentsessions

import (
	"strings"
	"testing"
)

func TestToSessionBatchShape(t *testing.T) {
	s := Session{
		SessionID:   "User_1/2025-07-19/s1",
		Agent:       Persona{UserID: "User_1"},
		WindowStart: "2025-07-19T01:00:00",
		WindowEnd:   "2025-07-19T02:00:00",
		Observations: []Observation{{Channel: "A", MsgNode: "Msg_1",
			Author: "User_2", Timestamp: "2025-07-19T01:00:00"}},
		Trajectory: []Action{{Type: "memory_write",
			SourceMsgs: []string{"Msg_1"}, Content: "note"}},
	}
	batch, err := ToSessionBatch(s, map[string]string{"Msg_1": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !batch.Complete || len(batch.Events) != 2 {
		t.Fatalf("want complete batch with 2 events, got %+v", batch)
	}
	obs, act := batch.Events[0], batch.Events[1]
	if obs.Type != "observation" || !strings.Contains(obs.Content, "hello") ||
		!strings.Contains(obs.Content, "Msg_1") {
		t.Fatalf("bad observation event: %+v", obs)
	}
	if act.Type != "memory_write" || act.Content != "note" ||
		act.Sequence != obs.Sequence+1 {
		t.Fatalf("bad action event: %+v", act)
	}
	if obs.Actor.UserID != "User_1" || obs.Actor.AgentID != "groupmembench-agent" ||
		obs.Actor.SessionID != "User_1/2025-07-19/s1" {
		t.Fatalf("bad actor: %+v", obs.Actor)
	}
	if obs.OccurredAt.IsZero() || act.OccurredAt.Before(obs.OccurredAt) {
		t.Fatalf("bad occurred_at: obs=%v act=%v", obs.OccurredAt, act.OccurredAt)
	}
}

func TestToSessionBatchRejectsMissingText(t *testing.T) {
	s := Session{SessionID: "User_1/2025-07-19/s1",
		Agent:        Persona{UserID: "User_1"},
		WindowEnd:    "2025-07-19T02:00:00",
		Observations: []Observation{{MsgNode: "Msg_missing",
			Timestamp: "2025-07-19T01:00:00"}}}
	if _, err := ToSessionBatch(s, map[string]string{}); err == nil {
		t.Fatal("want error for missing message text")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestToSessionBatch -v`
Expected: 编译失败

- [ ] **Step 3: 实现 convert.go**

```go
package agentsessions

import (
	"fmt"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/session"
)

const converterAgentID = "groupmembench-agent"

func ToSessionBatch(s Session, msgText map[string]string) (session.SessionBatch, error) {
	actor := session.Actor{UserID: s.Agent.UserID, AgentID: converterAgentID,
		SessionID: s.SessionID}
	windowEnd, err := time.ParseInLocation(timestampLayout, s.WindowEnd, time.UTC)
	if err != nil {
		return session.SessionBatch{}, fmt.Errorf("parse window end: %w", err)
	}
	var events []session.SessionEvent
	sequence := int64(0)
	for _, o := range s.Observations {
		text, ok := msgText[o.MsgNode]
		if !ok {
			return session.SessionBatch{}, fmt.Errorf(
				"missing text for %s in %s", o.MsgNode, s.SessionID)
		}
		at, err := time.ParseInLocation(timestampLayout, o.Timestamp, time.UTC)
		if err != nil {
			return session.SessionBatch{}, fmt.Errorf(
				"parse observation timestamp %s: %w", o.MsgNode, err)
		}
		sequence++
		events = append(events, session.SessionEvent{
			ID:         fmt.Sprintf("%s/obs/%s", s.SessionID, o.MsgNode),
			Actor:      actor,
			Sequence:   sequence,
			Type:       "observation",
			Content: fmt.Sprintf("[#%s msg:%s author:%s] %s",
				o.Channel, o.MsgNode, o.Author, text),
			Visibility: "team_note_eligible",
			OccurredAt: at,
		})
	}
	for i, action := range s.Trajectory {
		sequence++
		events = append(events, session.SessionEvent{
			ID:         fmt.Sprintf("%s/act/%d", s.SessionID, i),
			Actor:      actor,
			Sequence:   sequence,
			Type:       action.Type,
			Content:    action.Content,
			Visibility: "team_note_eligible",
			OccurredAt: windowEnd,
			Metadata:   actionMetadata(action),
		})
	}
	return session.SessionBatch{Events: events, Complete: true}, nil
}

func actionMetadata(action Action) map[string]string {
	metadata := map[string]string{}
	if len(action.SourceMsgs) > 0 {
		metadata["source_msgs"] = action.SourceMsgs[0]
		for _, node := range action.SourceMsgs[1:] {
			metadata["source_msgs"] += "," + node
		}
	}
	if action.Freeform {
		metadata["freeform"] = "true"
	}
	if action.Fallback {
		metadata["fallback"] = "true"
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}
```

注意:`session.SessionEvent` 字段名以 `internal/session/contracts.go` 为准,
若 struct 字段名与上面草稿不一致(如 `OccurredAt` 的具体导出名),以合同为准
修正测试与实现——**这是唯一允许对着源码调整签名的点**。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestToSessionBatch -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions 到 SessionBatch 线格式转换器"
```

---

### Task 12: 覆盖率验证器

**Files:**
- Create: `internal/eval/groupmembench/agentsessions/coverage.go`
- Test: `internal/eval/groupmembench/agentsessions/coverage_test.go`

**Interfaces:**
- Consumes: `EnhancedQuestion`、`Session`(Task 9/10)。
- Produces:

```go
type CoverageException struct {
	QuestionID string `json:"question_id"`
	Reason     string `json:"reason"` // "evidence_not_observed" | "no_memory_write"
	MsgNode    string `json:"msg_node"`
}

// VerifyCoverage:非拒答题的每个证据消息,必须 (a) 出现在提问者某 session
// 的 observations,(b) 提问者存在以该消息为 source 的 memory_write。
func VerifyCoverage(questions []EnhancedQuestion, sessions []Session) []CoverageException
```

- [ ] **Step 1: 写失败测试**

```go
// internal/eval/groupmembench/agentsessions/coverage_test.go
package agentsessions

import (
	"testing"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench"
)

func coverageFixture() ([]EnhancedQuestion, []Session) {
	questions := []EnhancedQuestion{
		{Question: groupmembench.Question{ID: "q_ok", AskingUserID: "User_1"},
			Category: "multi_hop", EvidenceMsgIDs: []string{"Msg_1"}},
		{Question: groupmembench.Question{ID: "q_unseen", AskingUserID: "User_1"},
			Category: "temporal", EvidenceMsgIDs: []string{"Msg_ghost"}},
		{Question: groupmembench.Question{ID: "q_abstain", AskingUserID: "User_1"},
			Category: "abstention"},
	}
	sessions := []Session{{SessionID: "User_1/2025-07-19/s1",
		Agent:        Persona{UserID: "User_1"},
		Observations: []Observation{{MsgNode: "Msg_1"}, {MsgNode: "Msg_2"}},
		Trajectory: []Action{{Type: "memory_write", SourceMsgs: []string{"Msg_1"}},
			{Type: "todo", SourceMsgs: []string{"Msg_2"}}}}}
	return questions, sessions
}

func TestVerifyCoverage(t *testing.T) {
	questions, sessions := coverageFixture()
	got := VerifyCoverage(questions, sessions)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 exception, got %+v", got)
	}
	if got[0].QuestionID != "q_unseen" || got[0].Reason != "evidence_not_observed" {
		t.Fatalf("bad exception: %+v", got[0])
	}
}

func TestVerifyCoverageFlagsMissingMemoryWrite(t *testing.T) {
	questions := []EnhancedQuestion{
		{Question: groupmembench.Question{ID: "q1", AskingUserID: "User_1"},
			Category: "multi_hop", EvidenceMsgIDs: []string{"Msg_2"}}}
	_, sessions := coverageFixture()
	got := VerifyCoverage(questions, sessions)
	// Msg_2 被观察到但只有 todo 没有 memory_write
	if len(got) != 1 || got[0].Reason != "no_memory_write" {
		t.Fatalf("want no_memory_write, got %+v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestVerifyCoverage -v`
Expected: 编译失败

- [ ] **Step 3: 实现 coverage.go**

```go
package agentsessions

type CoverageException struct {
	QuestionID string `json:"question_id"`
	Reason     string `json:"reason"`
	MsgNode    string `json:"msg_node"`
}

func VerifyCoverage(questions []EnhancedQuestion, sessions []Session) []CoverageException {
	observed := map[string]map[string]bool{}   // user → msg_node → seen
	memorized := map[string]map[string]bool{}  // user → msg_node → memory_write
	for _, s := range sessions {
		user := s.Agent.UserID
		if observed[user] == nil {
			observed[user] = map[string]bool{}
			memorized[user] = map[string]bool{}
		}
		for _, o := range s.Observations {
			observed[user][o.MsgNode] = true
		}
		for _, action := range s.Trajectory {
			if action.Type != "memory_write" {
				continue
			}
			for _, node := range action.SourceMsgs {
				memorized[user][node] = true
			}
		}
	}
	var exceptions []CoverageException
	for _, q := range questions {
		if q.Category == "abstention" {
			continue
		}
		for _, node := range q.EvidenceMsgIDs {
			switch {
			case !observed[q.AskingUserID][node]:
				exceptions = append(exceptions, CoverageException{
					QuestionID: q.ID, Reason: "evidence_not_observed", MsgNode: node})
			case !memorized[q.AskingUserID][node]:
				exceptions = append(exceptions, CoverageException{
					QuestionID: q.ID, Reason: "no_memory_write", MsgNode: node})
			}
		}
	}
	return exceptions
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/
git commit -m "feat(eval): agentsessions 评测证据覆盖率验证器"
```

---

### Task 13: CLI 编排(cmd/groupmembench-sessions)

**Files:**
- Create: `cmd/groupmembench-sessions/main.go`
- Create: `internal/eval/groupmembench/agentsessions/pipeline.go`
- Test: `internal/eval/groupmembench/agentsessions/pipeline_test.go`

**Interfaces:**
- Consumes: 前面全部任务的产物 + `groupmembench.LoadConversation` /
  `groupmembench.LoadQuestions` + `llm.NewDeepSeekClient`。
- Produces:

```go
type PipelineConfig struct {
	DomainFile   string // synthetic_domain_channels_rolevariants_Finance.json
	QuestionsDir string
	OutDir       string
	CacheDir     string
	Model        string // default "deepseek-v4-flash"
	Concurrency  int    // default 28
	MaxObs       int    // default 60
	TopK         int    // default 5
	Client       llm.ChatClient // 测试注入;nil → cmd 里构造 DeepSeek+Retry
}

type PipelineReport struct {
	Sessions   int
	Users      int
	Fill       FillStats
	Violations int
	Exceptions []CoverageException
}

// RunPipeline 全流程:normalize → anchors → windows → skeleton → fill(并发
// Concurrency,信号量限流)→ assemble → attach evidence → convert → verify,
// 输出写 OutDir:sessions.jsonl / messages.jsonl / questions_enhanced.jsonl /
// session_batches.json / coverage_exceptions.json / report.json。
func RunPipeline(ctx context.Context, config PipelineConfig) (PipelineReport, error)
```

CLI:`groupmembench-sessions -domain-file ... -questions-dir ... -out-dir ...
[-cache-dir ...] [-model deepseek-v4-flash] [-concurrency 28] [-api-key $DEEPSEEK_API_KEY]`

- [ ] **Step 1: 写失败测试(端到端,小 fixture + canned client)**

```go
// internal/eval/groupmembench/agentsessions/pipeline_test.go
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
		"multi_hop.jsonl": `{"id":"q1","question":"ESG deadline?","answer":"2025-07-18","asking_user_id":"User_1"}` + "\n",
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
```

说明:canned client 的 content 同时可被 anchor 判定与 fill 解码(两个解码器
都忽略未知字段),这让单个 fake 覆盖两条 LLM 路径。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/eval/groupmembench/agentsessions/ -run TestRunPipeline -v`
Expected: 编译失败

- [ ] **Step 3: 实现 pipeline.go**

```go
package agentsessions

import (
	"context"
	"encoding/json"
	"fmt"
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

func RunPipeline(ctx context.Context, config PipelineConfig) (PipelineReport, error) {
	var report PipelineReport
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
	type job struct {
		window  Window
		persona Persona
		specs   []ActionSpec
	}
	var jobs []job
	for _, user := range users {
		visible := VisibleTo(msgs, channels[user])
		persona := PersonaOf(msgs, user)
		for _, w := range Windows(user, visible, config.MaxObs) {
			jobs = append(jobs, job{window: w, persona: persona,
				specs: BuildSkeleton(w, parentAuthor, anchors)})
		}
	}
	sessions := make([]Session, len(jobs))
	semaphore := make(chan struct{}, config.Concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	for i, item := range jobs {
		wg.Add(1)
		go func(i int, item job) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
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
		return report, firstErr
	}
	report.Sessions = len(sessions)
	report.Fill = filler.Stats()

	enhanced = AttachEvidenceSessions(enhanced, sessions)
	report.Exceptions = VerifyCoverage(enhanced, sessions)

	batches := make([]session.SessionBatch, 0, len(sessions))
	for _, s := range sessions {
		batch, err := ToSessionBatch(s, msgText)
		if err != nil {
			return report, err
		}
		batches = append(batches, batch)
	}
	if err := WriteJSONL(filepath.Join(config.OutDir, "sessions.jsonl"),
		sessions); err != nil {
		return report, err
	}
	if err := WriteJSONL(filepath.Join(config.OutDir, "messages.jsonl"),
		MessageRows(msgs)); err != nil {
		return report, err
	}
	if err := WriteJSONL(filepath.Join(config.OutDir, "questions_enhanced.jsonl"),
		enhanced); err != nil {
		return report, err
	}
	for name, value := range map[string]any{
		"session_batches.json":      batches,
		"coverage_exceptions.json":  report.Exceptions,
		"report.json":               report,
	} {
		data, err := json.MarshalIndent(value, "", " ")
		if err != nil {
			return report, err
		}
		if err := os.WriteFile(filepath.Join(config.OutDir, name), data,
			0o644); err != nil {
			return report, err
		}
	}
	_ = fmt.Sprintf // keep fmt import if unused elsewhere
	return report, nil
}
```

(实现时若 fmt 未用则删除该行与 import;coverage_exceptions 为空时写 `[]`。)

- [ ] **Step 4: 实现 main.go(薄壳)**

```go
// Command groupmembench-sessions 把 GroupMemBench 域数据加工成 per-user
// agent 工作 session 数据集。见 docs/superpowers/specs/
// 2026-08-01-groupmembench-agent-sessions-design.md。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/pax-beehive/pax-nexus/internal/eval/groupmembench/agentsessions"
	"github.com/pax-beehive/pax-nexus/internal/platform/llm"
)

func main() {
	domainFile := flag.String("domain-file", "", "GroupMemBench domain JSON")
	questionsDir := flag.String("questions-dir", "", "questions directory")
	outDir := flag.String("out-dir", "", "output directory")
	cacheDir := flag.String("cache-dir", "", "LLM response cache directory")
	model := flag.String("model", "deepseek-v4-flash", "LLM model")
	concurrency := flag.Int("concurrency", 28, "LLM concurrency")
	apiKey := flag.String("api-key", os.Getenv("DEEPSEEK_API_KEY"), "DeepSeek API key")
	flag.Parse()
	if *domainFile == "" || *questionsDir == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "usage: -domain-file, -questions-dir, -out-dir are required")
		os.Exit(2)
	}
	if *apiKey == "" {
		fmt.Fprintln(os.Stderr, "missing DeepSeek API key (-api-key or DEEPSEEK_API_KEY)")
		os.Exit(2)
	}
	client := &agentsessions.RetryingChatClient{
		Inner: llm.NewDeepSeekClient(llm.DeepSeekConfig{APIKey: *apiKey}),
	}
	report, err := agentsessions.RunPipeline(context.Background(),
		agentsessions.PipelineConfig{
			DomainFile: *domainFile, QuestionsDir: *questionsDir,
			OutDir: *outDir, CacheDir: *cacheDir, Model: *model,
			Concurrency: *concurrency, MaxObs: 60, TopK: 5, Client: client,
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "pipeline failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sessions=%d users=%d calls=%d cache_hits=%d fallbacks=%d "+
		"violations=%d exceptions=%d\n",
		report.Sessions, report.Users, report.Fill.Calls, report.Fill.CacheHits,
		report.Fill.Fallbacks, report.Violations, len(report.Exceptions))
	if report.Fill.Calls > 0 &&
		float64(report.Fill.Fallbacks)/float64(report.Fill.Calls) > 0.02 {
		fmt.Fprintln(os.Stderr, "WARNING: fallback ratio above 2% red line")
	}
}
```

- [ ] **Step 5: 跑全部测试与构建**

Run: `go test ./internal/eval/groupmembench/... -race && go build ./cmd/groupmembench-sessions/ && go vet ./...`
Expected: 全部通过

- [ ] **Step 6: Commit**

```bash
git add internal/eval/groupmembench/agentsessions/ cmd/groupmembench-sessions/
git commit -m "feat(eval): groupmembench-sessions CLI 全管线编排"
```

---

### Task 14: Finance 全量运行 + HF 打包物料

**Files:**
- Create: `evals/groupmembench-sessions/README.md`(运行手册 + dataset card 草稿)
- Create: `evals/groupmembench-sessions/upload.sh`

**Interfaces:**
- Consumes: Task 13 的 CLI。
- Produces: `out/finance/` 全套产物 + HF 上传脚本。

- [ ] **Step 1: 下载源数据并全量运行(需 DEEPSEEK_API_KEY)**

```bash
mkdir -p /tmp/gmb && cd /tmp/gmb
curl -sLO "https://raw.githubusercontent.com/UCSB-NLP-Chang/GroupMemBench/main/data/final/Finance/synthetic_domain_channels_rolevariants_Finance.json"
mkdir -p questions && cd questions
for f in multi_hop knowledge_update temporal term_ambiguity user_implicit abstention; do
  curl -sLO "https://raw.githubusercontent.com/UCSB-NLP-Chang/GroupMemBench/main/questions/Finance/$f.jsonl"
done
cd /Users/toddzheng/Workspace/golang/team-memory
go run ./cmd/groupmembench-sessions \
  -domain-file /tmp/gmb/synthetic_domain_channels_rolevariants_Finance.json \
  -questions-dir /tmp/gmb/questions \
  -out-dir out/finance -cache-dir out/finance-cache
```

Expected: 正常退出;`sessions` 在 3000-5000 区间;fallback 比例 <2%;
`coverage_exceptions.json` 里逐条人工过目(见 Step 2)。

- [ ] **Step 2: 人工核对例外清单与抽样质检**

- 打开 `out/finance/coverage_exceptions.json`,对每条 `evidence_not_observed`
  判断是"提问者确实不可见证据"(原数据问题,记录进 README)还是 anchor 误判
  (改判后把修正写入 `questions_enhanced.jsonl` 重跑 verify);
- `questions_enhanced.jsonl` 中 `confidence: "low"` 的题逐条人工确认证据;
- 随机抽 30 个 session 检查轨迹语言质量(persona 口吻、事实保真)。

- [ ] **Step 3: 写 README + dataset card + upload.sh**

`evals/groupmembench-sessions/README.md` 内容(dataset card 部分):

```markdown
# GroupMemBench Agent Sessions (Finance)

Per-user agent work sessions derived from GroupMemBench (arXiv:2605.14498).
Each of the 12 Finance-domain users gets a simulated personal work agent that
observes the channels its principal participates in and produces a structured
trajectory (memory_write / draft_reply / todo) per daily session.

- `sessions.jsonl` — one agent work session per line
- `messages.jsonl` — original channel messages (referenced by msg_node)
- `questions_enhanced.jsonl` — original 214 questions + recovered
  evidence_msg_ids / evidence_session_ids
- `session_batches.json` — team-memory `/v1/session-batches` wire format

Synthesis: deterministic rule skeleton (anchors, decision reversals,
mentions, deadlines) + deepseek-v4-flash natural-language fill. Original
data: synthetic Microsoft-Teams-style enterprise conversations generated by
the GroupMemBench pipeline (GPT-5). Cite the original paper when using the
questions. Coverage exceptions and low-confidence anchors are listed in
`coverage_exceptions.json`.
```

`evals/groupmembench-sessions/upload.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
# usage: ./upload.sh <out-dir> <hf-repo-id>   e.g. ./upload.sh out/finance user/groupmembench-agent-sessions
OUT_DIR="$1"; REPO="$2"
hf repo create "$REPO" --repo-type dataset -y || true
hf upload "$REPO" "$OUT_DIR" finance --repo-type dataset
hf upload "$REPO" evals/groupmembench-sessions/README.md README.md --repo-type dataset
```

- [ ] **Step 4: 跑 shellcheck 与最终全量测试**

Run: `shellcheck evals/groupmembench-sessions/upload.sh && go test ./... 2>&1 | tail -20`
Expected: shellcheck 无告警;除 main 既有 flaky DB 测试外全绿(基线见
`docs/superpowers/specs` 相邻记录:main 门禁本身有 3 lint + 2 DB 既有红)。

- [ ] **Step 5: Commit**

```bash
git add evals/groupmembench-sessions/
git commit -m "docs(eval): groupmembench-sessions 运行手册与 HF 上传脚本"
```

---

## Self-Review 记录

- **Spec 覆盖**:§3 数据流→Task 13;§4 切窗→Task 3;§5 规则→Task 4;§6 重试/熔断/缓存/兜底→Task 6/7/8;§7 anchor→Task 5/9;§8.1-8.3 输出→Task 10/11;§9 验证→Task 12 + Task 14 Step 2;§10 预算→Task 14 Step 1 验证;§8.4 opencode-replay 明确移出本计划(另立计划)。
- **占位符扫描**:无 TBD/TODO;所有测试与实现均给出完整代码。
- **类型一致性**:`Msg/Window/ActionSpec/Action/Persona/Session/EnhancedQuestion` 在 Task 2/3/4/8/9/10 定义后被 11/12/13 按同名引用;唯一标注的"对源码调整点"是 Task 11 的 `session.SessionEvent` 字段名核对。
