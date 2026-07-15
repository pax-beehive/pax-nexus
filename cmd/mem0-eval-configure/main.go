package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/eval/v2/mem0config"
)

const configureTimeout = 30 * time.Second

func main() {
	if err := run(); err != nil {
		log.New(os.Stderr, "", 0).Printf("mem0 eval configuration failed: %v", err)
		os.Exit(1)
	}
}

func run() error {
	postgresPort, err := envInt("POSTGRES_PORT", 5432)
	if err != nil {
		return err
	}
	settings := mem0config.Settings{
		DeepSeekAPIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		DeepSeekBaseURL: envOrDefault("MEM0_DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		OpenAIAPIKey:    os.Getenv("MEM0_OPENAI_API_KEY"),
		LLMModel:        envOrDefault("MEM0_DEFAULT_LLM_MODEL", "deepseek-v4-flash"),
		EmbedderModel:   envOrDefault("MEM0_DEFAULT_EMBEDDER_MODEL", "text-embedding-3-small"),
		PostgresHost:    envOrDefault("POSTGRES_HOST", "mem0-postgres"),
		PostgresPort:    postgresPort,
		PostgresDB:      envOrDefault("POSTGRES_DB", "postgres"),
		PostgresUser:    envOrDefault("POSTGRES_USER", "postgres"),
		PostgresPass:    envOrDefault("POSTGRES_PASSWORD", "mem0-eval-password"),
		CollectionName:  envOrDefault("POSTGRES_COLLECTION_NAME", "memories"),
		HistoryDBPath:   envOrDefault("HISTORY_DB_PATH", "/app/history/history.db"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), configureTimeout)
	defer cancel()
	endpoint := envOrDefault("MEM0_CONFIGURE_URL", "http://mem0:8000/configure")
	client := &http.Client{Timeout: configureTimeout}
	if err := mem0config.Configure(ctx, client, endpoint, settings); err != nil {
		return err
	}
	log.New(os.Stdout, "", 0).Printf("configured Mem0 LLM=%s embedder=%s", settings.LLMModel, settings.EmbedderModel)
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}
