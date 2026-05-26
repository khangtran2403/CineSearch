package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	geminiEmbedURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-2:embedContent"
	// Gemini Embedding 2 output dimension
	EmbedDimension = 3072
)

// ─── Request / Response types ────────────────────────────────────────────────

type geminiRequest struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
	// TaskType giúp model optimize vector cho đúng mục đích
	// RETRIEVAL_DOCUMENT  → dùng khi embed movie (lúc index)
	// RETRIEVAL_QUERY     → dùng khi embed taste vector / search query
	// SEMANTIC_SIMILARITY → dùng khi so sánh 2 đoạn text
	TaskType string `json:"taskType,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ─── Batch request ─────────────────────────────────────

type geminiBatchRequest struct {
	Requests []geminiRequest `json:"requests"`
}

type geminiBatchResponse struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
}

// ─── Client ───────────────────────────────────────────────────────────────────

type GeminiClient struct {
	apiKey     string
	httpClient *http.Client
}

func NewGeminiClient(apiKey string) *GeminiClient {
	return &GeminiClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *GeminiClient) Embed(ctx context.Context, text string, taskType string) ([]float32, error) {
	if taskType == "" {
		taskType = "RETRIEVAL_DOCUMENT"
	}

	reqBody := geminiRequest{
		Model:    "models/gemini-embedding-2",
		TaskType: taskType,
		Content: geminiContent{
			Parts: []geminiPart{{Text: text}},
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s?key=%s", geminiEmbedURL, c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini API returned status %d", resp.StatusCode)
	}

	var result geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("gemini API error %d: %s", result.Error.Code, result.Error.Message)
	}

	if len(result.Embedding.Values) == 0 {
		return nil, fmt.Errorf("empty embedding returned")
	}

	return result.Embedding.Values, nil
}

// EmbedBatch — embed nhiều text cùng lúc, tiết kiệm API call.
// Tối đa 100 items/batch theo Gemini API limit.
func (c *GeminiClient) EmbedBatch(ctx context.Context, texts []string, taskType string) ([][]float32, error) {
	if taskType == "" {
		taskType = "RETRIEVAL_DOCUMENT"
	}
	if len(texts) > 100 {
		return nil, fmt.Errorf("batch size exceeds limit of 100 (got %d)", len(texts))
	}

	requests := make([]geminiRequest, len(texts))
	for i, t := range texts {
		requests[i] = geminiRequest{
			Model:    "models/gemini-embedding-2",
			TaskType: taskType,
			Content:  geminiContent{Parts: []geminiPart{{Text: t}}},
		}
	}

	batchBody := geminiBatchRequest{Requests: requests}
	data, err := json.Marshal(batchBody)
	if err != nil {
		return nil, fmt.Errorf("marshal batch: %w", err)
	}

	batchURL := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-2:batchEmbedContents?key=%s",
		c.apiKey,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, batchURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini batch request: %w", err)
	}
	defer resp.Body.Close()

	var result geminiBatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode batch response: %w", err)
	}

	vectors := make([][]float32, len(result.Embeddings))
	for i, e := range result.Embeddings {
		vectors[i] = e.Values
	}
	return vectors, nil
}

// ─── Helper functions ─────────────────────────────────────────────────────────

// EmbedMovie — embed một bộ phim với RETRIEVAL_DOCUMENT task type.
func (c *GeminiClient) EmbedMovie(ctx context.Context, title string, genres []string, overview string) ([]float32, error) {
	text := MovieText(title, genres, overview)
	return c.Embed(ctx, text, "RETRIEVAL_DOCUMENT")
}

// EmbedQuery — embed taste vector / search query với RETRIEVAL_QUERY task type.
func (c *GeminiClient) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return c.Embed(ctx, text, "RETRIEVAL_QUERY")
}

// MovieText — tạo text để embed, giữ nguyên từ client.go gốc
func MovieText(title string, genres []string, overview string) string {
	 return fmt.Sprintf("Title: %s\nGenres: %s\nPlot: %s", title, strings.Join(genres, ", "), overview)
}

// UpdateTasteVec — weighted moving average của taste vector.
func UpdateTasteVec(current []float32, newVec []float32, weight float64) []float32 {
	if len(current) == 0 {
		return newVec
	}
	alpha := weight * 0.3 // learning rate
	result := make([]float32, len(current))
	for i := range current {
		result[i] = float32((1-alpha)*float64(current[i]) + alpha*float64(newVec[i]))
	}
	return result
}