// Package groupmembench builds bounded, reproducible evaluation cases from
// the official GroupMemBench conversations and question sets.
package groupmembench

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"
)

var categories = []string{
	"multi_hop",
	"knowledge_update",
	"temporal",
	"user_implicit",
	"term_ambiguity",
	"abstention",
}

type Question struct {
	ID           string `json:"id"`
	Question     string `json:"question"`
	Answer       string `json:"answer"`
	AskingUserID string `json:"asking_user_id"`
}

type Message struct {
	NodeID                 string         `json:"msg_node"`
	Channel                string         `json:"channel"`
	Author                 string         `json:"author"`
	Role                   string         `json:"role"`
	Content                string         `json:"content"`
	Timestamp              string         `json:"timestamp"`
	ReplyTo                string         `json:"reply_to,omitempty"`
	PhaseName              string         `json:"phase_name,omitempty"`
	Topic                  string         `json:"topic,omitempty"`
	IsNoise                bool           `json:"is_noise"`
	IsDecisionPoint        bool           `json:"is_decision_point"`
	DecisionChangeMetadata map[string]any `json:"decision_change_metadata,omitempty"`
	channelIndex           int
}

type Case struct {
	Category string    `json:"category"`
	Question Question  `json:"question"`
	Messages []Message `json:"messages"`
}

type Config struct {
	PerCategory        int
	TopK               int
	NeighborRadius     int
	MaxContextMessages int
	Seed               string
}

func Categories() []string {
	return slices.Clone(categories)
}

func Select(questions map[string][]Question, messages []Message, config Config) ([]Case, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("select GroupMemBench cases: messages are required")
	}
	messages = normalizeMessages(messages)
	index := newBM25Index(messages)
	result := make([]Case, 0, len(categories)*config.PerCategory)
	for _, category := range categories {
		available := slices.Clone(questions[category])
		if len(available) < config.PerCategory {
			return nil, fmt.Errorf("select GroupMemBench cases: category %q has %d questions, need %d", category, len(available), config.PerCategory)
		}
		slices.SortFunc(available, func(left, right Question) int {
			return strings.Compare(selectionKey(config.Seed, category, left.ID), selectionKey(config.Seed, category, right.ID))
		})
		for _, question := range available[:config.PerCategory] {
			contextMessages := index.retrieve(question.Question, config.TopK, config.NeighborRadius, config.MaxContextMessages)
			result = append(result, Case{Category: category, Question: question, Messages: contextMessages})
		}
	}
	return result, nil
}

func normalizeConfig(config Config) (Config, error) {
	if config.PerCategory < 0 || config.TopK < 0 || config.NeighborRadius < 0 || config.MaxContextMessages < 0 {
		return Config{}, fmt.Errorf("select GroupMemBench cases: limits cannot be negative")
	}
	if config.PerCategory == 0 {
		config.PerCategory = 2
	}
	if config.TopK == 0 {
		config.TopK = 8
	}
	if config.MaxContextMessages == 0 {
		config.MaxContextMessages = 32
	}
	if config.MaxContextMessages < config.TopK {
		return Config{}, fmt.Errorf("select GroupMemBench cases: max context messages cannot be less than top-k")
	}
	if config.Seed == "" {
		config.Seed = "team-memory-v1"
	}
	return config, nil
}

func normalizeMessages(messages []Message) []Message {
	result := slices.Clone(messages)
	channelIndexes := make(map[string]int)
	for index := range result {
		result[index].channelIndex = channelIndexes[result[index].Channel]
		channelIndexes[result[index].Channel]++
	}
	return result
}

func selectionKey(seed, category, id string) string {
	digest := sha256.Sum256([]byte(seed + "\x00" + category + "\x00" + id))
	return hex.EncodeToString(digest[:])
}

type bm25Index struct {
	messages       []Message
	tokens         [][]string
	documentFreq   map[string]int
	averageLength  float64
	messageByID    map[string]int
	channelIndexes map[string][]int
}

func newBM25Index(messages []Message) bm25Index {
	index := bm25Index{
		messages: messages, tokens: make([][]string, len(messages)), documentFreq: make(map[string]int),
		messageByID: make(map[string]int), channelIndexes: make(map[string][]int),
	}
	totalLength := 0
	for messageIndex, message := range messages {
		index.messageByID[message.NodeID] = messageIndex
		index.channelIndexes[message.Channel] = append(index.channelIndexes[message.Channel], messageIndex)
		index.tokens[messageIndex] = tokenize(message.Content)
		totalLength += len(index.tokens[messageIndex])
		seen := make(map[string]struct{})
		for _, token := range index.tokens[messageIndex] {
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			index.documentFreq[token]++
		}
	}
	index.averageLength = float64(totalLength) / float64(len(messages))
	return index
}

func (index bm25Index) retrieve(query string, limit, neighborRadius, maxContextMessages int) []Message {
	type scoredMessage struct {
		index int
		score float64
	}
	scored := make([]scoredMessage, len(index.messages))
	queryTokens := tokenize(query)
	for messageIndex := range index.messages {
		scored[messageIndex] = scoredMessage{index: messageIndex, score: index.score(queryTokens, messageIndex)}
	}
	slices.SortFunc(scored, func(left, right scoredMessage) int {
		if left.score != right.score {
			if left.score > right.score {
				return -1
			}
			return 1
		}
		return strings.Compare(index.messages[left.index].NodeID, index.messages[right.index].NodeID)
	})
	selected := make(map[int]struct{})
	topHits := scored[:min(limit, len(scored))]
	for _, scoredMessage := range topHits {
		index.addContext(selected, scoredMessage.index, neighborRadius)
	}
	messageIndexes := make([]int, 0, len(selected))
	for messageIndex := range selected {
		messageIndexes = append(messageIndexes, messageIndex)
	}
	slices.SortFunc(messageIndexes, func(left, right int) int {
		leftMessage, rightMessage := index.messages[left], index.messages[right]
		if leftMessage.Timestamp != rightMessage.Timestamp {
			return strings.Compare(leftMessage.Timestamp, rightMessage.Timestamp)
		}
		return strings.Compare(leftMessage.NodeID, rightMessage.NodeID)
	})
	if len(messageIndexes) > maxContextMessages {
		bounded := make(map[int]struct{}, maxContextMessages)
		for _, hit := range topHits {
			bounded[hit.index] = struct{}{}
		}
		for _, messageIndex := range messageIndexes {
			if len(bounded) == maxContextMessages {
				break
			}
			bounded[messageIndex] = struct{}{}
		}
		messageIndexes = messageIndexes[:0]
		for messageIndex := range bounded {
			messageIndexes = append(messageIndexes, messageIndex)
		}
		slices.SortFunc(messageIndexes, func(left, right int) int {
			leftMessage, rightMessage := index.messages[left], index.messages[right]
			if leftMessage.Timestamp != rightMessage.Timestamp {
				return strings.Compare(leftMessage.Timestamp, rightMessage.Timestamp)
			}
			return strings.Compare(leftMessage.NodeID, rightMessage.NodeID)
		})
	}
	result := make([]Message, 0, len(messageIndexes))
	for _, messageIndex := range messageIndexes {
		result = append(result, index.messages[messageIndex])
	}
	return result
}

func (index bm25Index) score(queryTokens []string, messageIndex int) float64 {
	const k1 = 1.5
	const b = 0.75
	frequencies := make(map[string]int)
	for _, token := range index.tokens[messageIndex] {
		frequencies[token]++
	}
	documentLength := float64(len(index.tokens[messageIndex]))
	var score float64
	for _, token := range queryTokens {
		frequency := float64(frequencies[token])
		if frequency == 0 {
			continue
		}
		documentFrequency := float64(index.documentFreq[token])
		inverseDocumentFrequency := math.Log(1 + (float64(len(index.messages))-documentFrequency+0.5)/(documentFrequency+0.5))
		denominator := frequency + k1*(1-b+b*documentLength/index.averageLength)
		score += inverseDocumentFrequency * frequency * (k1 + 1) / denominator
	}
	return score
}

func (index bm25Index) addContext(selected map[int]struct{}, messageIndex, neighborRadius int) {
	selected[messageIndex] = struct{}{}
	message := index.messages[messageIndex]
	channel := index.channelIndexes[message.Channel]
	from := max(0, message.channelIndex-neighborRadius)
	to := min(len(channel), message.channelIndex+neighborRadius+1)
	for _, adjacent := range channel[from:to] {
		selected[adjacent] = struct{}{}
	}
	for replyTo := message.ReplyTo; replyTo != ""; {
		parentIndex, ok := index.messageByID[replyTo]
		if !ok {
			break
		}
		selected[parentIndex] = struct{}{}
		replyTo = index.messages[parentIndex].ReplyTo
	}
}

func tokenize(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(current rune) bool {
		return !unicode.IsLetter(current) && !unicode.IsNumber(current)
	})
}
