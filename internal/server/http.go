package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/obs"
)

const maxRequestBody = 1 << 20

var errNoBearer = errors.New("Send the new-api access token as Authorization: Bearer nap_... in the MCP client configuration.")

// bearerToken returns the nap_ token from an Authorization header value.
func bearerToken(header string) (string, bool) {
	token, ok := strings.CutPrefix(strings.TrimSpace(header), "Bearer ")
	token = strings.TrimSpace(token)
	if !ok || !strings.HasPrefix(token, "nap_") || len(token) > 128 {
		return "", false
	}
	return token, true
}

// HeaderCredential reads the token the HTTP client sent with this request.
func HeaderCredential(req *mcp.CallToolRequest) (string, error) {
	if req.Extra == nil || req.Extra.Header == nil {
		return "", errNoBearer
	}
	token, ok := bearerToken(req.Extra.Header.Get("Authorization"))
	if !ok {
		return "", errNoBearer
	}
	return token, nil
}

// FixedCredential always returns the token given at start, for stdio.
func FixedCredential(token string) CredentialFunc {
	return func(*mcp.CallToolRequest) (string, error) { return token, nil }
}

// HTTPOptions configures the HTTP front.
type HTTPOptions struct {
	Server  *mcp.Server
	API     newapi.Client
	Logger  *slog.Logger
	Metrics *obs.Metrics
}

// Handler returns the HTTP handler serving /mcp, /healthz, /readyz and /metrics.
func Handler(opts HTTPOptions) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return opts.Server }, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		PropagateRequestCancellation: true,
		MaxRequestBodyBytes:          maxRequestBody,
		Logger:                       opts.Logger,
	})
	mux := http.NewServeMux()
	mux.Handle("/mcp", requireBearer(http.NewCrossOriginProtection().Handler(streamable)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := opts.API.Do(ctx, newapi.Request{Route: newapi.RouteStatus}, nil); err != nil {
			http.Error(w, "new-api unreachable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(opts.Metrics.Registry, promhttp.HandlerOpts{}))
	return accessLog(opts.Logger, mux)
}

// requireBearer rejects MCP requests without an access token before they
// reach the SDK, so an unauthenticated client cannot even list tools.
func requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := bearerToken(r.Header.Get("Authorization")); !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="new-api-mcp"`)
			http.Error(w, errNoBearer.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
			return
		}
		token, _ := bearerToken(r.Header.Get("Authorization"))
		logger.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(), "remote", r.RemoteAddr, "token_ref", obs.ShortRef(token))
	})
}
