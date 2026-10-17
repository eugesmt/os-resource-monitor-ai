package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testCompletionRequest() CompletionRequest {
	return CompletionRequest{Messages: []ModelMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "historical user"},
		{Role: "assistant", Content: "historical assistant"},
		{Role: "user", Content: "current user"},
	}, MaxTokens: DefaultMaxTokens}
}

func TestNewClientCompatibilityWrapperUsesNeutralDefaults(t *testing.T) {
	client := NewClient("key", "http://example.com", "")
	config := client.Config()
	if config.Model != "default-model" || !config.JSONMode || config.ResponseFormat != "json_object" {
		t.Fatalf("compatibility client config = %#v", config)
	}
}

func TestClientCompleteSendsModelAndMessages(t *testing.T) {
	server := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request["model"] != "test-model" {
			t.Errorf("model = %v", request["model"])
		}
		if request["max_tokens"] != float64(DefaultMaxTokens) {
			t.Errorf("max_tokens = %v", request["max_tokens"])
		}
		format, _ := request["response_format"].(map[string]any)
		if format["type"] != "json_object" {
			t.Errorf("response_format = %v", format)
		}
		messages, ok := request["messages"].([]any)
		if !ok || len(messages) != 4 {
			t.Fatalf("messages = %v", request["messages"])
		}
		want := []ModelMessage{{Role: "system", Content: "system"}, {Role: "user", Content: "historical user"}, {Role: "assistant", Content: "historical assistant"}, {Role: "user", Content: "current user"}}
		for i, expected := range want {
			message, _ := messages[i].(map[string]any)
			if message["role"] != expected.Role || message["content"] != expected.Content {
				t.Fatalf("messages = %v", messages)
			}
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"test-model","usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5},"choices":[{"finish_reason":"stop","message":{"content":"  answer  "}}]}`))
	}))
	defer server.Close()

	answer, err := NewClient("key", server.URL, "test-model").Complete(context.Background(), testCompletionRequest())
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if answer.Content != "answer" || answer.FinishReason != "stop" || !answer.Usage.Available || answer.Usage.TotalTokens != 5 || answer.Latency <= 0 {
		t.Fatalf("answer = %#v, want stopped answer", answer)
	}
}

func TestClientCompleteOmitsResponseFormatWhenJSONModeDisabled(t *testing.T) {
	server := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if _, ok := request["response_format"]; ok {
			t.Errorf("response_format = %v, want omitted", request["response_format"])
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"local-model","choices":[{"finish_reason":"stop","message":{"content":"answer"}}]}`))
	}))
	defer server.Close()

	client := NewClientWithOptions(ClientOptions{APIKey: "", BaseURL: server.URL, Model: "local-model", JSONMode: false})
	if client.Config().JSONMode || client.Config().ResponseFormat != "" {
		t.Fatalf("config = %#v, want JSON mode disabled", client.Config())
	}
	if _, err := client.Complete(context.Background(), testCompletionRequest()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
}

func TestClientCompleteEmptyChoices(t *testing.T) {
	server := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()
	result, err := NewClient("key", server.URL, "model").Complete(context.Background(), testCompletionRequest())
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("error = %v", err)
	}
	if result.Model != "model" || result.ConfigVersion == "" || result.Latency <= 0 || result.Usage.Available {
		t.Fatalf("empty response metadata = %#v", result)
	}
}

func TestClientCompleteRejectsAbnormalFinishReasons(t *testing.T) {
	for _, reason := range []string{"length", "content_filter", "tool_calls", "aborted"} {
		t.Run(reason, func(t *testing.T) {
			server := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"` + reason + `","message":{"content":"{}"}}]}`))
			}))
			defer server.Close()
			_, err := NewClient("key", server.URL, "model").Complete(context.Background(), testCompletionRequest())
			if err == nil {
				t.Fatal("Complete() error = nil")
			}
			if reason == "length" && !errors.Is(err, ErrTruncated) {
				t.Fatalf("error = %v", err)
			}
			if reason == "content_filter" && !errors.Is(err, ErrContentFiltered) {
				t.Fatalf("error = %v", err)
			}
			if reason == "tool_calls" && !errors.Is(err, ErrToolCalls) {
				t.Fatalf("error = %v", err)
			}
			if reason == "aborted" && !errors.Is(err, ErrAbnormalFinish) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestClientCompleteUpstreamError(t *testing.T) {
	server := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad upstream"}}`, http.StatusBadGateway)
	}))
	defer server.Close()
	result, err := NewClient("key", server.URL, "model").Complete(context.Background(), testCompletionRequest())
	if err == nil || !strings.Contains(err.Error(), "AI request failed") {
		t.Fatalf("error = %v", err)
	}
	if result.Model != "model" || result.ConfigVersion == "" || result.Latency <= 0 || result.Usage.Available {
		t.Fatalf("upstream error metadata = %#v", result)
	}
}

func TestClientCompleteCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := NewClient("key", "http://127.0.0.1:1", "model").Complete(ctx, testCompletionRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if result.Model != "model" || result.ConfigVersion == "" || result.Latency <= 0 || result.Usage.Available {
		t.Fatalf("cancelled metadata = %#v", result)
	}
}

func TestClientCompleteDeadlineContext(t *testing.T) {
	server := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, err := NewClient("key", server.URL, "model").Complete(ctx, testCompletionRequest())
	if err == nil || !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v", err)
	}
	if result.Model != "model" || result.ConfigVersion == "" || result.Latency <= 0 || result.Usage.Available {
		t.Fatalf("timeout metadata = %#v", result)
	}
}

type testServer struct {
	server   *http.Server
	URL      string
	listener net.Listener
}

func (s *testServer) Close() { _ = s.server.Close() }

func newTestServer(t *testing.T, handler http.Handler) *testServer {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	return &testServer{server: server, URL: "http://" + listener.Addr().String(), listener: listener}
}
