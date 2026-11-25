package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/newapi"
)

func TestLoadAndCheck(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	write("middleware/access_token_routes.go", `
		"GET /api/log/":            accessTokenScopeRule("log:read"),
		"POST /api/option/payment_compliance": accessTokenSessionRule,
		"POST /api/channel/:id/key": accessTokenScopeRule(service.AccessTokenScopeOf(authz.ChannelSecretView)),`)
	write("middleware/access_token_routes_private.go", `"GET /api/risk/suspicious": accessTokenScopeRule("user:read"),`)
	write("router/channel-router.go", `
	{method: http.MethodGet, path: "/", permission: authz.ChannelRead, handler: controller.GetAllChannels},
	{method: http.MethodPost, path: "/:id/status", permission: authz.ChannelOperate, handler: controller.UpdateChannelStatus},`)
	write("router/api-router.go", `
		apiRouter.GET("/status", controller.GetStatus)
		handlePermissionRoute(apiRouter.Group("", middleware.AdminAuth()), http.MethodGet, "/audit", authz.AuditRead, controller.GetAuditLogs)`)

	rules, err := Load(root)
	require.NoError(t, err)
	assert.Equal(t, Rule{Kind: "scope", Scope: "channel:secret_view"}, rules["POST /api/channel/:id/key"])
	assert.Equal(t, Rule{Kind: "scope", Scope: "channel:operate"}, rules["POST /api/channel/:id/status"])
	assert.Equal(t, Rule{Kind: "scope", Scope: "audit:read"}, rules["GET /api/audit"])
	assert.Equal(t, Rule{Kind: "anonymous"}, rules["GET /api/status"])

	problems := Check([]newapi.Route{
		{Method: "GET", Path: "/api/log/", Scope: "log:read"},                           // ok
		{Method: "GET", Path: "/api/channel/", Scope: "channel:read"},                   // ok
		{Method: "GET", Path: "/api/status"},                                            // ok
		{Method: "GET", Path: "/api/risk/suspicious", Scope: "user:write"},              // wrong scope
		{Method: "POST", Path: "/api/option/payment_compliance", Scope: "option:write"}, // session only
		{Method: "GET", Path: "/api/gone", Scope: "x:read"},                             // missing
	}, rules)
	assert.Len(t, problems, 3)
}

func TestPermissionScope(t *testing.T) {
	assert.Equal(t, "channel:sensitive_write", permissionScope("ChannelSensitiveWrite"))
	assert.Equal(t, "task_plugin:bind", permissionScope("TaskPluginBind"))
	assert.Equal(t, "audit:read", permissionScope("AuditRead"))
}
