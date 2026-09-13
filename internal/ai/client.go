package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"taskmanager-ai/internal/domain"

	openai "github.com/sashabaranov/go-openai"
)

type Client struct {
	client      *openai.Client
	model       string
	sem         chan struct{}
	log         *slog.Logger
	usageConfig UsageConfig
}

type ClientOption func(*clientOptions)

type clientOptions struct {
	log         *slog.Logger
	usageConfig UsageConfig
}

func WithLogger(logger *slog.Logger) ClientOption {
	return func(options *clientOptions) { options.log = logger }
}

func WithUsageConfig(config UsageConfig) ClientOption {
	return func(options *clientOptions) { options.usageConfig = config }
}

func NewClient(apiKey, baseURL, model string, options ...ClientOption) *Client {
	config := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		config.BaseURL = strings.TrimRight(baseURL, "/")
	}
	if model == "" {
		model = "deepseek-chat"
	}
	clientOptions := clientOptions{log: slog.Default()}
	for _, option := range options {
		if option != nil {
			option(&clientOptions)
		}
	}
	if clientOptions.log == nil {
		clientOptions.log = slog.Default()
	}
	return &Client{
		client:      openai.NewClientWithConfig(config),
		model:       model,
		sem:         make(chan struct{}, 1),
		log:         clientOptions.log,
		usageConfig: clientOptions.usageConfig,
	}
}

func (c *Client) Complete(ctx context.Context, systemPrompt string, messages []domain.ChatMessage) (CompletionResult, error) {
	return c.complete(ctx, systemPrompt, messages, c.model, 2000, true)
}

func (c *Client) complete(ctx context.Context, systemPrompt string, messages []domain.ChatMessage, model string, maxTokens int, jsonResponse bool) (CompletionResult, error) {
	usage := estimatePrompt(systemPrompt, messages)
	usage.Model = model
	for _, message := range messages {
		if message.Role == openai.ChatMessageRoleSystem && strings.HasPrefix(message.Content, "DIALOGUE SUMMARY") {
			usage.HistoryCompacted = true
			usage.SummaryTokens = estimateTokens("summary", message.Content)
		}
	}
	usage.ContextWindowTokens = nil
	if c.usageConfig.ContextWindowTokens > 0 {
		usage.ContextWindowTokens = intPtr(c.usageConfig.ContextWindowTokens)
	}
	usage.ContextStatus = contextStatus(usage.EstimatedInputTokens, c.usageConfig)
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return CompletionResult{Usage: usage}, ctx.Err()
	}

	requestCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()

	requestMessages := make([]openai.ChatCompletionMessage, 0, len(messages)+1)
	requestMessages = append(requestMessages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: systemPrompt})
	for _, message := range messages {
		requestMessages = append(requestMessages, openai.ChatCompletionMessage{Role: message.Role, Content: message.Content})
	}
	callID := usageCallID()
	started := time.Now()
	outcome := "upstream_error"
	responseID := ""
	defer func() {
		attrs := []any{
			"call_id", callID,
			"call_kind", func() string {
				if jsonResponse {
					return "main"
				}
				return "summarizer"
			}(),
			"model", model,
			"outcome", outcome,
			"duration", time.Since(started),
			"system_prompt_tokens", usage.SystemPromptTokens,
			"current_request_tokens", usage.CurrentRequestTokens,
			"history_tokens", usage.HistoryTokens,
			"estimated_input_tokens", usage.EstimatedInputTokens,
			"component_token_count", usage.ComponentTokenCount,
			"history_messages", max(0, len(messages)-1),
			"history_truncated", usage.HistoryTruncated,
			"history_compacted", usage.HistoryCompacted,
			"usage_available", usage.UsageAvailable,
			"usage_inconsistent", usage.UsageInconsistent,
		}
		if usage.ContextWindowTokens != nil {
			attrs = append(attrs, "context_window_tokens", *usage.ContextWindowTokens)
		}
		if usage.ContextStatus != "" {
			attrs = append(attrs, "context_status", usage.ContextStatus)
		}
		if usage.InputTokens != nil {
			attrs = append(attrs, "input_tokens", *usage.InputTokens)
		}
		if usage.OutputTokens != nil {
			attrs = append(attrs, "output_tokens", *usage.OutputTokens)
		}
		if usage.TotalTokens != nil {
			attrs = append(attrs, "total_tokens", *usage.TotalTokens)
		}
		if usage.EstimatedCostUSD != nil {
			attrs = append(attrs, "estimated_cost_usd", *usage.EstimatedCostUSD)
		}
		if responseID != "" {
			attrs = append(attrs, "response_id", responseID)
		}
		c.log.Info("AI model call", attrs...)
	}()
	request := openai.ChatCompletionRequest{
		Model:     model,
		Messages:  requestMessages,
		MaxTokens: maxTokens,
	}
	if jsonResponse {
		request.ResponseFormat = &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}
	}
	resp, err := c.client.CreateChatCompletion(requestCtx, request)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			outcome = "cancelled"
			return CompletionResult{Usage: usage}, ctx.Err()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			outcome = "timeout"
			return CompletionResult{Usage: usage}, fmt.Errorf("%w: request timed out", ErrTimeout)
		}
		if isContextOverflow(err) {
			outcome = "context_window_exceeded"
			usage.ContextStatus = "context_window_exceeded"
			return CompletionResult{Usage: usage}, fmt.Errorf("%w: %w", ErrContextWindowExceeded, err)
		}
		return CompletionResult{Usage: usage}, fmt.Errorf("%w: AI request failed: %w", ErrUpstream, err)
	}
	responseID = resp.ID
	usage = providerUsage(usage, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	usage.ContextStatus = contextStatusForProvider(resp.Usage.PromptTokens, c.usageConfig)
	addCost(&usage, c.usageConfig)
	if len(resp.Choices) == 0 {
		return CompletionResult{Usage: usage}, errors.New("AI returned an empty response")
	}
	choice := resp.Choices[0]
	if choice.FinishReason == openai.FinishReasonLength {
		outcome = "output_truncated"
		return CompletionResult{Usage: usage}, ErrTruncated
	}
	content := strings.TrimSpace(choice.Message.Content)
	if content == "" {
		return CompletionResult{Usage: usage}, errors.New("AI returned an empty response")
	}
	outcome = "success"
	return CompletionResult{Content: content, Usage: usage}, nil
}

func isContextOverflow(err error) bool {
	var apiErr *openai.APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatusCode != http.StatusBadRequest {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(fmt.Sprint(apiErr.Code)))
	typ := strings.ToLower(strings.TrimSpace(apiErr.Type))
	for _, value := range []string{code, typ} {
		if value == "context_length_exceeded" || value == "context_length_exceeded_error" || value == "input_context_length_exceeded" {
			return true
		}
	}
	message := strings.ToLower(apiErr.Message)
	return strings.Contains(message, "context length") || strings.Contains(message, "maximum context") || strings.Contains(message, "prompt is too long") || strings.Contains(message, "token limit")
}
