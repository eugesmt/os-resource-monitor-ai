package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"taskmanager-ai/internal/ai"
	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/storage"
)

type historyForTest struct{}

func (historyForTest) Load(context.Context) ([]domain.ChatMessage, error) {
	return []domain.ChatMessage{{ID: 1, Role: domain.ChatRoleUser, Content: "question"}}, nil
}
func (historyForTest) AppendTurn(context.Context, string, string, domain.AICallUsage) error {
	return nil
}

func TestAIHistoryHandlerReturnsPersistedMessages(t *testing.T) {
	state := NewState(collector.New(1, nil), storage.NewRingBuffer(1), nil)
	state.SetAIHistory(historyForTest{})
	mux := http.NewServeMux()
	RegisterRoutes(mux, state, nil)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/ai/history", nil)
	mux.ServeHTTP(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var response aiHistoryResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].Content != "question" {
		t.Fatalf("response = %#v", response)
	}
}

func TestAIAskHandlerReturnsTypedUsage(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"response","choices":[{"message":{"content":"{\"summary\":\"ok\",\"facts\":[],\"recommendations\":[]}"}}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`))
	}))
	defer provider.Close()
	state := readyAIState(provider.URL)
	mux := http.NewServeMux()
	RegisterRoutes(mux, state, nil)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/ai/ask", strings.NewReader(`{"question":"status?"}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response aiResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Answer == nil || response.Answer.Summary != "ok" || response.Usage.InputTokens == nil || *response.Usage.InputTokens != 12 {
		t.Fatalf("response = %#v", response)
	}
}

func TestAIAskHandlerHidesOverflowCause(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"secret upstream context details","type":"invalid_request_error","code":"context_length_exceeded"}}`))
	}))
	defer provider.Close()
	state := readyAIState(provider.URL)
	mux := http.NewServeMux()
	RegisterRoutes(mux, state, nil)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/ai/ask", strings.NewReader(`{"question":"status?"}`))
	mux.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "secret upstream") || !strings.Contains(recorder.Body.String(), "AI context window exceeded") {
		t.Fatalf("unsafe response = %s", recorder.Body.String())
	}
}

func readyAIState(providerURL string) *State {
	state := NewState(collector.New(1, nil), storage.NewRingBuffer(1), nil)
	state.snapshot = collector.SystemSnapshot{Timestamp: time.Now()}
	state.hasSnapshot = true
	state.SetAIAgent(ai.NewAgent(ai.NewClient("test-key", providerURL, "test-model"), "system", historyForTest{}))
	return state
}
