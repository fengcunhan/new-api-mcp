# new-api-mcp

[English](README.md) | 简体中文

把 [new-api](https://github.com/QuantumNous/new-api) 的管理后台接口包装成 [MCP](https://modelcontextprotocol.io) 工具，让 Claude Code、Codex 等 Agent 能查渠道、查日志、看用量、启停渠道、改定价。

- **不持有凭据**：每个调用方带着自己的 new-api 个人访问令牌（PAT，`nap_...`）访问，服务原样透传。能做什么完全由令牌在 new-api 里的权限决定，撤权立即生效，所有操作都记在 new-api 的审计日志里。
- **改动先预览**：配置类操作分两步。第一次调用只返回 diff 和 `confirm_token`；带上令牌再调一次才执行。如果预览之后目标被别人改过，执行会被拒绝。
- **不外泄密钥**：渠道 key、令牌明文从不出现在输出里；渠道代理地址和请求头覆盖里的凭据会被打码。
- **不提供高危操作**：没有删除渠道/用户、禁用用户、改角色、查看密钥这类工具。

设计说明见 [docs/design.md](docs/design.md)，架构见 [docs/architecture.md](docs/architecture.md)。

## 工具

| 域 | 只读 | 可逆操作 | 配置变更（两步确认） |
| --- | --- | --- | --- |
| ops | `server_status` `model_list` `model_missing` `group_list` `perf_stats` `system_info` | | |
| channel | `channel_list` `channel_get` `channel_routing` `channel_codex_usage` | `channel_test` `channel_set_status` `channel_multikey` `channel_fetch_upstream_models` | `channel_update` |
| logs | `logs_search` `logs_stat` `task_list` `audit_search` `affinity_cache_stats` | `affinity_cache_clear` | |
| user | `user_search` `user_get` | | `user_add_quota` `user_update_profile` |
| pricing / option | `pricing_get` `pricing_issues` `option_get` | | `pricing_update` `option_set` |
| report | `usage_quota_report` | | |

## 快速开始

```bash
docker build -t new-api-mcp .
docker run -d -p 8080:8080 -e NEWAPI_BASE_URL=https://your-new-api.example.com -e MCP_ALLOW=write -e TZ=Asia/Shanghai new-api-mcp
```

在 new-api 控制台「个人设置 → 访问令牌」新建令牌并勾选需要的 scope，然后接入 Claude Code：

```bash
claude mcp add --transport http new-api http://localhost:8080/mcp \
  --header "Authorization: Bearer nap_xxx"
```

让 Agent 先调用 `server_status`，能返回 new-api 版本和你的用户名就说明接通了。

本机也可以用 stdio 模式，不起 HTTP 服务：

```bash
MCP_TRANSPORT=stdio NEWAPI_BASE_URL=https://your-new-api.example.com NEWAPI_PAT=nap_xxx go run ./cmd/new-api-mcp
```

### 常用 scope

| 用途 | scope |
| --- | --- |
| 只读排障 | `profile:read` `channel:read` `log:read` `user:read` `model:read` `group:read` `audit:read` |
| 定价、配置、节点（root） | `option:read` `option:write` `ops:read` |
| 渠道操作 | `channel:operate` `channel:write` |
| 用户余额与资料 | `user:write` |

## 配置

| 变量 | 说明 | 默认 |
| --- | --- | --- |
| `NEWAPI_BASE_URL` | new-api 地址，必填 | — |
| `MCP_TRANSPORT` | `http` 或 `stdio` | `http` |
| `NEWAPI_PAT` | 仅 stdio 模式，必填；HTTP 模式禁止设置 | — |
| `MCP_LISTEN` | HTTP 监听地址 | `:8080` |
| `MCP_ALLOW` | 暴露哪些工具：`read` / `operate` / `write`，逐级包含 | `read` |
| `MCP_TOOLSETS` | 只暴露这些域，逗号分隔：`ops,channel,logs,user,pricing,option,report` | 全部 |
| `MCP_CONFIRM_SECRET` | 两步确认的签名密钥，≥32 字节；不配则启动时随机生成 | 随机 |
| `MCP_UPSTREAM_RPS` / `MCP_UPSTREAM_BURST` | 调 new-api 的限速 | `1.5` / `10` |
| `MCP_UPSTREAM_TIMEOUT` | 单次调用超时（`channel_test` 固定 150s） | `30s` |
| `MCP_LOG_LEVEL` | `debug` / `info` / `warn` / `error` | `info` |
| `TZ` | 输出时间所用的时区 | 系统时区 |

HTTP 端点：`POST /mcp`（需 `Authorization: Bearer nap_...`）、`GET /healthz`、`GET /readyz`、`GET /metrics`（Prometheus）。

**建议单副本运行。** new-api 默认按来源 IP 限流（`GLOBAL_API_RATE_LIMIT`，360 次 / 180 秒），对 new-api 来说本服务就是一个 IP，多副本只会让总请求速率翻倍。默认 1.5 rps 留了余量。

## 开发

```bash
go test ./...                                   # 单元与集成测试
go run ./tools/contractcheck -newapi ../new-api # 把每个工具声明的路由和 scope 与 new-api 源码逐条比对
go run ./cmd/new-api-mcp manifest               # 导出工具清单（JSON）
```

端到端测试会修改数据，只能对一次性的 new-api 实例跑，准备方法见 `e2e/e2e_test.go` 文件头：

```bash
NEWAPI_BASE_URL=http://127.0.0.1:3000 E2E_READ_PAT=nap_... E2E_WRITE_PAT=nap_... go test -tags e2e ./e2e/
```

新增工具的步骤：在 `internal/newapi/routes.go` 加路由常量 → 在 `internal/tools/<域>/` 写处理函数和 `tools.Spec` → 加进该域的 `Definitions()` → 跑契约检查。升级 new-api 后也先跑一次契约检查。

目前对照的 new-api 版本：`v1.0.0-rc.51`。
