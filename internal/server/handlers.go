package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"system-monitor-ai/internal/ai"
	"system-monitor-ai/internal/collector"
	"system-monitor-ai/internal/domain"
	"system-monitor-ai/internal/invariants"
	"system-monitor-ai/internal/memory"
	"system-monitor-ai/internal/storage"
)

type apiError struct {
	Error string `json:"error"`
}
type aiRequest struct {
	Question string `json:"question"`
}
type aiResponse struct {
	Answer    *domain.AnalysisResponse `json:"answer"`
	TaskState *domain.TaskState        `json:"task_state"`
}
type historyResponse struct {
	Items []collector.SystemSnapshot `json:"items"`
}
type conversationHistoryResponse struct {
	Items []domain.ConversationEvent `json:"items"`
}
type memoryRequest struct {
	Layer   memory.Layer `json:"layer"`
	Scope   string       `json:"scope"`
	Kind    string       `json:"kind"`
	Content string       `json:"content"`
}
type memoryResponse struct {
	Context memory.Context `json:"context"`
}
type profileRequest struct {
	UserID      string   `json:"user_id"`
	Style       string   `json:"style"`
	Format      string   `json:"format"`
	Constraints []string `json:"constraints"`
}
type invariantsRequest struct {
	Items []invariants.Invariant `json:"items"`
}
type taskStateRequest struct {
	TaskID  string                 `json:"task_id"`
	Version int64                  `json:"version"`
	Action  domain.TaskStateAction `json:"action"`
}

const (
	minMetricsSummaryDuration = 3 * time.Minute
	maxMetricsSummaryDuration = 7 * 24 * time.Hour
	maxMetricsSummaryMinutes  = 7 * 24 * 60
)

func RegisterRoutes(mux *http.ServeMux, state *Runtime, webFS fs.FS) {
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		snapshot, err := state.GetSnapshot()
		if err != nil {
			writeAPIError(w, 503, err)
			return
		}
		writeJSON(w, 200, snapshot)
	})
	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		limit := 120
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				limit = n
			}
		}
		writeJSON(w, 200, historyResponse{Items: state.GetHistory(limit)})
	})
	mux.HandleFunc("/api/metrics/summary", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		from, to, err := parseMetricsSummaryRange(r.URL.Query(), time.Now().UTC())
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		summary, err := state.MetricsSummary(r.Context(), from, to)
		if err != nil {
			state.log.Error("metrics summary failed", "error", err)
			writeAPIError(w, http.StatusServiceUnavailable, errors.New("metrics summary unavailable"))
			return
		}
		writeJSON(w, http.StatusOK, summary)
	})
	mux.HandleFunc("/api/ai/ask", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		defer r.Body.Close()
		var req aiRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		if err := dec.Decode(&req); err != nil {
			writeAPIError(w, 400, errors.New("invalid JSON body"))
			return
		}
		if len([]rune(strings.TrimSpace(req.Question))) == 0 {
			writeAPIError(w, 400, errors.New("question is empty"))
			return
		}
		current, stateErr := state.TaskState(r.Context())
		if stateErr == nil && current.Phase != domain.TaskPhaseDone {
			if current.Status != domain.TaskStatusActive || current.ExpectedActionKind != domain.TaskActionKindUser {
				writeJSON(w, http.StatusConflict, struct {
					Error     string            `json:"error"`
					TaskState *domain.TaskState `json:"task_state"`
				}{Error: "task is already running", TaskState: &current})
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), ai.RequestTimeout)
			answer, err := state.AskAI(ctx, req.Question)
			cancel()
			if err == nil {
				updated, loadErr := state.TaskState(r.Context())
				if loadErr == nil && updated.Status == domain.TaskStatusActive && (updated.ExpectedActionKind == domain.TaskActionKindAgent || updated.ExpectedActionKind == domain.TaskActionKindTool) {
					state.scheduleTask(updated.TaskID, "")
				}
			}
			if err == nil {
				updated, loadErr := state.TaskState(r.Context())
				if loadErr != nil {
					writeAPIError(w, http.StatusServiceUnavailable, errors.New("task state unavailable"))
					return
				}
				writeJSON(w, http.StatusOK, aiResponse{Answer: answer, TaskState: &updated})
				return
			}
			// Fall through to the shared error mapping below.
			if err != nil {
				status := 502
				publicErr := errors.New("AI service temporarily unavailable")
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ai.ErrTimeout) {
					status, publicErr = 504, errors.New("AI service timed out")
				}
				state.log.Error("AI request failed", "error", err)
				writeAPIError(w, status, publicErr)
				return
			}
		}
		created, err := state.StartTaskRun(r.Context(), req.Question)
		if err == nil {
			writeJSON(w, http.StatusAccepted, aiResponse{TaskState: &created})
			return
		}
		if errors.Is(err, ErrTaskAlreadyOpen) {
			writeAPIError(w, http.StatusConflict, errors.New("task is already running"))
			return
		}
		state.log.Error("task start failed", "error", err)
		writeAPIError(w, http.StatusServiceUnavailable, errors.New("task start temporarily unavailable"))
	})
	mux.HandleFunc("/api/ai/history", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		items, err := state.GetConversationHistory(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, errors.New("conversation history temporarily unavailable"))
			return
		}
		writeJSON(w, http.StatusOK, conversationHistoryResponse{Items: items})
	})
	mux.HandleFunc("/api/ai/task/state", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			state, err := state.TaskState(r.Context())
			if err != nil {
				if errors.Is(err, storage.ErrTaskNotFound) {
					writeAPIError(w, http.StatusNotFound, errors.New("no task has been started"))
				} else {
					writeAPIError(w, http.StatusServiceUnavailable, errors.New("task state unavailable"))
				}
				return
			}
			writeJSON(w, http.StatusOK, state)
		case http.MethodPost:
			defer r.Body.Close()
			var req taskStateRequest
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, errors.New("invalid JSON body"))
				return
			}
			if req.Action != domain.TaskActionPause && req.Action != domain.TaskActionResume {
				writeAPIError(w, http.StatusBadRequest, errors.New("task phase transitions are agent-owned"))
				return
			}
			if strings.TrimSpace(req.TaskID) == "" || req.Version < 1 {
				writeAPIError(w, http.StatusBadRequest, errors.New("task_id and version are required"))
				return
			}
			updated, err := state.UpdateTaskState(r.Context(), req.TaskID, req.Version, req.Action)
			if err != nil {
				if errors.Is(err, storage.ErrTaskStateVersionConflict) {
					writeAPIError(w, http.StatusConflict, errors.New("task state changed; retry with current state"))
				} else if errors.Is(err, storage.ErrTaskNotFound) {
					writeAPIError(w, http.StatusNotFound, errors.New("task not found"))
				} else {
					writeAPIError(w, http.StatusBadRequest, err)
				}
				return
			}
			writeJSON(w, http.StatusOK, updated)
		default:
			methodNotAllowed(w)
		}
	})
	mux.HandleFunc("/api/ai/task/continue", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		defer r.Body.Close()
		var req taskStateRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, errors.New("invalid JSON body"))
			return
		}
		if strings.TrimSpace(req.TaskID) == "" || req.Version < 1 {
			writeAPIError(w, http.StatusBadRequest, errors.New("task_id and version are required"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), ai.RequestTimeout)
		defer cancel()
		answer, err := state.ContinueTask(ctx, req.TaskID, req.Version)
		if err != nil {
			if paused, ok := err.(*TaskPausedError); ok {
				writeJSON(w, http.StatusConflict, struct {
					Error     string            `json:"error"`
					TaskState *domain.TaskState `json:"task_state"`
				}{Error: "task is paused; resume it before continuing", TaskState: &paused.State})
				return
			}
			if errors.Is(err, storage.ErrTaskStateVersionConflict) {
				writeAPIError(w, http.StatusConflict, errors.New("task state changed; retry with current state"))
			} else if errors.Is(err, storage.ErrTaskNotFound) {
				writeAPIError(w, http.StatusNotFound, errors.New("task not found"))
			} else if errors.Is(err, ErrTaskNotRunnable) || errors.Is(err, ai.ErrInvalidResponse) || errors.Is(err, ai.ErrSemanticValidation) {
				writeAPIError(w, http.StatusBadRequest, errors.New("AI returned an invalid task step; retry continuation"))
			} else {
				state.log.Error("task continuation failed", "error", err)
				writeAPIError(w, http.StatusServiceUnavailable, errors.New("task continuation temporarily unavailable"))
			}
			return
		}
		current, err := state.TaskState(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, errors.New("task state unavailable"))
			return
		}
		writeJSON(w, http.StatusOK, aiResponse{Answer: answer, TaskState: &current})
	})
	mux.HandleFunc("/api/ai/task/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		defer r.Body.Close()
		var req taskStateRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, errors.New("invalid JSON body"))
			return
		}
		if strings.TrimSpace(req.TaskID) == "" || req.Version < 1 {
			writeAPIError(w, http.StatusBadRequest, errors.New("task_id and version are required"))
			return
		}
		updated, err := state.CancelTask(r.Context(), req.TaskID, req.Version)
		if err != nil {
			if errors.Is(err, storage.ErrTaskStateVersionConflict) {
				writeAPIError(w, http.StatusConflict, errors.New("task state changed; retry with current state"))
			} else if errors.Is(err, storage.ErrTaskNotFound) {
				writeAPIError(w, http.StatusNotFound, errors.New("task not found"))
			} else {
				writeAPIError(w, http.StatusBadRequest, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, updated)
	})
	mux.HandleFunc("/api/ai/task/transitions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
		if taskID == "" {
			writeAPIError(w, http.StatusBadRequest, errors.New("task_id is required"))
			return
		}
		items, err := state.TaskTransitions(r.Context(), taskID)
		if err != nil {
			if errors.Is(err, storage.ErrTaskNotFound) {
				writeAPIError(w, http.StatusNotFound, errors.New("task not found"))
			} else {
				writeAPIError(w, http.StatusServiceUnavailable, errors.New("task transitions unavailable"))
			}
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Items []domain.TaskTransition `json:"items"`
		}{Items: items})
	})
	mux.HandleFunc("/api/ai/task/result", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		current, err := state.TaskState(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusNotFound, errors.New("task not found"))
			return
		}
		if current.Phase != domain.TaskPhaseDone || current.Outcome != domain.TaskOutcomeCompleted {
			writeAPIError(w, http.StatusConflict, errors.New("task result is not ready"))
			return
		}
		events, err := state.GetConversationHistory(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, errors.New("task result temporarily unavailable"))
			return
		}
		for index := len(events) - 1; index >= 0; index-- {
			if events[index].Role == domain.EventRoleAssistant {
				writeJSON(w, http.StatusOK, map[string]string{"content": events[index].Content})
				return
			}
		}
		writeAPIError(w, http.StatusNotFound, errors.New("task result not found"))
	})
	mux.HandleFunc("/api/ai/task", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		created, err := state.StartTask(r.Context())
		if err != nil {
			if errors.Is(err, ErrTaskAlreadyOpen) {
				writeAPIError(w, http.StatusConflict, err)
			} else {
				writeAPIError(w, http.StatusServiceUnavailable, errors.New("task creation unavailable"))
			}
			return
		}
		writeJSON(w, http.StatusCreated, created)
	})
	mux.HandleFunc("/api/ai/profile", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, state.Profile())
		case http.MethodPut:
			defer r.Body.Close()
			var req profileRequest
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, errors.New("invalid JSON body"))
				return
			}
			profile := memory.Profile{UserID: req.UserID, Style: req.Style, Format: req.Format, Constraints: req.Constraints}
			if err := profile.Validate(); err != nil {
				writeAPIError(w, http.StatusBadRequest, err)
				return
			}
			if err := state.SetProfile(r.Context(), profile); err != nil {
				state.log.Error("profile storage failed", "error", err)
				writeAPIError(w, http.StatusServiceUnavailable, errors.New("profile storage unavailable"))
				return
			}
			writeJSON(w, http.StatusOK, state.Profile())
		default:
			methodNotAllowed(w)
		}
	})
	mux.HandleFunc("/api/ai/profiles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		profiles, err := state.Profiles(r.Context())
		if err != nil {
			state.log.Error("profiles list failed", "error", err)
			writeAPIError(w, http.StatusServiceUnavailable, errors.New("profiles temporarily unavailable"))
			return
		}
		writeJSON(w, http.StatusOK, profiles)
	})
	mux.HandleFunc("/api/ai/invariants", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, struct {
				Items []invariants.Invariant `json:"items"`
			}{Items: state.Invariants()})
		case http.MethodPut:
			defer r.Body.Close()
			var req invariantsRequest
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
				writeAPIError(w, http.StatusBadRequest, errors.New("invalid JSON body"))
				return
			}
			if err := state.ReplaceInvariants(r.Context(), req.Items); err != nil {
				writeAPIError(w, http.StatusBadRequest, err)
				return
			}
			writeJSON(w, http.StatusOK, struct {
				Items []invariants.Invariant `json:"items"`
			}{Items: state.Invariants()})
		default:
			methodNotAllowed(w)
		}
	})
	mux.HandleFunc("/api/ai/invariants/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			methodNotAllowed(w)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/ai/invariants/")
		if id == "" || strings.Contains(id, "/") {
			writeAPIError(w, http.StatusBadRequest, errors.New("invariant id is required"))
			return
		}
		if err := state.DeleteInvariant(r.Context(), id); err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeAPIError(w, http.StatusNotFound, err)
				return
			}
			state.log.Error("invariant deletion failed", "error", err)
			writeAPIError(w, http.StatusServiceUnavailable, errors.New("invariant storage unavailable"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/ai/memory", func(w http.ResponseWriter, r *http.Request) {
		model := state.MemoryModel()
		if r.Method == http.MethodGet {
			conversationID := state.ConversationID()
			selected := model.BuildContext(memory.MemorySelection{ConversationID: conversationID, TaskID: conversationID, LongTermScope: conversationID, IncludeShort: true, IncludeWorking: true, IncludeLong: true})
			if events, err := state.GetConversationHistory(r.Context()); err == nil {
				selected.ShortTerm = make([]memory.Record, 0, len(events))
				for _, event := range events {
					selected.ShortTerm = append(selected.ShortTerm, memory.Record{ID: fmt.Sprint(event.ID), Layer: memory.ShortTerm, Scope: event.ConversationID, Kind: string(event.Role), Content: event.Content, CreatedAt: event.CreatedAt})
				}
			}
			writeJSON(w, http.StatusOK, memoryResponse{Context: selected})
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		defer r.Body.Close()
		var req memoryRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, errors.New("invalid JSON body"))
			return
		}
		var (
			record memory.Record
			err    error
		)
		switch req.Layer {
		case memory.ShortTerm:
			record, err = model.RememberShortTerm(state.ConversationID(), req.Kind, req.Content)
		case memory.Working:
			record, err = model.SetWorking(state.ConversationID(), req.Kind, req.Content)
		case memory.LongTerm:
			record, err = model.RememberLongTerm(state.ConversationID(), req.Kind, req.Content)
		default:
			err = errors.New("invalid memory layer")
		}
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if err := state.SaveMemory(r.Context(), record); err != nil {
			model.Remove(record.ID)
			writeAPIError(w, http.StatusServiceUnavailable, errors.New("memory storage unavailable"))
			return
		}
		writeJSON(w, http.StatusCreated, record)
	})
	mux.HandleFunc("/api/ai/memory/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			methodNotAllowed(w)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/ai/memory/")
		if id == "" || strings.Contains(id, "/") {
			writeAPIError(w, http.StatusBadRequest, errors.New("memory id is required"))
			return
		}
		if err := state.DeleteMemory(r.Context(), id); err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, errors.New("memory storage unavailable"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		serveEvents(w, r, state)
	})
	mux.Handle("/", spaHandler(webFS))
}

func parseMetricsSummaryRange(query url.Values, now time.Time) (time.Time, time.Time, error) {
	allowed := map[string]bool{"last_minutes": true, "from": true, "to": true}
	for key, values := range query {
		if !allowed[key] {
			return time.Time{}, time.Time{}, fmt.Errorf("unknown query parameter %q", key)
		}
		if len(values) != 1 {
			return time.Time{}, time.Time{}, fmt.Errorf("query parameter %q must be specified once", key)
		}
	}
	lastValues, hasLast := query["last_minutes"]
	fromValues, hasFrom := query["from"]
	toValues, hasTo := query["to"]
	if hasLast && (hasFrom || hasTo) {
		return time.Time{}, time.Time{}, errors.New("use last_minutes or from and to, not both")
	}
	if !hasLast && (!hasFrom || !hasTo) {
		return time.Time{}, time.Time{}, errors.New("provide last_minutes or both from and to")
	}
	if hasLast {
		minutes, err := strconv.Atoi(lastValues[0])
		if err != nil || minutes < 3 || minutes > maxMetricsSummaryMinutes {
			return time.Time{}, time.Time{}, fmt.Errorf("last_minutes must be between 3 and %d", maxMetricsSummaryMinutes)
		}
		to := now.UTC()
		return to.Add(-time.Duration(minutes) * time.Minute), to, nil
	}
	from, err := time.Parse(time.RFC3339, fromValues[0])
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("from must be a valid RFC3339 timestamp")
	}
	to, err := time.Parse(time.RFC3339, toValues[0])
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("to must be a valid RFC3339 timestamp")
	}
	from, to = from.UTC(), to.UTC()
	if to.After(now.UTC()) {
		return time.Time{}, time.Time{}, errors.New("to cannot be in the future")
	}
	duration := to.Sub(from)
	if duration < minMetricsSummaryDuration || duration > maxMetricsSummaryDuration {
		return time.Time{}, time.Time{}, errors.New("time range must be between 3 minutes and 7 days")
	}
	return from, to, nil
}

func serveEvents(w http.ResponseWriter, r *http.Request, state *Runtime) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, 500, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsubscribe := state.Subscribe()
	defer unsubscribe()
	if snapshot, err := state.GetSnapshot(); err == nil {
		writeSSE(w, snapshot)
		flusher.Flush()
	}
	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case snapshot, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, snapshot)
			flusher.Flush()
		case <-keepAlive.C:
			_, _ = w.Write([]byte(": keep-alive\n\n"))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
func writeSSE(w http.ResponseWriter, snapshot collector.SystemSnapshot) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("event: snapshot\ndata: "))
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n\n"))
}
func spaHandler(webFS fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." || clean == "" {
			clean = "index.html"
		}

		if data, err := fs.ReadFile(webFS, clean); err == nil {
			if ct := mime.TypeByExtension(path.Ext(clean)); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			http.ServeContent(w, r, path.Base(clean), time.Time{}, bytes.NewReader(data))
			return
		}

		index, err := fs.ReadFile(webFS, "index.html")
		if err != nil {
			http.Error(w, "frontend not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeAPIError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, apiError{Error: err.Error()})
}
func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, POST")
	w.WriteHeader(http.StatusMethodNotAllowed)
}
