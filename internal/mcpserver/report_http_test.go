package mcpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportDownloadHandlerServesGeneratedReport(t *testing.T) {
	directory := t.TempDir()
	content := "# Diagnostic report\n\nEverything is healthy.\n"
	saved, err := saveDiagnosticReport(context.Background(), directory, SaveDiagnosticReportInput{Content: content, Format: "markdown"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewReportDownloadHandler(directory)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request := httptest.NewRequest(method, "/reports/"+saved.Path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		result := response.Result()
		body, readErr := io.ReadAll(result.Body)
		result.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if result.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", method, result.StatusCode)
		}
		if result.Header.Get("Content-Type") != "text/markdown; charset=utf-8" || result.Header.Get("X-Content-Type-Options") != "nosniff" || result.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s headers = %#v", method, result.Header)
		}
		if !strings.Contains(result.Header.Get("Content-Disposition"), saved.Path) {
			t.Fatalf("%s content disposition = %q", method, result.Header.Get("Content-Disposition"))
		}
		if method == http.MethodGet && string(body) != content {
			t.Fatalf("GET body = %q", body)
		}
		if method == http.MethodHead && len(body) != 0 {
			t.Fatalf("HEAD returned %d body bytes", len(body))
		}
	}
}

func TestReportDownloadHandlerRejectsUnsafeRequests(t *testing.T) {
	directory := t.TempDir()
	handler := NewReportDownloadHandler(directory)
	for _, target := range []string{
		"/reports/",
		"/reports/../secret",
		"/reports/%2e%2e%2fsecret",
		"/reports/arbitrary.md",
		"/reports/diagnostic-20260925T120000.000000000Z-0123456789abcdef0123456789abcdef.txt",
		"/reports/diagnostic-20260925T120000.000000000Z-0123456789abcdef0123456789abcdef.md/extra",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %q status = %d, want 404", target, response.Code)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/reports/anything", strings.NewReader("data"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST status = %d, Allow = %q", response.Code, response.Header().Get("Allow"))
	}
}

func TestReportDownloadHandlerRejectsSymlinkEscape(t *testing.T) {
	directory := t.TempDir()
	outsideDirectory := t.TempDir()
	target := filepath.Join(outsideDirectory, "secret.md")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "diagnostic-20260925T120000.000000000Z-0123456789abcdef0123456789abcdef.md"
	if err := os.Symlink(target, filepath.Join(directory, name)); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/reports/"+name, nil)
	response := httptest.NewRecorder()
	NewReportDownloadHandler(directory).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("symlink response = %d %q", response.Code, response.Body.String())
	}
}

func TestReportDownloadHandlerDisabledWithoutDirectory(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/reports/diagnostic-20260925T120000.000000000Z-0123456789abcdef0123456789abcdef.md", nil)
	response := httptest.NewRecorder()
	NewReportDownloadHandler("").ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}
