// Package ops holds status, model catalogue and runtime tools.
package ops

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/shape"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
)

// Version is this server's version, set at build time.
var Version = "dev"

// TestedNewAPIVersion is the new-api release this server was checked against.
const TestedNewAPIVersion = "v1.0.0-rc.51"

type statusOut struct {
	MCPVersion      string  `json:"mcp_version"`
	NewAPIVersion   string  `json:"newapi_version"`
	TestedNewAPI    string  `json:"tested_newapi_version"`
	NewAPIStartedAt string  `json:"newapi_started_at"`
	QuotaPerUnit    float64 `json:"quota_per_unit"`
	CallerID        int     `json:"caller_id"`
	CallerUsername  string  `json:"caller_username"`
	CallerRole      int     `json:"caller_role" jsonschema:"1 user, 5 department admin, 10 admin, 100 root"`
	Note            string  `json:"note,omitempty"`
}

func status(ctx context.Context, d tools.Deps, _ struct{}) (statusOut, error) {
	st, err := tools.Get[struct {
		Version      string  `json:"version"`
		StartTime    int64   `json:"start_time"`
		QuotaPerUnit float64 `json:"quota_per_unit"`
	}](ctx, d.API, newapi.Request{Route: newapi.RouteStatus})
	if err != nil {
		return statusOut{}, err
	}
	me, err := tools.Get[struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
		Role     int    `json:"role"`
	}](ctx, d.API, newapi.Request{Route: newapi.RouteUserSelf})
	if err != nil {
		return statusOut{}, err
	}
	out := statusOut{
		MCPVersion: Version, NewAPIVersion: st.Version, TestedNewAPI: TestedNewAPIVersion,
		NewAPIStartedAt: shape.Time(st.StartTime), QuotaPerUnit: st.QuotaPerUnit,
		CallerID: me.ID, CallerUsername: me.Username, CallerRole: me.Role,
	}
	if st.QuotaPerUnit != 0 && st.QuotaPerUnit != shape.QuotaPerUSD {
		out.Note = "new-api uses a non-default quota per unit; USD figures from this server assume 500000 quota per USD."
	}
	return out, nil
}

type modelListIn struct {
	shape.Page
	Keyword string `json:"keyword,omitempty"`
}

type modelRow struct {
	ID           int      `json:"id"`
	Name         string   `json:"model_name"`
	Enabled      bool     `json:"enabled"`
	Tags         string   `json:"tags,omitempty"`
	Channels     int      `json:"bound_channels"`
	EnableGroups []string `json:"enable_groups,omitempty"`
}

type modelListOut struct {
	Items []modelRow     `json:"items"`
	Page  shape.PageInfo `json:"page"`
}

func modelList(ctx context.Context, d tools.Deps, in modelListIn) (modelListOut, error) {
	q := url.Values{}
	in.Page.Query(q)
	route := newapi.RouteModelList
	if in.Keyword != "" {
		route = newapi.RouteModelSearch
		q.Set("keyword", in.Keyword)
	}
	raw, err := tools.Get[struct {
		Items []struct {
			ID            int               `json:"id"`
			ModelName     string            `json:"model_name"`
			Status        int               `json:"status"`
			Tags          string            `json:"tags"`
			BoundChannels []json.RawMessage `json:"bound_channels"`
			EnableGroups  []string          `json:"enable_groups"`
		} `json:"items"`
		Total int `json:"total"`
	}](ctx, d.API, newapi.Request{Route: route, Query: q})
	if err != nil {
		return modelListOut{}, err
	}
	out := modelListOut{Items: []modelRow{}, Page: shape.NewPageInfo(in.Page, raw.Total)}
	for _, m := range raw.Items {
		out.Items = append(out.Items, modelRow{ID: m.ID, Name: m.ModelName, Enabled: m.Status == 1, Tags: m.Tags, Channels: len(m.BoundChannels), EnableGroups: m.EnableGroups})
	}
	return out, nil
}

type missingOut struct {
	Models []string `json:"models" jsonschema:"models served by channels that have no model metadata entry"`
}

func modelMissing(ctx context.Context, d tools.Deps, _ struct{}) (missingOut, error) {
	models, err := tools.Get[[]string](ctx, d.API, newapi.Request{Route: newapi.RouteModelMissing})
	if models == nil {
		models = []string{}
	}
	return missingOut{Models: models}, err
}

type groupOut struct {
	Groups []string `json:"groups"`
}

func groupList(ctx context.Context, d tools.Deps, _ struct{}) (groupOut, error) {
	groups, err := tools.Get[[]string](ctx, d.API, newapi.Request{Route: newapi.RouteGroupList})
	return groupOut{Groups: groups}, err
}

type perfIn struct {
	Hours int `json:"hours,omitempty" jsonschema:"window for latency and success-rate metrics per model (default set by new-api)"`
}

type perfOut struct {
	Models  json.RawMessage `json:"models" jsonschema:"latency, first-token time and success rate per model"`
	Process json.RawMessage `json:"process,omitempty" jsonschema:"process memory, goroutines and caches; root only"`
}

func perf(ctx context.Context, d tools.Deps, in perfIn) (perfOut, error) {
	q := url.Values{}
	if in.Hours > 0 {
		q.Set("hours", strconv.Itoa(in.Hours))
	}
	models, err := tools.Get[json.RawMessage](ctx, d.API, newapi.Request{Route: newapi.RouteDataPerfSummary, Query: q})
	if err != nil {
		return perfOut{}, err
	}
	out := perfOut{Models: models}
	// Process statistics need ops:read and root; missing them is not an error.
	if process, err := tools.Get[json.RawMessage](ctx, d.API, newapi.Request{Route: newapi.RoutePerfStats}); err == nil {
		out.Process = process
	}
	return out, nil
}

type systemOut struct {
	Nodes json.RawMessage `json:"nodes" jsonschema:"gateway instances with version, role and last heartbeat"`
	Tasks json.RawMessage `json:"system_tasks" jsonschema:"recent background system tasks"`
}

func system(ctx context.Context, d tools.Deps, _ struct{}) (systemOut, error) {
	nodes, err := tools.Get[json.RawMessage](ctx, d.API, newapi.Request{Route: newapi.RouteSystemNodes})
	if err != nil {
		return systemOut{}, err
	}
	tasks, err := tools.Get[json.RawMessage](ctx, d.API, newapi.Request{Route: newapi.RouteSystemTasks, Query: url.Values{"p": {"1"}, "page_size": {"10"}}})
	return systemOut{Nodes: nodes, Tasks: tasks}, err
}

// Definitions returns every ops tool.
func Definitions() []tools.Definition {
	return []tools.Definition{
		tools.Define(tools.Spec{
			Name: "server_status", Toolset: "ops", Level: config.LevelRead, Title: "Connection check",
			Description: "Check the connection: new-api version, the version this server was tested with, and which user the access token belongs to. Run it first.",
			Routes:      []newapi.Route{newapi.RouteStatus, newapi.RouteUserSelf},
		}, status),
		tools.Define(tools.Spec{
			Name: "model_list", Toolset: "ops", Level: config.LevelRead, Title: "Model catalogue",
			Description: "List model metadata entries with how many channels serve each model and which groups may use it.",
			Routes:      []newapi.Route{newapi.RouteModelList, newapi.RouteModelSearch},
		}, modelList),
		tools.Define(tools.Spec{
			Name: "model_missing", Toolset: "ops", Level: config.LevelRead, Title: "Models without metadata",
			Description: "Models that channels serve but that have no model metadata entry.",
			Routes:      []newapi.Route{newapi.RouteModelMissing},
		}, modelMissing),
		tools.Define(tools.Spec{
			Name: "group_list", Toolset: "ops", Level: config.LevelRead, Title: "User groups",
			Description: "List the user group names.",
			Routes:      []newapi.Route{newapi.RouteGroupList},
		}, groupList),
		tools.Define(tools.Spec{
			Name: "perf_stats", Toolset: "ops", Level: config.LevelRead, Title: "Performance",
			Description: "Latency, first-token time and success rate per model; for root tokens also process memory and cache statistics.",
			Routes:      []newapi.Route{newapi.RouteDataPerfSummary, newapi.RoutePerfStats},
		}, perf),
		tools.Define(tools.Spec{
			Name: "system_info", Toolset: "ops", Level: config.LevelRead, Title: "Gateway nodes",
			Description: "Gateway instances (version, master or slave, last heartbeat) and recent background system tasks. Root only.",
			Routes:      []newapi.Route{newapi.RouteSystemNodes, newapi.RouteSystemTasks},
		}, system),
	}
}
