package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"system-monitor-ai/internal/ai"
	"system-monitor-ai/internal/collector"
	"system-monitor-ai/internal/domain"
	"system-monitor-ai/internal/memory"
	"system-monitor-ai/internal/storage"
)

type historyStub struct {
	items []domain.ConversationEvent
	err   error
}

type profileStoreStub struct {
	profiles map[string]memory.Profile
}

func (s *profileStoreStub) SaveProfile(_ context.Context, profile memory.Profile) error {
	if s.profiles == nil {
		s.profiles = make(map[string]memory.Profile)
	}
	profile.Constraints = append([]string(nil), profile.Constraints...)
	s.profiles[profile.UserID] = profile
	return nil
}

func (s *profileStoreStub) ListProfiles(context.Context) ([]memory.Profile, error) {
	profiles := make([]memory.Profile, 0, len(s.profiles))
	for _, profile := range s.profiles {
		profile.Constraints = append([]string(nil), profile.Constraints...)
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func (s *historyStub) AppendUser(context.Context, string, string, string, string) (domain.ConversationEvent, error) {
	return domain.ConversationEvent{}, errors.New("not implemented")
}

func (s *historyStub) AppendAssistant(context.Context, string, string, string, string) (domain.ConversationEvent, error) {
	return domain.ConversationEvent{}, errors.New("not implemented")
}

func (s *historyStub) ListEvents(_ context.Context, conversationID string) ([]domain.ConversationEvent, error) {
	if conversationID != storage.DefaultConversationID {
		return nil, errors.New("unexpected conversation")
	}
	return s.items, s.err
}

func (s *historyStub) ListRecentEvents(_ context.Context, conversationID string, beforeEventID int64, limit int) ([]domain.ConversationEvent, error) {
	if conversationID != storage.DefaultConversationID {
		return nil, errors.New("unexpected conversation")
	}
	if s.err != nil {
		return nil, s.err
	}
	items := make([]domain.ConversationEvent, 0, limit)
	for i := len(s.items) - 1; i >= 0 && len(items) < limit; i-- {
		if s.items[i].ID < beforeEventID {
			items = append(items, s.items[i])
		}
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return items, nil
}

func TestStateGetSnapshotDoesNotAliasSnapshotSlices(t *testing.T) {
	state := NewRuntime(nil, storage.NewRingBuffer(1), nil)
	state.state.snapshot = collector.SystemSnapshot{CPUPerCore: []float64{10}, GPU: []collector.GPUInfo{{Name: "gpu"}}, MissingData: []string{"gpu"}}
	state.state.hasSnapshot = true

	got, err := state.GetSnapshot()
	if err != nil {
		t.Fatalf("GetSnapshot() error = %v", err)
	}
	got.CPUPerCore[0] = 99
	got.GPU[0].Name = "changed"
	got.MissingData[0] = "changed"
	want, err := state.GetSnapshot()
	if err != nil {
		t.Fatalf("second GetSnapshot() error = %v", err)
	}
	if want.CPUPerCore[0] != 10 || want.GPU[0].Name != "gpu" || want.MissingData[0] != "gpu" {
		t.Fatalf("state snapshot aliases internal slices: %#v", want)
	}
}

func TestConversationHistoryEndpointReturnsOrderedEvents(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	items := []domain.ConversationEvent{
		{ID: 1, ConversationID: "default", TurnID: "turn-1", Role: domain.EventRoleUser, Content: "question", SnapshotID: "snap-1", CreatedAt: now},
		{ID: 2, ConversationID: "default", TurnID: "turn-1", Role: domain.EventRoleAssistant, Content: `{"diagnosis_schema_version":"diagnosis.v1"}`, SnapshotID: "snap-1", CreatedAt: now.Add(time.Second)},
	}
	runtime := NewRuntime(nil, storage.NewRingBuffer(1), nil)
	runtime.SetHistoryStore(&historyStub{items: items})
	mux := http.NewServeMux()
	RegisterRoutes(mux, runtime, fstest.MapFS{"index.html": {Data: []byte("ok")}})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ai/history", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("status=%d content-type=%q body=%s", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
	var response conversationHistoryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Items) != 2 || response.Items[0].ID != 1 || response.Items[1].ID != 2 || response.Items[0].Content != "question" || !response.Items[1].CreatedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("response items = %#v", response.Items)
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/ai/history", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", recorder.Code)
	}
}

func TestInvariantEndpointsCreateListAndDelete(t *testing.T) {
	runtime := NewRuntime(nil, storage.NewRingBuffer(1), nil)
	mux := http.NewServeMux()
	RegisterRoutes(mux, runtime, fstest.MapFS{"index.html": {Data: []byte("ok")}})
	body := strings.NewReader(`{"items":[{"id":"stack-no-kubernetes","category":"stack","statement":"Kubernetes is forbidden","enabled":true}]}`)
	put := httptest.NewRecorder()
	mux.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/api/ai/invariants", body))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", put.Code, put.Body.String())
	}
	if got := runtime.Invariants(); len(got) != 1 || got[0].ID != "stack-no-kubernetes" {
		t.Fatalf("invariants=%#v", got)
	}
	remove := httptest.NewRecorder()
	mux.ServeHTTP(remove, httptest.NewRequest(http.MethodDelete, "/api/ai/invariants/stack-no-kubernetes", nil))
	if remove.Code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d body=%s", remove.Code, remove.Body.String())
	}
	if got := runtime.Invariants(); len(got) != 0 {
		t.Fatalf("invariants after delete=%#v", got)
	}
	missing := httptest.NewRecorder()
	mux.ServeHTTP(missing, httptest.NewRequest(http.MethodDelete, "/api/ai/invariants/stack-no-kubernetes", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing DELETE status=%d", missing.Code)
	}
}

func TestConversationHistoryEndpointHidesStorageError(t *testing.T) {
	runtime := NewRuntime(nil, storage.NewRingBuffer(1), nil)
	runtime.SetHistoryStore(&historyStub{err: errors.New("database path and details")})
	mux := http.NewServeMux()
	RegisterRoutes(mux, runtime, fstest.MapFS{"index.html": {Data: []byte("ok")}})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ai/history", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "conversation history temporarily unavailable") || strings.Contains(recorder.Body.String(), "database path") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestProfileEndpointStoresAndReturnsPreferences(t *testing.T) {
	runtime := NewRuntime(nil, storage.NewRingBuffer(1), nil)
	runtime.SetProfileStore(&profileStoreStub{})
	mux := http.NewServeMux()
	RegisterRoutes(mux, runtime, fstest.MapFS{"index.html": {Data: []byte("ok")}})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/ai/profile", bytes.NewBufferString(`{"user_id":"user-1","style":"concise","format":"bullets","constraints":["answer in Russian"]}`))
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ai/profile", nil))
	var profile map[string]any
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &profile) != nil {
		t.Fatalf("GET status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if profile["user_id"] != "user-1" || profile["style"] != "concise" || profile["format"] != "bullets" {
		t.Fatalf("profile = %#v", profile)
	}

	recorder = httptest.NewRecorder()
	memoryRequest := httptest.NewRequest(http.MethodPost, "/api/ai/memory", bytes.NewBufferString(`{"layer":"long_term","scope":"other-user","kind":"preference","content":"private"}`))
	memoryRequest.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, memoryRequest)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("memory POST status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var record struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &record); err != nil || record.Scope != "profile:user-1" {
		t.Fatalf("memory scope = %#v, err=%v", record, err)
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/ai/profiles", nil))
	var profiles []memory.Profile
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &profiles) != nil || len(profiles) != 1 || profiles[0].UserID != "user-1" {
		t.Fatalf("profiles response status=%d body=%s profiles=%#v", recorder.Code, recorder.Body.String(), profiles)
	}
}

func TestRuntimeProfileSelectsIsolatedConversation(t *testing.T) {
	runtime := NewRuntime(nil, storage.NewRingBuffer(1), nil)
	if got := runtime.ConversationID(); got != storage.DefaultConversationID {
		t.Fatalf("default conversation ID = %q", got)
	}
	if err := runtime.SetProfile(context.Background(), memory.Profile{UserID: "profile-user"}); err != nil {
		t.Fatal(err)
	}
	if got := runtime.ConversationID(); got != "profile:profile-user" {
		t.Fatalf("profile conversation ID = %q, want profile:profile-user", got)
	}
}

func TestAskAIRejectsInvalidOrStaleInputBeforePersistence(t *testing.T) {
	history := &historyStub{}
	runtime := NewRuntime(nil, storage.NewRingBuffer(1), nil)
	runtime.SetHistoryStore(history)
	runtime.SetAIAgent(ai.NewAgent(ai.NewClient("unused-key", "http://127.0.0.1:1", "unused-model"), ai.DefaultSystemPrompt, history))

	if _, err := runtime.AskAI(context.Background(), "question"); !errors.Is(err, ErrSnapshotNotReady) {
		t.Fatalf("unavailable snapshot error = %v", err)
	}
	runtime.state.snapshot = collector.SystemSnapshot{SnapshotID: "snap-stale", CapturedAt: time.Now().Add(-time.Minute), Timestamp: time.Now().Add(-time.Minute)}
	runtime.state.hasSnapshot = true
	if _, err := runtime.AskAI(context.Background(), "question"); !errors.Is(err, ErrSnapshotNotReady) {
		t.Fatalf("stale snapshot error = %v", err)
	}
	runtime.state.snapshot.CapturedAt = time.Now()
	runtime.state.snapshot.Timestamp = time.Now()
	if _, err := runtime.AskAI(context.Background(), "  "); err == nil || err.Error() != "question is empty" {
		t.Fatalf("invalid question error = %v", err)
	}
	if len(history.items) != 0 {
		t.Fatalf("rejected inputs persisted events: %#v", history.items)
	}
}
