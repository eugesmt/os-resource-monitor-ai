package mcpserver

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var diagnosticReportName = regexp.MustCompile(`^diagnostic-[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{32}\.(md|json)$`)

// NewReportDownloadHandler serves only generated diagnostic report files from
// directory. Rooted file access prevents symlink and traversal escapes.
func NewReportDownloadHandler(directory string) http.Handler {
	directory = strings.TrimSpace(directory)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/reports/")
		if directory == "" || !diagnosticReportName.MatchString(name) {
			http.NotFound(w, r)
			return
		}

		root, err := os.OpenRoot(directory)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer root.Close()
		file, err := root.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}

		contentType := "text/markdown; charset=utf-8"
		if strings.HasSuffix(name, ".json") {
			contentType = "application/json; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, name, info.ModTime(), file)
	})
}
