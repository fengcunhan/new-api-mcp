//go:build e2e

// End-to-end tests against a real new-api. They change data, so point them
// only at a disposable instance:
//
//	NEWAPI_BASE_URL=http://127.0.0.1:3911 E2E_READ_PAT=nap_... E2E_WRITE_PAT=nap_... go test -tags e2e ./e2e/
//
// The write token needs channel:read/operate/write, user:read/write,
// option:read/write, log:read and profile:read; the instance needs channel 1
// (single key), channel 2 (multi-key, 3 keys) and a user named alice.
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
	"github.com/fengcunhan/new-api-mcp/internal/server"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
)

func env(t *testing.T, key string) string {
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s not set", key)
	}
	return v
}

func start(t *testing.T) string {
	base, err := url.Parse(env(t, "NEWAPI_BASE_URL"))
	require.NoError(t, err)
	metrics := obs.NewMetrics()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := newapi.New(newapi.Options{BaseURL: base, RPS: 50, Burst: 50, Timeout: 30 * time.Second, Metrics: metrics})
	defs, err := catalog.Select(catalog.All(), config.LevelWrite, nil)
	require.NoError(t, err)
	deps := tools.Deps{API: api, Now: time.Now, Guard: guard.New([]byte(strings.Repeat("e", 32)), 5*time.Minute, nil, guard.Hooks{})}
	srv := server.New(server.Options{Version: "e2e", Definitions: defs, Deps: deps, Credential: server.HeaderCredential, Timeout: 60 * time.Second, Logger: logger, Metrics: metrics})
	front := httptest.NewServer(server.Handler(server.HTTPOptions{Server: srv, API: api, Logger: logger, Metrics: metrics}))
	t.Cleanup(front.Close)
	return front.URL + "/mcp"
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

type session struct {
	t  *testing.T
	cs *mcp.ClientSession
}

func connect(t *testing.T, endpoint, token string) session {
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: bearer{token}}}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return session{t: t, cs: cs}
}

// call returns the result text and whether it was a tool error.
func (s session) call(name string, args map[string]any) (string, bool) {
	s.t.Helper()
	res, err := s.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(s.t, err, name)
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String(), res.IsError
}

func (s session) ok(name string, args map[string]any) map[string]any {
	s.t.Helper()
	text, isErr := s.call(name, args)
	require.False(s.t, isErr, "%s failed: %s", name, text)
	var out map[string]any
	require.NoError(s.t, json.Unmarshal([]byte(text), &out), text)
	return out
}

// confirm runs a write tool's preview and then applies it.
func (s session) confirm(name string, args map[string]any) map[string]any {
	s.t.Helper()
	preview := s.ok(name, args)
	require.Equal(s.t, false, preview["applied"], "%s preview applied: %v", name, preview)
	token, _ := preview["confirm_token"].(string)
	require.NotEmpty(s.t, token, "%s preview has no token: %v", name, preview)
	withToken := map[string]any{"confirm_token": token}
	for k, v := range args {
		withToken[k] = v
	}
	done := s.ok(name, withToken)
	require.Equal(s.t, true, done["applied"], "%s not applied: %v", name, done)
	return done
}

func TestEveryReadTool(t *testing.T) {
	s := connect(t, start(t), env(t, "E2E_READ_PAT"))
	status := s.ok("server_status", nil)
	assert.NotEmpty(t, status["newapi_version"])

	list := s.ok("channel_list", map[string]any{})
	encoded, _ := json.Marshal(list)
	assert.NotContains(t, string(encoded), "sk-")
	detail := s.ok("channel_get", map[string]any{"id": 2})
	assert.NotNil(t, detail["multi_key"])
	encoded, _ = json.Marshal(detail)
	assert.NotContains(t, string(encoded), "sk-")

	for name, args := range map[string]map[string]any{
		"channel_routing":      {},
		"logs_search":          {"hours": 24},
		"logs_stat":            {"hours": 24},
		"task_list":            {},
		"audit_search":         {},
		"affinity_cache_stats": {},
		"user_search":          {"keyword": "alice"},
		"pricing_get":          {},
		"pricing_issues":       {},
		"option_get":           {"keys": []string{"RetryTimes"}},
		"usage_quota_report":   {"group_by": "user"},
		"model_list":           {},
		"model_missing":        {},
		"group_list":           {},
		"perf_stats":           {},
		"system_info":          {},
	} {
		s.ok(name, args)
	}
	users := s.ok("user_search", map[string]any{"keyword": "alice"})
	require.NotEmpty(t, users["items"])
	id := users["items"].([]any)[0].(map[string]any)["id"]
	s.ok("user_get", map[string]any{"id": id})

	// The read token has no write scope: new-api itself refuses, and the
	// hint names the missing scope.
	text, isErr := s.call("channel_set_status", map[string]any{"ids": []int{1}, "enabled": false})
	assert.True(t, isErr)
	assert.Contains(t, text, `scope "channel:operate"`) // reading the channel first is allowed; the write is not
}

func TestOperateAndWriteTools(t *testing.T) {
	s := connect(t, start(t), env(t, "E2E_WRITE_PAT"))

	// Channel status round trip.
	off := s.ok("channel_set_status", map[string]any{"ids": []int{1}, "enabled": false})
	assert.Equal(t, "manually_disabled", off["channels"].([]any)[0].(map[string]any)["after"])
	on := s.ok("channel_set_status", map[string]any{"ids": []int{1}, "enabled": true})
	assert.Equal(t, "enabled", on["channels"].([]any)[0].(map[string]any)["after"])

	// A failed upstream test is a result, not a tool error.
	test := s.ok("channel_test", map[string]any{"id": 1})
	assert.Equal(t, false, test["ok"])

	// Multi-key: disable key 1, see it, enable it again.
	s.ok("channel_multikey", map[string]any{"id": 2, "action": "disable_key", "key_index": 1})
	keys := s.ok("channel_multikey", map[string]any{"id": 2, "action": "status"})
	assert.EqualValues(t, 1, keys["manually_disabled"])
	s.ok("channel_multikey", map[string]any{"id": 2, "action": "enable_all_keys"})

	// Channel update: preview, apply, and a replayed token is refused.
	priority := time.Now().Unix() % 1000
	args := map[string]any{"id": 1, "priority": priority, "add_models": []string{"e2e-extra"}}
	preview := s.ok("channel_update", args)
	token := preview["confirm_token"].(string)
	withToken := map[string]any{"id": 1, "priority": priority, "add_models": []string{"e2e-extra"}, "confirm_token": token}
	assert.Equal(t, true, s.ok("channel_update", withToken)["applied"])
	got := s.ok("channel_get", map[string]any{"id": 1})
	assert.EqualValues(t, priority, got["priority"])
	assert.Contains(t, got["models"], "e2e-extra")
	encoded, _ := json.Marshal(got)
	assert.NotContains(t, string(encoded), "sk-e2e")
	replay, _ := s.call("channel_update", withToken)
	assert.Contains(t, replay, "Nothing to change")
	s.confirm("channel_update", map[string]any{"id": 1, "remove_models": []string{"e2e-extra"}})

	// Option patch keeps other entries.
	before := s.ok("option_get", map[string]any{"keys": []string{"GroupRatio"}})
	s.confirm("option_set", map[string]any{"key": "GroupRatio", "set": map[string]any{"e2e": 0.5}})
	after := s.ok("option_get", map[string]any{"keys": []string{"GroupRatio"}})
	var beforeMap, afterMap map[string]any
	require.NoError(t, json.Unmarshal([]byte(before["values"].(map[string]any)["GroupRatio"].(string)), &beforeMap))
	require.NoError(t, json.Unmarshal([]byte(after["values"].(map[string]any)["GroupRatio"].(string)), &afterMap))
	for k, v := range beforeMap {
		assert.Equal(t, v, afterMap[k], "entry %s must survive", k)
	}
	assert.Equal(t, 0.5, afterMap["e2e"])
	s.confirm("option_set", map[string]any{"key": "GroupRatio", "unset": []string{"e2e"}})
	_, refused := s.call("option_set", map[string]any{"key": "ModelRatio", "value": "{}"})
	assert.True(t, refused)

	// Pricing for one model.
	ratio := 1 + float64(time.Now().UnixMicro()%1000)/1000
	s.confirm("pricing_update", map[string]any{"model": "gpt-6.1", "set": map[string]any{"ModelRatio": ratio, "CompletionRatio": 8}})
	priced := s.ok("pricing_get", map[string]any{"models": []string{"gpt-6.1"}})
	configured := priced["models"].([]any)[0].(map[string]any)["configured"].(map[string]any)
	assert.Equal(t, ratio, configured["ModelRatio"])

	// User balance and daily counters.
	users := s.ok("user_search", map[string]any{"keyword": "alice"})
	id := users["items"].([]any)[0].(map[string]any)["id"]
	s.confirm("user_add_quota", map[string]any{"id": id, "amount_usd": 1})
	s.confirm("user_update_profile", map[string]any{"id": id, "remark": "e2e " + time.Now().Format(time.TimeOnly)})

	s.ok("affinity_cache_clear", map[string]any{"all": true})
	// These depend on upstream accounts that a disposable
	// instance lacks; they must still fail with a readable tool error.
	for name, args := range map[string]map[string]any{
		"channel_fetch_upstream_models": {"id": 1},
		"channel_codex_usage":           {"id": 1},
	} {
		text, isErr := s.call(name, args)
		t.Logf("%s error=%v: %.200s", name, isErr, text)
		assert.NotEmpty(t, text)
	}

	// Every change above went through this token and is in new-api's audit log.
	audit := s.ok("audit_search", map[string]any{"token_ref": obs.TokenRef(env(t, "E2E_WRITE_PAT"))})
	assert.NotEmpty(t, audit["items"])
}
