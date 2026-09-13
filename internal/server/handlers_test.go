package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
	"taskmanager-ai/internal/storage"
)

type historyForTest struct{}

func (historyForTest) Load(context.Context) ([]domain.ChatMessage, error) {
	return []domain.ChatMessage{{ID: 1, Role: domain.ChatRoleUser, Content: "question"}}, nil
}
func (historyForTest) AppendTurn(context.Context, string, string) error { return nil }

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
