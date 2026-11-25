# new-api-mcp

English | [简体中文](README.zh-CN.md)

An [MCP](https://modelcontextprotocol.io) server that exposes the admin API of [new-api](https://github.com/QuantumNous/new-api) as tools, so agents such as Claude Code and Codex can inspect channels, search logs, read usage reports, enable or disable channels and update model pricing.

- **Holds no credentials.** Every caller brings their own new-api personal access token (PAT, `nap_...`) and the server forwards it unchanged. What a caller can do is decided entirely by that token's permissions in new-api; revoking a permission takes effect immediately, and every action is recorded in new-api's audit log.
- **Changes are previewed first.** Configuration changes take two calls. The first returns a diff and a `confirm_token`; only a second call with the token applies the change. If the target changed after the preview, the change is refused.
- **Never leaks secrets.** Channel keys and token values never appear in output; credentials in channel proxy URLs and header overrides are masked.
- **No high-risk operations.** There are no tools to delete channels or users, disable users, change roles or reveal keys.

See [docs/design.en.md](docs/design.en.md) for the design and [docs/architecture.en.md](docs/architecture.en.md) for the architecture.

## Tools

| Toolset | Read | Reversible operations | Configuration changes (two-step) |
| --- | --- | --- | --- |
| ops | `server_status` `model_list` `model_missing` `group_list` `perf_stats` `system_info` | | |
| channel | `channel_list` `channel_get` `channel_routing` `channel_codex_usage` | `channel_test` `channel_set_status` `channel_multikey` `channel_fetch_upstream_models` | `channel_update` |
| logs | `logs_search` `logs_stat` `task_list` `audit_search` `affinity_cache_stats` | `affinity_cache_clear` | |
| user | `user_search` `user_get` | | `user_add_quota` `user_update_profile` |
| pricing / option | `pricing_get` `pricing_issues` `option_get` | | `pricing_update` `option_set` |
| report | `usage_quota_report` | | |

## Quick start

```bash
docker build -t new-api-mcp .
docker run -d -p 8080:8080 -e NEWAPI_BASE_URL=https://your-new-api.example.com -e MCP_ALLOW=write -e TZ=UTC new-api-mcp
```

Create an access token in the new-api console (personal settings → access tokens) with the scopes you need, then add the server to Claude Code:

```bash
claude mcp add --transport http new-api http://localhost:8080/mcp \
  --header "Authorization: Bearer nap_xxx"
```

Ask the agent to call `server_status` first. If it returns the new-api version and your username, you are connected.

You can also run it locally over stdio without an HTTP server:

```bash
MCP_TRANSPORT=stdio NEWAPI_BASE_URL=https://your-new-api.example.com NEWAPI_PAT=nap_xxx go run ./cmd/new-api-mcp
```

### Common scopes

| Purpose | Scopes |
| --- | --- |
| Read-only troubleshooting | `profile:read` `channel:read` `log:read` `user:read` `model:read` `group:read` `audit:read` |
| Pricing, options, nodes (root) | `option:read` `option:write` `ops:read` |
| Channel operations | `channel:operate` `channel:write` |
| User balance and profile | `user:write` |

## Configuration

| Variable | Description | Default |
| --- | --- | --- |
| `NEWAPI_BASE_URL` | new-api address, required | — |
| `MCP_TRANSPORT` | `http` or `stdio` | `http` |
| `NEWAPI_PAT` | stdio mode only, required there; must not be set in HTTP mode | — |
| `MCP_LISTEN` | HTTP listen address | `:8080` |
| `MCP_ALLOW` | Which tools to expose: `read` / `operate` / `write`, each including the previous | `read` |
| `MCP_TOOLSETS` | Expose only these toolsets, comma-separated: `ops,channel,logs,user,pricing,option,report` | all |
| `MCP_CONFIRM_SECRET` | Signing key for two-step confirmation, at least 32 bytes; generated at start when unset | random |
| `MCP_UPSTREAM_RPS` / `MCP_UPSTREAM_BURST` | Rate limit for calls to new-api | `1.5` / `10` |
| `MCP_UPSTREAM_TIMEOUT` | Per-call timeout (`channel_test` always uses 150s) | `30s` |
| `MCP_LOG_LEVEL` | `debug` / `info` / `warn` / `error` | `info` |
| `TZ` | Timezone used for times in output | system timezone |

HTTP endpoints: `POST /mcp` (requires `Authorization: Bearer nap_...`), `GET /healthz`, `GET /readyz`, `GET /metrics` (Prometheus).

**Run a single replica.** new-api rate-limits by source IP by default (`GLOBAL_API_RATE_LIMIT`, 360 requests per 180 seconds). To new-api this server is one IP, so extra replicas only multiply the total request rate. The default of 1.5 rps leaves headroom.

## Development

```bash
go test ./...                                   # unit and integration tests
go run ./tools/contractcheck -newapi ../new-api # compare every tool's routes and scopes with the new-api source
go run ./cmd/new-api-mcp manifest               # print the tool manifest as JSON
```

End-to-end tests modify data, so run them only against a disposable new-api instance. See the header of `e2e/e2e_test.go` for the required setup:

```bash
NEWAPI_BASE_URL=http://127.0.0.1:3000 E2E_READ_PAT=nap_... E2E_WRITE_PAT=nap_... go test -tags e2e ./e2e/
```

To add a tool: add the route constant to `internal/newapi/routes.go` → write the handler and its `tools.Spec` under `internal/tools/<toolset>/` → add it to that toolset's `Definitions()` → run the contract check. Run the contract check after every new-api upgrade as well.

Checked against new-api `v1.0.0-rc.51`.
