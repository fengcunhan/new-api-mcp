# Design

English | [简体中文](design.md)

This document explains what new-api-mcp is for, why it is designed the way it is, and what it deliberately does not do. For the implementation structure, see [architecture.en.md](architecture.en.md).

## 1. Goals and non-goals

**Goal**: a standalone MCP server that wraps new-api's admin `/api/*` endpoints as task-oriented tools, so an agent can handle day-to-day troubleshooting and operations: why a channel was disabled, which requests in the logs are slow, enabling or disabling channels, pricing a newly added model, ranking usage, and so on.

**Non-goals**:

- No changes to new-api itself and no direct database access; only its HTTP admin API is used.
- No wrapping of the `/v1/*` relay endpoints. Those are model calls; an agent can call them directly with an API key.
- No attempt to cover every console route. Payment, OAuth, passkey, registration and sign-in endpoints are out of scope.
- No way to read secrets in plain text (channel keys, user tokens).

## 2. Key facts

The following facts drive the whole design. They all come from the new-api source (`middleware/access_token_routes.go`, `docs/authentication.md` and others).

### 2.1 Authentication: personal access tokens (PATs)

- The console supports PATs: `nap_` followed by 43 random characters, sent as `Authorization: Bearer <pat>`.
- A PAT must be created with scopes (of the form `resource:action`) and an expiry. A user can only grant permissions they currently hold, and **roles and permissions are re-read on every request**, so revoking a permission takes effect immediately.
- **Every console route declares the scope it requires**; routes without a declaration reject PATs (`ACCESS_TOKEN_ROUTE_UNDECLARED`).
- A few routes accept only browser sign-in sessions and can never be called with a PAT, for example managing PATs themselves. As a result **a PAT cannot look up its own scopes**, so the MCP server cannot know a caller's permissions in advance and relies on the 403 responses from new-api.

### 2.2 Additional security verification

High-risk operations such as deleting or disabling users, promoting or demoting admins, resetting passwords or 2FA, and revealing channel keys require a short-lived `X-Security-Proof` request header. A PAT can obtain a proof through `POST /api/verify`, but only with a password or 2FA, not OAuth.

These operations are left out of v1 and stay manual in the console.

### 2.3 Auditing and rate limiting

- new-api audits every PAT request, recording the user, role and `token_ref` (the SHA-256 digest of the PAT). As long as everyone uses their own PAT, "who changed what through an agent" can be looked up in new-api's audit log; the MCP server needs no audit store of its own.
- `/api/*` has a global rate limit per source IP (360 requests per 180 seconds by default). When all agents go through one MCP instance they share its egress IP, so the MCP server must rate-limit itself.
- `GET /api/option/` drops secret options on the server side, so reading options through the MCP server cannot leak secrets.

### 2.4 Endpoint behaviour that shapes the tools

- `PUT /api/channel/` updates only the fields present in the request, but rejects a body containing `status`; enabling and disabling must use `POST /api/channel/:id/status`.
- `PUT /api/option/` **replaces the whole value**. Writing an option whose value is a JSON object, such as `GroupRatio`, directly would wipe the whole table; it must be read, merged and written back.
- `PATCH /api/option/model_pricing` updates per model and requires each model's current `expected_version`, which gives optimistic locking for free. Price changes should use it.
- `PUT /api/user/` overwrites username, display name, group and remark together; changing one of them still requires sending the other three.
- The OpenAPI file in the repository is incomplete and its authentication notes are outdated, so **tools cannot be generated from it**; the tool list has to be maintained by hand.
- Responses are always `{success, message, data}`, and many business failures still return HTTP 200, so `success` must be checked.

## 3. Design decisions

| Decision | Choice | Reason |
| --- | --- | --- |
| Code location | Separate repository | Depends only on the HTTP contract, adds no merge burden to new-api, releases independently |
| Language | Go + the official `modelcontextprotocol/go-sdk` | Single binary, easy to deploy |
| Transport | Streamable HTTP (stateless) first, stdio from the same binary | One shared instance for many users; stateless restarts are free; stdio for local debugging |
| Authentication | Forward the caller's PAT | Permissions, revocation and auditing all reuse new-api; a compromised server holds no long-lived credentials |
| Tool shape | Hand-written, task-oriented tools | Exposing every route would flood the model's context and pass pitfalls like whole-value replacement straight to the model |
| Writes | Two-step confirmation | A human sees the diff before execution, and concurrent changes after the preview are caught |

About forwarding the PAT: the MCP specification discourages passing upstream tokens through. Here the MCP server is a thin facade over new-api's admin API, and forwarding is the simplest option with the clearest permission semantics. To serve untrusted third-party agent platforms, switch to MCP OAuth with new-api as the authorization server; the tool layer would not change.

## 4. Tool levels

Each tool belongs to a toolset (ops, channel, logs, user, pricing, option, report) and to one of three levels:

- **Read**: queries that change nothing.
- **Reversible operations**: enabling or disabling channels, testing channels, managing keys of multi-key channels, clearing the channel affinity cache. They run immediately and report the state before and after.
- **Configuration changes**: channel settings, pricing, system options, user balance and profile. Always two-step.

The `MCP_ALLOW` setting decides the highest level exposed and `MCP_TOOLSETS` decides which toolsets are exposed. They only narrow the tool list; **the actual permission is always the caller's PAT permission in new-api**, and the MCP server adds no permission checks of its own.

Deliberately absent: creating, deleting or copying channels, revealing channel keys, deleting or disabling users, changing roles, resetting 2FA or passkeys, exporting token values. These either require additional verification, cannot be undone, or involve secrets.

## 5. Safety of writes

1. **Two-step confirmation**: the first call returns a preview (current value → new value) and a `confirm_token`; the change runs only when the same arguments are sent again with the token. The token is an HMAC-signed `{tool, argument digest, current value digest, caller token digest, expiry}`, so the server keeps no state.
2. **Optimistic locking**: the current value is re-read before execution; if its digest no longer matches (someone changed it after the preview), the change is refused and a new preview is required.
3. **Merge, never overwrite**: JSON-object options are changed entry by entry with `set` / `unset`, read, merged and written back by the MCP server; pricing is updated per model with its version.
4. **Side effects in tool descriptions**: for example "disabling a channel closes its WebSocket sessions" or "channel affinity has no automatic failover", so both the model and the approving human see them.
5. **Input validation**: arguments are validated against a JSON Schema first, then by business rules (positive ids, time window limits, enum values).
6. **No secrets in logs**: logs carry only a prefix of the PAT digest (which matches `token_ref` in new-api's audit log), never sensitive request fields.

## 6. Output and errors

- **Trimming**: lists return 20 items per page by default and at most 100, with `total` and `has_more`; every tool outputs whitelisted fields only; a result over roughly 60 KB is refused with a hint to narrow the query.
- **Untrusted text**: free text from outside, such as log content and upstream errors, is placed in fields whose names start with `untrusted_`, and the server instructions tell the model to treat it as data and never follow instructions inside it.
- **Error mapping**: new-api responses become actionable hints, such as which scope is missing, an expired token, manual verification required, a version mismatch or rate limiting. All tool errors are returned as tool results (`isError: true`) rather than protocol errors, so the model can read them and adjust.

## 7. Contract with new-api versions

The biggest risk of a separate repository is that a new-api upgrade moves a route or changes a scope without the MCP server noticing. Countermeasures:

1. **Contract check** (`tools/contractcheck`): parses the route scope tables in the new-api source and compares the method, path and scope of every route each tool declares. Run it after every new-api upgrade.
2. **Runtime hint**: `server_status` reports both the new-api version and the version this server was checked against.
3. **Fallback**: `ACCESS_TOKEN_ROUTE_UNDECLARED` and 404 are reported explicitly as a version mismatch.
