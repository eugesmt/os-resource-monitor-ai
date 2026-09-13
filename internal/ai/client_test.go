package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
		if !ok || len(messages) != 2 {
			t.Fatalf("messages = %v", request["messages"])
		}
		first, _ := messages[0].(map[string]any)
		second, _ := messages[1].(map[string]any)
		if first["role"] != "system" || first["content"] != "system" ||
			second["role"] != "user" || second["content"] != "content" {
			t.Fatalf("messages = %v", messages)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"  answer  "}}]}`))
	}))
	defer server.Close()

	answer, err := NewClient("key", server.URL, "test-model").Complete(context.Background(), "system", "content")
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if answer != "answer" {
		t.Fatalf("answer = %q, want answer", answer)
	}
}

func TestClientCompleteEmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()
	_, err := NewClient("key", server.URL, "model").Complete(context.Background(), "system", "content")
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("error = %v", err)
	}
}

func TestClientCompleteUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad upstream"}}`, http.StatusBadGateway)
	}))
	defer server.Close()
	_, err := NewClient("key", server.URL, "model").Complete(context.Background(), "system", "content")
	if err == nil || !strings.Contains(err.Error(), "AI request failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestClientCompleteCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewClient("key", "http://127.0.0.1:1", "model").Complete(ctx, "system", "content")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestClientCompleteDeadlineContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := NewClient("key", server.URL, "model").Complete(ctx, "system", "content")
	if err == nil || !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v", err)
	}
}
