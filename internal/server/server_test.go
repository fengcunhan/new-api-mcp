package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/catalog"
	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/obs"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
)

// fakeNewAPI is a minimal new-api: it records the Authorization header and
// answers a few routes.
func fakeNewAPI(t *testing.T, seenAuth *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seenAuth = append(*seenAuth, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"version":"v1.0.0-rc.51","quota_per_unit":500000}}`))
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"root","role":100}}`))
		case "/api/channel/7":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"code":"ACCESS_TOKEN_SCOPE_DENIED","message":"lacks channel:read"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type headerTransport struct {
	token string
	base  http.RoundTripper
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if h.token != "" {
		r.Header.Set("Authorization", "Bearer "+h.token)
	}
	return h.base.RoundTrip(r)
}

func startMCP(t *testing.T, newapiURL string, level config.Level) *httptest.Server {
	t.Helper()
	base, err := url.Parse(newapiURL)
	require.NoError(t, err)
	metrics := obs.NewMetrics()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := newapi.New(newapi.Options{BaseURL: base, RPS: 100, Burst: 100, Timeout: 5 * time.Second, Metrics: metrics})
	defs, err := catalog.Select(catalog.All(), level, nil)
	require.NoError(t, err)
	deps := tools.Deps{API: api, Now: time.Now, Guard: guard.New([]byte(strings.Repeat("s", 32)), time.Minute, nil, guard.Hooks{})}
	srv := New(Options{Version: "test", Definitions: defs, Deps: deps, Credential: HeaderCredential, Timeout: 5 * time.Second, Logger: logger, Metrics: metrics})
	front := httptest.NewServer(Handler(HTTPOptions{Server: srv, API: api, Logger: logger, Metrics: metrics}))
	t.Cleanup(front.Close)
	return front
}

func connect(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   endpoint + "/mcp",
		HTTPClient: &http.Client{Transport: headerTransport{token: token, base: http.DefaultTransport}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestHTTPRejectsMissingToken(t *testing.T) {
	var seen []string
	front := startMCP(t, fakeNewAPI(t, &seen).URL, config.LevelRead)
	resp, err := http.Post(front.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Empty(t, seen, "nothing may reach new-api without a token")
}

func TestToolListFollowsLevel(t *testing.T) {
	var seen []string
	upstream := fakeNewAPI(t, &seen)
	readNames := listNames(t, connect(t, startMCP(t, upstream.URL, config.LevelRead).URL, "nap_reader"))
	writeNames := listNames(t, connect(t, startMCP(t, upstream.URL, config.LevelWrite).URL, "nap_writer"))
	assert.Contains(t, readNames, "channel_list")
	assert.NotContains(t, readNames, "channel_set_status")
	assert.NotContains(t, readNames, "pricing_update")
	assert.Contains(t, writeNames, "pricing_update")
	assert.Len(t, writeNames, len(catalog.All()))
}

func listNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	res, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Name == "channel_list" {
			assert.True(t, tool.Annotations.ReadOnlyHint)
		}
	}
	slices.Sort(names)
	return names
}

func TestCallForwardsCallerTokenAndMapsErrors(t *testing.T) {
	var seen []string
	front := startMCP(t, fakeNewAPI(t, &seen).URL, config.LevelRead)
	session := connect(t, front.URL, "nap_alice")

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "server_status"})
	require.NoError(t, err)
	require.False(t, res.IsError, textOf(res))
	var status map[string]any
	require.NoError(t, json.Unmarshal([]byte(textOf(res)), &status))
	assert.Equal(t, "v1.0.0-rc.51", status["newapi_version"])
	assert.Equal(t, "root", status["caller_username"])
	assert.Contains(t, seen, "Bearer nap_alice")

	res, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "channel_get", Arguments: map[string]any{"id": 7}})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(res), `scope "channel:read"`)

	res, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "channel_get", Arguments: map[string]any{"id": "x"}})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, textOf(res), "Invalid arguments")
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func TestBearerToken(t *testing.T) {
	for header, want := range map[string]bool{
		"Bearer nap_abc": true, "  Bearer nap_abc ": true, "nap_abc": false, "Bearer sk-abc": false, "Basic nap_abc": false, "": false,
	} {
		_, ok := bearerToken(header)
		assert.Equal(t, want, ok, header)
	}
}
