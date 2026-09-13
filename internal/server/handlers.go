package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"taskmanager-ai/internal/ai"
	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
)

type apiError struct {
	Error string `json:"error"`
}
type aiRequest struct {
	Question string `json:"question"`
}
type aiResponse struct {
	Answer *domain.AnalysisResponse `json:"answer"`
	Usage  domain.AICallUsage       `json:"usage"`
}
type aiUsageErrorResponse struct {
	Error string             `json:"error"`
	Usage domain.AICallUsage `json:"usage"`
}
type historyResponse struct {
	Items []collector.SystemSnapshot `json:"items"`
}
type aiHistoryResponse struct {
	Items []domain.ChatMessage `json:"items"`
}

func RegisterRoutes(mux *http.ServeMux, state *State, webFS fs.FS) {
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
		ctx, cancel := context.WithTimeout(r.Context(), ai.RequestTimeout)
		defer cancel()
		answer, usage, err := state.AskAI(ctx, req.Question)
		if err != nil {
			if errors.Is(err, ai.ErrContextWindowExceeded) {
				state.log.Error("AI request failed", "error", err)
				writeJSON(w, http.StatusUnprocessableEntity, aiUsageErrorResponse{Error: "AI context window exceeded", Usage: usage})
				return
			}
			status := 502
			publicErr := errors.New("AI service temporarily unavailable")
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ai.ErrTimeout) {
				status = 504
				publicErr = errors.New("AI service timed out")
			} else if errors.Is(err, ErrAIUnavailable) || errors.Is(err, ErrSnapshotNotReady) || errors.Is(err, ai.ErrPromptNotConfigured) {
				status = 503
			} else if errors.Is(err, ai.ErrTruncated) {
				publicErr = errors.New("AI response was truncated")
			}
			state.log.Error("AI request failed", "error", err)
			writeAPIError(w, status, publicErr)
			return
		}
		writeJSON(w, 200, aiResponse{Answer: answer, Usage: usage})
	})
	mux.HandleFunc("/api/ai/history", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		items, err := state.GetAIHistory(r.Context())
		if err != nil {
			writeAPIError(w, 503, err)
			return
		}
		writeJSON(w, http.StatusOK, aiHistoryResponse{Items: items})
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

func serveEvents(w http.ResponseWriter, r *http.Request, state *State) {
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
