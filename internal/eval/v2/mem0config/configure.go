package mem0config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultLLMModel      = "deepseek-v4-flash"
	defaultEmbedderModel = "text-embedding-3-small"
)

type Settings struct {
	DeepSeekAPIKey string
	OpenAIAPIKey   string
	LLMModel       string
	EmbedderModel  string
	PostgresHost   string
	PostgresPort   int
	PostgresDB     string
	PostgresUser   string
	PostgresPass   string
	CollectionName string
	HistoryDBPath  string
}

type providerConfig struct {
	Provider string         `json:"provider"`
	Config   map[string]any `json:"config"`
}

type memoryConfig struct {
	Version       string         `json:"version"`
	VectorStore   providerConfig `json:"vector_store"`
	LLM           providerConfig `json:"llm"`
	Embedder      providerConfig `json:"embedder"`
	HistoryDBPath string         `json:"history_db_path"`
}

func Configure(ctx context.Context, client *http.Client, endpoint string, settings Settings) error {
	if client == nil {
		return fmt.Errorf("configure mem0: HTTP client is required")
	}
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("configure mem0: endpoint is required")
	}
	settings = settings.withDefaults()
	if strings.TrimSpace(settings.DeepSeekAPIKey) == "" {
		return fmt.Errorf("configure mem0: DeepSeek API key is required")
	}
	if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
		return fmt.Errorf("configure mem0: OpenAI API key is required for embeddings")
	}

	payload, err := json.Marshal(settings.memoryConfig())
	if err != nil {
		return fmt.Errorf("configure mem0: encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("configure mem0: create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("configure mem0: send request: %w", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return fmt.Errorf("configure mem0: read response: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("configure mem0: close response: %w", closeErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("configure mem0: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (settings Settings) withDefaults() Settings {
	if settings.LLMModel == "" {
		settings.LLMModel = defaultLLMModel
	}
	if settings.EmbedderModel == "" {
		settings.EmbedderModel = defaultEmbedderModel
	}
	if settings.PostgresHost == "" {
		settings.PostgresHost = "mem0-postgres"
	}
	if settings.PostgresPort == 0 {
		settings.PostgresPort = 5432
	}
	if settings.PostgresDB == "" {
		settings.PostgresDB = "postgres"
	}
	if settings.PostgresUser == "" {
		settings.PostgresUser = "postgres"
	}
	if settings.PostgresPass == "" {
		settings.PostgresPass = "mem0-eval-password"
	}
	if settings.CollectionName == "" {
		settings.CollectionName = "memories"
	}
	if settings.HistoryDBPath == "" {
		settings.HistoryDBPath = "/app/history/history.db"
	}
	return settings
}

func (settings Settings) memoryConfig() memoryConfig {
	return memoryConfig{
		Version: "v1.1",
		VectorStore: providerConfig{Provider: "pgvector", Config: map[string]any{
			"host":            settings.PostgresHost,
			"port":            settings.PostgresPort,
			"dbname":          settings.PostgresDB,
			"user":            settings.PostgresUser,
			"password":        settings.PostgresPass,
			"collection_name": settings.CollectionName,
		}},
		LLM: providerConfig{Provider: "deepseek", Config: map[string]any{
			"api_key":     settings.DeepSeekAPIKey,
			"model":       settings.LLMModel,
			"temperature": 0.2,
		}},
		Embedder: providerConfig{Provider: "openai", Config: map[string]any{
			"api_key": settings.OpenAIAPIKey,
			"model":   settings.EmbedderModel,
		}},
		HistoryDBPath: settings.HistoryDBPath,
	}
}
