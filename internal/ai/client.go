package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const contextTextMock = `SYSTEM STATE

Current CPU: 1.8%
Memory: 9.51 GB / 15.71 GB (60.0%)

Disks:
- C: (C:): 308.97 GB used / 458.49 GB total
- D: (D:): 255.25 GB used / 953.85 GB total

Disk I/O: read 0.03 MB/s, write 0.14 MB/s
Network: receive 0.35 KB/s, send 0.36 KB/s

Top 5 processes by CPU:
1. brave.exe (PID 18776): CPU 15.0%, MEM 1.5%
2. brave.exe (PID 16304): CPU 13.2%, MEM 2.4%
3. Code.exe (PID 13076): CPU 10.0%, MEM 2.5%
4. brave.exe (PID 23036): CPU 8.9%, MEM 1.0%
5. go.exe (PID 5048): CPU 7.6%, MEM 0.2%

Change vs previous snapshot: CPU +0.5 pp, Memory +0.0 pp

History over last 10 snapshots:
CPU: avg 5.7%, min 1.3%, max 11.1%
Memory: avg 60.0%, min 60.0%, max 60.0%`

const systemPrompt = `Ты AI-ассистент`

type Client struct {
	client *openai.Client
	model  string
	sem    chan struct{}
}

func NewClient(apiKey, baseURL, model string) *Client {
	config := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		config.BaseURL = strings.TrimRight(baseURL, "/")
	}
	if model == "" {
		model = "deepseek-chat"
	}
	return &Client{
		client: openai.NewClientWithConfig(config),
		model:  model,
		sem:    make(chan struct{}, 1),
	}
}

func (c *Client) Ask(ctx context.Context, question, contextText string) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", errors.New("question is empty")
	}
	if len([]rune(question)) > 4000 {
		return "", errors.New("question exceeds 4000 characters")
	}

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	userContent := fmt.Sprintf("SYSTEM CONTEXT:\n%s\n\nUSER QUESTION:\n%s", contextTextMock, question)
	resp, err := c.client.CreateChatCompletion(requestCtx, openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userContent},
		},
	})
	if err != nil {
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return "", errors.New("AI request timed out after 30 seconds")
		}
		return "", fmt.Errorf("AI request failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("AI returned an empty response")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}
