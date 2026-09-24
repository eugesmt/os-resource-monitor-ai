package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"taskmanager-ai/internal/mcpserver"
	"taskmanager-ai/internal/taskmanagerapi"
)

const (
	defaultAPIURL      = "http://127.0.0.1:8080"
	defaultTransport   = "stdio"
	defaultHTTPAddress = "127.0.0.1:8090"
	httpEndpoint       = "/mcp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv); err != nil {
		log.Fatalf("taskmanager MCP stopped: %v", err)
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	baseURL := getenv("TASKMANAGER_API_URL")
	if baseURL == "" {
		baseURL = defaultAPIURL
	}
	api, err := taskmanagerapi.NewClient(baseURL)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	transport := strings.ToLower(strings.TrimSpace(getenv("TASKMANAGER_MCP_TRANSPORT")))
	if transport == "" {
		transport = defaultTransport
	}
	switch transport {
	case "stdio":
		err := mcpserver.NewServer(api).Run(ctx, &mcp.StdioTransport{MaxLineLength: 1 << 20})
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case "http":
		address := strings.TrimSpace(getenv("TASKMANAGER_MCP_ADDR"))
		if address == "" {
			address = defaultHTTPAddress
		}
		if err := validateLoopbackAddress(address); err != nil {
			return err
		}
		return runHTTP(ctx, address, mcpserver.NewStreamableHTTPHandler(api))
	default:
		return fmt.Errorf("unsupported TASKMANAGER_MCP_TRANSPORT %q: use stdio or http", transport)
	}
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid TASKMANAGER_MCP_ADDR: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("TASKMANAGER_MCP_ADDR must use a loopback host because HTTP mode has no authentication")
	}
	return nil
}

func runHTTP(ctx context.Context, address string, handler http.Handler) error {
	mux := http.NewServeMux()
	mux.Handle(httpEndpoint, handler)
	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("taskmanager MCP Streamable HTTP listening on http://%s%s", address, httpEndpoint)
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP transport: %w", err)
		}
		return nil
	}
}
