package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"NEWAPI_BASE_URL": "http://newapi.internal:3000/"}))
	require.NoError(t, err)
	assert.Equal(t, "http://newapi.internal:3000", cfg.BaseURL.String())
	assert.Equal(t, TransportHTTP, cfg.Transport)
	assert.Equal(t, ":8080", cfg.Listen)
	assert.Equal(t, LevelRead, cfg.MaxLevel)
	assert.Empty(t, cfg.Toolsets)
	assert.Equal(t, 1.5, cfg.UpstreamRPS)
	assert.Equal(t, 10, cfg.UpstreamBurst)
	assert.Equal(t, 30*time.Second, cfg.UpstreamTimeout)
}

func TestLoadRejects(t *testing.T) {
	secret := strings.Repeat("s", 32)
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"missing base url", map[string]string{}, "NEWAPI_BASE_URL is required"},
		{"non http base url", map[string]string{"NEWAPI_BASE_URL": "ftp://x"}, "http(s) URL"},
		{"credentials in base url", map[string]string{"NEWAPI_BASE_URL": "http://a:b@x"}, "must not carry"},
		{"pat in http mode", map[string]string{"NEWAPI_BASE_URL": "http://x", "NEWAPI_PAT": "nap_x"}, "must not be set in http mode"},
		{"stdio without pat", map[string]string{"NEWAPI_BASE_URL": "http://x", "MCP_TRANSPORT": "stdio"}, "required in stdio mode"},
		{"unknown transport", map[string]string{"NEWAPI_BASE_URL": "http://x", "MCP_TRANSPORT": "sse"}, "http or stdio"},
		{"unknown level", map[string]string{"NEWAPI_BASE_URL": "http://x", "MCP_ALLOW": "admin"}, "read, operate or write"},
		{"short secret", map[string]string{"NEWAPI_BASE_URL": "http://x", "MCP_ALLOW": "write", "MCP_CONFIRM_SECRET": secret[:31]}, "MCP_CONFIRM_SECRET"},
		{"bad rps", map[string]string{"NEWAPI_BASE_URL": "http://x", "MCP_UPSTREAM_RPS": "0"}, "MCP_UPSTREAM_RPS"},
		{"bad timeout", map[string]string{"NEWAPI_BASE_URL": "http://x", "MCP_UPSTREAM_TIMEOUT": "10"}, "MCP_UPSTREAM_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(tt.env))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestLoadWriteWithoutSecret(t *testing.T) {
	cfg, err := Load(env(map[string]string{"NEWAPI_BASE_URL": "http://x", "MCP_ALLOW": "write"}))
	require.NoError(t, err)
	assert.Empty(t, cfg.ConfirmSecret, "generated at start by the caller")
}

func TestLoadStdioAndWrite(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"NEWAPI_BASE_URL":    "https://llm.example.com",
		"MCP_TRANSPORT":      "stdio",
		"NEWAPI_PAT":         "nap_abc",
		"MCP_ALLOW":          "write",
		"MCP_CONFIRM_SECRET": strings.Repeat("k", 32),
		"MCP_TOOLSETS":       "channel, logs,,",
	}))
	require.NoError(t, err)
	assert.Equal(t, TransportStdio, cfg.Transport)
	assert.Equal(t, LevelWrite, cfg.MaxLevel)
	assert.Equal(t, []string{"channel", "logs"}, cfg.Toolsets)
}
