package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const (
	GenerationConfigVersion = "deepseek-generation.v2"
	DefaultMaxTokens        = 3000
)

type GenerationConfig struct {
	Version        string `json:"config_version"`
	Model          string `json:"model"`
	MaxTokens      int    `json:"max_tokens"`
	ResponseFormat string `json:"response_format"`
	Temperature    string `json:"temperature"`
	TopP           string `json:"top_p"`
	Reasoning      string `json:"reasoning"`
}

type CompletionUsage struct {
	PromptTokens     int  `json:"prompt_tokens"`
	CompletionTokens int  `json:"completion_tokens"`
	TotalTokens      int  `json:"total_tokens"`
	Available        bool `json:"-"`
}

type CompletionResult struct {
	Content       string
	Model         string
	ResponseID    string
	FinishReason  string
	Usage         CompletionUsage
	Latency       time.Duration
	ConfigVersion string
}

type Client struct {
	client       *openai.Client
	config       GenerationConfig
	sem          chan struct{}
	usageTracker *usageTracker
}

type usageTracker struct {
	mu        sync.Mutex
	available bool
}

func (t *usageTracker) reset() {
	t.mu.Lock()
	t.available = false
	t.mu.Unlock()
}

func (t *usageTracker) setAvailable(available bool) {
	t.mu.Lock()
	t.available = available
	t.mu.Unlock()
}

func (t *usageTracker) isAvailable() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.available
}

// trackingHTTPClient preserves the SDK boundary while retaining whether the
// provider response contained a usage object. Zero token counts alone cannot
// distinguish unavailable usage from a legitimate zero.
type trackingHTTPClient struct {
	delegate openai.HTTPDoer
	tracker  *usageTracker
}

func (c *trackingHTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := c.delegate.Do(request)
	if err != nil || response == nil || response.Body == nil {
		return response, err
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		return response, readErr
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) == nil {
		usage, ok := envelope["usage"]
		c.tracker.setAvailable(ok && string(bytes.TrimSpace(usage)) != "null")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	return response, nil
}

func NewClient(apiKey, baseURL, model string) *Client {
	config := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		config.BaseURL = strings.TrimRight(baseURL, "/")
	}
	if model == "" {
		model = "deepseek-flash"
	}
	tracker := &usageTracker{}
	config.HTTPClient = &trackingHTTPClient{delegate: config.HTTPClient, tracker: tracker}
	return &Client{client: openai.NewClientWithConfig(config), config: GenerationConfig{Version: GenerationConfigVersion, Model: model, MaxTokens: DefaultMaxTokens, ResponseFormat: "json_object", Temperature: "provider-default", TopP: "provider-default", Reasoning: "provider-default"}, sem: make(chan struct{}, 1), usageTracker: tracker}
}

func (c *Client) Config() GenerationConfig { return c.config }

func (c *Client) Complete(ctx context.Context, request CompletionRequest) (result CompletionResult, err error) {
	started := time.Now()
	result = CompletionResult{Model: c.config.Model, ConfigVersion: c.config.Version, Usage: CompletionUsage{Available: false}}
	defer func() { result.Latency = time.Since(started) }()
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return result, ctx.Err()
	}
	requestCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	c.usageTracker.reset()
	messages := make([]openai.ChatCompletionMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		messages = append(messages, openai.ChatCompletionMessage{Role: message.Role, Content: message.Content})
	}
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.config.MaxTokens
	}
	resp, err := c.client.CreateChatCompletion(requestCtx, openai.ChatCompletionRequest{Model: c.config.Model, Messages: messages, MaxTokens: maxTokens, ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}})
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return result, ctx.Err()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return result, fmt.Errorf("%w: request timed out", ErrTimeout)
		}
		return result, fmt.Errorf("%w: AI request failed: %v", ErrUpstream, err)
	}
	if resp.Model != "" {
		result.Model = resp.Model
	}
	result.ResponseID = resp.ID
	result.Usage = CompletionUsage{PromptTokens: resp.Usage.PromptTokens, CompletionTokens: resp.Usage.CompletionTokens, TotalTokens: resp.Usage.TotalTokens, Available: c.usageTracker.isAvailable()}
	if len(resp.Choices) == 0 {
		return result, errors.New("AI returned an empty response")
	}
	choice := resp.Choices[0]
	result.FinishReason = string(choice.FinishReason)
	if result.FinishReason != string(openai.FinishReasonStop) {
		switch choice.FinishReason {
		case openai.FinishReasonLength:
			return result, ErrTruncated
		case openai.FinishReasonContentFilter:
			return result, ErrContentFiltered
		case openai.FinishReasonToolCalls, openai.FinishReasonFunctionCall:
			return result, ErrToolCalls
		default:
			return result, fmt.Errorf("%w: %s", ErrAbnormalFinish, result.FinishReason)
		}
	}
	result.Content = strings.TrimSpace(choice.Message.Content)
	if result.Content == "" {
		return result, errors.New("AI returned an empty response")
	}
	return result, nil
}
