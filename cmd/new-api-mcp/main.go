// Command new-api-mcp serves new-api admin tools over MCP.
//
//	new-api-mcp            serve (HTTP or stdio, from MCP_TRANSPORT)
//	new-api-mcp manifest   print every tool's spec as JSON
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fengcunhan/new-api-mcp/internal/catalog"
	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/obs"
	"github.com/fengcunhan/new-api-mcp/internal/server"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
	"github.com/fengcunhan/new-api-mcp/internal/tools/ops"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "manifest" {
		if err := printManifest(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "new-api-mcp:", err)
		os.Exit(1)
	}
}

func printManifest() error {
	specs := []tools.Spec{}
	for _, d := range catalog.All() {
		specs = append(specs, d.Spec)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"version": ops.Version, "tested_newapi_version": ops.TestedNewAPIVersion, "tools": specs})
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	// stdout carries the protocol in stdio mode, so logs always go to stderr.
	logger := obs.NewLogger(os.Stderr, cfg.LogLevel)
	metrics := obs.NewMetrics()

	api := newapi.New(newapi.Options{
		BaseURL: cfg.BaseURL, RPS: cfg.UpstreamRPS, Burst: cfg.UpstreamBurst, Timeout: cfg.UpstreamTimeout, Metrics: metrics,
	})
	deps := tools.Deps{API: api, Now: time.Now}
	if cfg.MaxLevel == config.LevelWrite {
		secret := cfg.ConfirmSecret
		if len(secret) == 0 {
			// Single replica: a per-process secret only means previews made
			// before a restart must be redone.
			secret = make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				return err
			}
		}
		deps.Guard = guard.New(secret, 5*time.Minute, time.Now, guard.Hooks{
			Outcome: func(tool, outcome string) { metrics.Guard.WithLabelValues(tool, outcome).Inc() },
		})
	}
	defs, err := catalog.Select(catalog.All(), cfg.MaxLevel, cfg.Toolsets)
	if err != nil {
		return err
	}

	credential := server.HeaderCredential
	if cfg.Transport == config.TransportStdio {
		credential = server.FixedCredential(cfg.PAT)
	}
	mcpServer := server.New(server.Options{
		Version: ops.Version, Definitions: defs, Deps: deps, Credential: credential,
		Timeout: cfg.UpstreamTimeout, Logger: logger, Metrics: metrics,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("starting", "version", ops.Version, "transport", cfg.Transport, "newapi", cfg.BaseURL.String(),
		"allow", cfg.MaxLevel.String(), "tools", len(defs), "upstream_rps", cfg.UpstreamRPS)

	if cfg.Transport == config.TransportStdio {
		return mcpServer.Run(ctx, &mcp.StdioTransport{})
	}

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.Handler(server.HTTPOptions{Server: mcpServer, API: api, Logger: logger, Metrics: metrics}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}
