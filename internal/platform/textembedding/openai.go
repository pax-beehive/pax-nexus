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
}

// OpenAI is an adapter for an OpenAI-compatible local embedding runtime.
type OpenAI struct {
	endpoint   string
	model      string
	dimensions int
	client     *http.Client
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
