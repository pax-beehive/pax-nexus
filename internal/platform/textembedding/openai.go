// Package textembedding provides adapters for local text embedding runtimes.
package textembedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
)

const maxResponseBytes = 16 << 20

// Embedder maps text batches to normalized dense vectors.
type Embedder interface {
	Embed(context.Context, []string) ([][]float32, error)
}

type OpenAIConfig struct {
	BaseURL    string
	Model      string
	Dimensions int
	Client     *http.Client
	// APIKey authenticates against a hosted provider. A local runtime needs
	// none, and an empty value sends no Authorization header at all.
	APIKey string
}

// OpenAI is an adapter for any OpenAI-compatible embedding runtime, local
// or hosted. Responses are truncated to the configured dimensions and
// re-normalized, so a provider returning a longer Matryoshka vector (such as
// OpenAI's text-embedding-3-small at 1536) is used correctly at the shorter
// length the store expects.
type OpenAI struct {
	endpoint   string
	model      string
	dimensions int
	client     *http.Client
	apiKey     string
}

func NewOpenAI(config OpenAIConfig) (*OpenAI, error) {
	baseURL, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("create OpenAI embedder: valid base URL is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("create OpenAI embedder: model is required")
	}
	if config.Dimensions <= 0 {
		return nil, fmt.Errorf("create OpenAI embedder: positive dimensions are required")
	}
	if config.Client == nil {
		return nil, fmt.Errorf("create OpenAI embedder: HTTP client is required")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/v1/embeddings"
	return &OpenAI{
		endpoint: baseURL.String(), model: config.Model,
		dimensions: config.Dimensions, client: config.Client,
		apiKey: strings.TrimSpace(config.APIKey),
	}, nil
}

func (o *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("embed text: at least one input is required")
	}
	payload, err := json.Marshal(struct {
		Input          []string `json:"input"`
		Model          string   `json:"model"`
		EncodingFormat string   `json:"encoding_format"`
	}{Input: texts, Model: o.model, EncodingFormat: "float"})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	response, err := o.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send embedding request: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	closeErr := response.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close embedding response: %w", closeErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("embed text: runtime returned status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("embed text: received %d vectors for %d inputs", len(result.Data), len(texts))
	}
	vectors := make([][]float32, len(texts))
	for _, item := range result.Data {
		if item.Index < 0 || item.Index >= len(vectors) || vectors[item.Index] != nil {
			return nil, fmt.Errorf("embed text: invalid response index %d", item.Index)
		}
		vector, normalizeErr := truncateAndNormalize(item.Embedding, o.dimensions)
		if normalizeErr != nil {
			return nil, fmt.Errorf("normalize embedding %d: %w", item.Index, normalizeErr)
		}
		vectors[item.Index] = vector
	}
	return vectors, nil
}

func truncateAndNormalize(vector []float32, dimensions int) ([]float32, error) {
	if len(vector) < dimensions {
		return nil, fmt.Errorf("received %d dimensions, need %d", len(vector), dimensions)
	}
	result := append([]float32(nil), vector[:dimensions]...)
	var squaredNorm float64
	for _, value := range result {
		squaredNorm += float64(value) * float64(value)
	}
	if squaredNorm == 0 || math.IsNaN(squaredNorm) || math.IsInf(squaredNorm, 0) {
		return nil, fmt.Errorf("embedding has invalid norm")
	}
	norm := float32(math.Sqrt(squaredNorm))
	for index := range result {
		result[index] /= norm
	}
	return result, nil
}

// nativeModelDimensions maps an embedding model to the width it emits. A
// deployment configures the model it runs, and the width follows from it,
// because configuring both invites the two to disagree -- a mismatch that
// surfaces only as a failed write or as silently discarded resolution.
//
// Keys are lower-cased and matched both fully qualified and bare, so
// "Qwen/Qwen3-Embedding-0.6B" and "Qwen3-Embedding-0.6B" resolve alike.
var nativeModelDimensions = map[string]int{
	"text-embedding-3-small": 1536,
	"text-embedding-3-large": 3072,
	"text-embedding-ada-002": 1536,
	"qwen3-embedding-0.6b":   1024,
	"qwen3-embedding-4b":     2560,
	"qwen3-embedding-8b":     4096,
}

// ModelDimensions resolves the stored vector width for a model.
//
// An explicit override always wins: a Matryoshka model is legitimately used
// below its native width, and a model this build has never heard of still
// needs a way in. Otherwise the width comes from the model. An unknown model
// with no override is an error rather than a guess, because guessing wrong
// is not visible until vectors fail to store.
func ModelDimensions(model string, override int) (int, error) {
	if override > 0 {
		return override, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(model))
	if width, found := nativeModelDimensions[normalized]; found {
		return width, nil
	}
	if index := strings.LastIndex(normalized, "/"); index >= 0 {
		if width, found := nativeModelDimensions[normalized[index+1:]]; found {
			return width, nil
		}
	}
	return 0, fmt.Errorf(
		"embedding model %q has no known vector width; set TEAM_MEMORY_EMBEDDING_DIMENSIONS to its width",
		strings.TrimSpace(model),
	)
}
