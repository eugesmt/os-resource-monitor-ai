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

	"taskmanager-ai/internal/collector"
)

type apiError struct {
	Error string `json:"error"`
}
type aiRequest struct {
	Question string `json:"question"`
}
type aiResponse struct {
	Answer string `json:"answer"`
}
type historyResponse struct {
	Items []collector.SystemSnapshot `json:"items"`
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
		ctx, cancel := context.WithTimeout(r.Context(), 31*time.Second)
		defer cancel()
		answer, err := state.AskAI(ctx, req.Question)
		if err != nil {
			status := 502
			if errors.Is(err, context.DeadlineExceeded) {
				status = 504
			}
			if errors.Is(err, ErrAIUnavailable) {
				status = 503
			}
			if errors.Is(err, ErrSnapshotNotReady) {
				status = 503
			}
			writeAPIError(w, status, err)
			return
		}
		writeJSON(w, 200, aiResponse{Answer: answer})
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
