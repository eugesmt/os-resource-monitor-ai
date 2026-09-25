package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxDiagnosticReportBytes = 128 << 10

type SaveDiagnosticReportInput struct {
	Content string `json:"content"`
	Format  string `json:"format"`
}

type SaveDiagnosticReportOutput struct {
	ReportID     string `json:"report_id"`
	Path         string `json:"path"`
	DownloadURL  string `json:"download_url,omitempty"`
	Format       string `json:"format"`
	BytesWritten int    `json:"bytes_written"`
	SHA256       string `json:"sha256"`
}

type ServerOption func(*serverConfig)

type serverConfig struct {
	reportDirectory       string
	reportDownloadBaseURL string
}

// WithReportDownloadBaseURL adds an absolute HTTP download URL to successful
// save results. It is intended for the loopback Streamable HTTP transport.
func WithReportDownloadBaseURL(baseURL string) ServerOption {
	return func(config *serverConfig) {
		config.reportDownloadBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
}

// WithReportDirectory enables save_diagnostic_report and confines all reports
// to directory. Clients cannot provide or influence the destination path.
func WithReportDirectory(directory string) ServerOption {
	return func(config *serverConfig) {
		config.reportDirectory = strings.TrimSpace(directory)
	}
}

func saveDiagnosticReport(ctx context.Context, directory string, input SaveDiagnosticReportInput) (SaveDiagnosticReportOutput, error) {
	if err := ctx.Err(); err != nil {
		return SaveDiagnosticReportOutput{}, err
	}
	if strings.TrimSpace(input.Content) == "" {
		return SaveDiagnosticReportOutput{}, errors.New("content must not be empty")
	}
	if len(input.Content) > maxDiagnosticReportBytes {
		return SaveDiagnosticReportOutput{}, fmt.Errorf("content must not exceed %d bytes", maxDiagnosticReportBytes)
	}

	extension := ""
	switch input.Format {
	case "markdown":
		extension = ".md"
	case "json":
		if !json.Valid([]byte(input.Content)) {
			return SaveDiagnosticReportOutput{}, errors.New("content must be valid JSON when format is json")
		}
		extension = ".json"
	default:
		return SaveDiagnosticReportOutput{}, errors.New("format must be markdown or json")
	}
	if directory == "" {
		return SaveDiagnosticReportOutput{}, errors.New("diagnostic report storage is unavailable")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return SaveDiagnosticReportOutput{}, fmt.Errorf("create report directory: %w", err)
	}

	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return SaveDiagnosticReportOutput{}, fmt.Errorf("generate report ID: %w", err)
	}
	reportID := time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random)
	filename := "diagnostic-" + reportID + extension
	destination := filepath.Join(directory, filename)

	temporary, err := os.CreateTemp(directory, ".diagnostic-*.tmp")
	if err != nil {
		return SaveDiagnosticReportOutput{}, fmt.Errorf("create temporary report: %w", err)
	}
	temporaryName := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return SaveDiagnosticReportOutput{}, fmt.Errorf("set report permissions: %w", err)
	}
	if _, err := temporary.WriteString(input.Content); err != nil {
		_ = temporary.Close()
		return SaveDiagnosticReportOutput{}, fmt.Errorf("write report: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return SaveDiagnosticReportOutput{}, fmt.Errorf("sync report: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return SaveDiagnosticReportOutput{}, fmt.Errorf("close report: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return SaveDiagnosticReportOutput{}, err
	}
	// Hard-link publication is atomic and, unlike os.Rename on Unix, never
	// replaces an existing destination.
	if err := os.Link(temporaryName, destination); err != nil {
		return SaveDiagnosticReportOutput{}, fmt.Errorf("publish report: %w", err)
	}
	if err := os.Remove(temporaryName); err != nil {
		_ = os.Remove(destination)
		return SaveDiagnosticReportOutput{}, fmt.Errorf("remove temporary report: %w", err)
	}
	cleanup = false

	digest := sha256.Sum256([]byte(input.Content))
	return SaveDiagnosticReportOutput{
		ReportID:     reportID,
		Path:         filename,
		Format:       input.Format,
		BytesWritten: len(input.Content),
		SHA256:       hex.EncodeToString(digest[:]),
	}, nil
}
