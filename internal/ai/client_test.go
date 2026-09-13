package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"taskmanager-ai/internal/domain"
	"testing"
	"time"
)

func TestClientCompleteSendsModelAndMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request["model"] != "test-model" {
			t.Errorf("model = %v", request["model"])
		}
		messages, ok := request["messages"].([]any)
		if !ok || len(messages) != 4 {
			t.Fatalf("messages = %v", request["messages"])
		}
		want := [][2]string{{"system", "system"}, {"user", "old question"}, {"assistant", "old answer"}, {"user", "content"}}
		for i, expected := range want {
			message, _ := messages[i].(map[string]any)
			if message["role"] != expected[0] || message["content"] != expected[1] {
				t.Fatalf("messages = %v", messages)
			}
		}
		_, _ = w.Write([]byte(`{"id":"response-1","choices":[{"message":{"content":"  answer  "}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}}`))
	}))
	defer server.Close()

	answer, err := NewClient("key", server.URL, "test-model").Complete(context.Background(), "system", []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "old question"}, {Role: domain.ChatRoleAssistant, Content: "old answer"}, {Role: domain.ChatRoleUser, Content: "content"}})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if answer.Content != "answer" {
		t.Fatalf("answer = %q, want answer", answer.Content)
	}
	if answer.Usage.InputTokens == nil || *answer.Usage.InputTokens != 100 || answer.Usage.OutputTokens == nil || *answer.Usage.OutputTokens != 20 || answer.Usage.TotalTokens == nil || *answer.Usage.TotalTokens != 120 {
		t.Fatalf("usage = %#v", answer.Usage)
	}
}

func TestClientCompleteLogsStructuredUsageWithoutPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"response-1","choices":[{"message":{"content":"safe answer"}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`))
	}))
	defer server.Close()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	_, err := NewClient("key", server.URL, "model", WithLogger(logger), WithUsageConfig(UsageConfig{InputPricePerMillionTokens: 1, OutputPricePerMillionTokens: 2, PricingConfigured: true})).Complete(context.Background(), "secret system", []domain.ChatMessage{{Role: "user", Content: "secret prompt"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(logs.String(), "AI model call") != 1 || strings.Contains(logs.String(), "secret prompt") || strings.Contains(logs.String(), "secret system") {
		t.Fatalf("unexpected logs = %s", logs.String())
	}
	var event map[string]any
	if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"call_id", "model", "input_tokens", "output_tokens", "total_tokens", "estimated_input_tokens", "component_token_count"} {
		if _, ok := event[key]; !ok {
			t.Fatalf("log missing %q: %#v", key, event)
		}
	}
}

func TestClientCompleteClassifiesOverflowAndOutputTruncation(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"provider details must not reach UI","type":"invalid_request_error","code":"context_length_exceeded"}}`))
		}))
		defer server.Close()
		result, err := NewClient("key", server.URL, "model").Complete(context.Background(), "system", []domain.ChatMessage{{Role: "user", Content: "content"}})
		if !errors.Is(err, ErrContextWindowExceeded) || result.Usage.ContextStatus != "context_window_exceeded" {
			t.Fatalf("result = %#v, error = %v", result, err)
		}
	})
	t.Run("output truncation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"id":"response-2","choices":[{"finish_reason":"length","message":{"content":"partial"}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`))
		}))
		defer server.Close()
		result, err := NewClient("key", server.URL, "model").Complete(context.Background(), "system", []domain.ChatMessage{{Role: "user", Content: "content"}})
		if !errors.Is(err, ErrTruncated) || result.Usage.InputTokens == nil || result.Usage.ContextStatus == "context_window_exceeded" {
			t.Fatalf("result = %#v, error = %v", result, err)
		}
	})
}

func TestClientCompleteEmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()
	_, err := NewClient("key", server.URL, "model").Complete(context.Background(), "system", []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "content"}})
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("error = %v", err)
	}
}

func TestClientCompleteUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad upstream"}}`, http.StatusBadGateway)
	}))
	defer server.Close()
	_, err := NewClient("key", server.URL, "model").Complete(context.Background(), "system", []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "content"}})
	if err == nil || !strings.Contains(err.Error(), "AI request failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestClientCompleteCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewClient("key", "http://127.0.0.1:1", "model").Complete(ctx, "system", []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "content"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestClientCompleteDeadlineContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := NewClient("key", server.URL, "model").Complete(ctx, "system", []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "content"}})
	if err == nil || !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v", err)
	}
}
