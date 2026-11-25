// Package obs holds logging, metrics and the token reference used to correlate
// this service's logs with new-api's audit log.
package obs

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// TokenRef is the SHA-256 hex digest of an access token, the same value
// new-api records as token_ref in its audit log. It is safe to log.
func TokenRef(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ShortRef is the first 12 characters of TokenRef, enough to search the
// audit log without printing the whole digest on every line.
func ShortRef(token string) string {
	if token == "" {
		return ""
	}
	return TokenRef(token)[:12]
}

// NewLogger returns a JSON logger at the given level (debug, info, warn, error).
func NewLogger(w io.Writer, level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: l}))
}

// Metrics are registered on a private registry served at /metrics.
type Metrics struct {
	Registry         *prometheus.Registry
	ToolCalls        *prometheus.CounterVec
	ToolDuration     *prometheus.HistogramVec
	UpstreamRequests *prometheus.CounterVec
	RateLimitWait    prometheus.Histogram
	Guard            *prometheus.CounterVec
}

func NewMetrics() *Metrics {
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		ToolCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcp_tool_calls_total", Help: "Tool calls by tool and result kind.",
		}, []string{"tool", "result"}),
		ToolDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "mcp_tool_duration_seconds", Help: "Tool call duration.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
		}, []string{"tool"}),
		UpstreamRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "newapi_requests_total", Help: "Requests to new-api by route and HTTP status.",
		}, []string{"route", "status"}),
		RateLimitWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "newapi_ratelimit_wait_seconds", Help: "Time spent waiting for the outbound rate limiter.",
			Buckets: []float64{0.001, 0.01, 0.1, 0.5, 1, 2, 5, 10},
		}),
		Guard: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcp_guard_total", Help: "Two-step change outcomes.",
		}, []string{"tool", "outcome"}),
	}
	m.Registry.MustRegister(m.ToolCalls, m.ToolDuration, m.UpstreamRequests, m.RateLimitWait, m.Guard,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}
