package channel

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

type listIn struct {
	shape.Page
	Keyword string `json:"keyword,omitempty" jsonschema:"match channel id, name, key prefix or base URL"`
	Model   string `json:"model,omitempty" jsonschema:"only channels that serve a model containing this text"`
	Group   string `json:"group,omitempty" jsonschema:"only channels in this user group"`
	Status  string `json:"status,omitempty" jsonschema:"enabled, disabled (manual or automatic) or all (default)"`
	Type    *int   `json:"type,omitempty" jsonschema:"only channels of this numeric channel type"`
}

type listOut struct {
	Items []Summary      `json:"items"`
	Page  shape.PageInfo `json:"page"`
}

type rawList struct {
	Items []rawChannel `json:"items"`
	Total int          `json:"total"`
}

func list(ctx context.Context, d tools.Deps, in listIn) (listOut, error) {
	q := url.Values{}
	in.Page.Query(q)
	switch in.Status {
	case "", "all":
	case "enabled", "disabled":
		q.Set("status", in.Status)
	default:
		return listOut{}, tools.Invalid("status must be enabled, disabled or all")
	}
	if in.Group != "" {
		q.Set("group", in.Group)
	}
	route := newapi.RouteChannelList
	if in.Keyword != "" || in.Model != "" {
		route = newapi.RouteChannelSearch
		q.Set("keyword", in.Keyword)
		q.Set("model", in.Model)
	}
	if in.Type != nil {
		if route == newapi.RouteChannelSearch {
			return listOut{}, tools.Invalid("type cannot be combined with keyword or model")
		}
		q.Set("type", strconv.Itoa(*in.Type))
	}
	raw, err := tools.Get[rawList](ctx, d.API, newapi.Request{Route: route, Query: q})
	if err != nil {
		return listOut{}, err
	}
	out := listOut{Items: make([]Summary, 0, len(raw.Items)), Page: shape.NewPageInfo(in.Page, raw.Total)}
	for _, c := range raw.Items {
		out.Items = append(out.Items, c.summary())
	}
	return out, nil
}

type idIn struct {
	ID int `json:"id" jsonschema:"channel id"`
}

func get(ctx context.Context, d tools.Deps, in idIn) (Detail, error) {
	if err := tools.RequirePositive("id", in.ID); err != nil {
		return Detail{}, err
	}
	raw, err := tools.Get[rawChannel](ctx, d.API, newapi.Request{Route: newapi.RouteChannelGet, Params: tools.ID(in.ID)})
	if err != nil {
		return Detail{}, err
	}
	return raw.detail(), nil
}

type routingOut struct {
	RetryTimes       int             `json:"retry_times"`
	AutomaticDisable bool            `json:"automatic_disable"`
	PriorityOrder    json.RawMessage `json:"priority_order" jsonschema:"channels in the order new-api tries them, highest priority first"`
}

func routing(ctx context.Context, d tools.Deps, _ struct{}) (routingOut, error) {
	var ops struct {
		RetryTimes    int `json:"retry_times"`
		RequestPolicy struct {
			AutomaticDisable bool `json:"automatic_disable"`
		} `json:"request_policy"`
	}
	if _, err := d.API.Do(ctx, newapi.Request{Route: newapi.RouteChannelOps}, &ops); err != nil {
		return routingOut{}, err
	}
	order, err := tools.Get[json.RawMessage](ctx, d.API, newapi.Request{Route: newapi.RouteChannelPriorityOrder})
	if err != nil {
		return routingOut{}, err
	}
	return routingOut{RetryTimes: ops.RetryTimes, AutomaticDisable: ops.RequestPolicy.AutomaticDisable, PriorityOrder: order}, nil
}

type codexUsageOut struct {
	Usage json.RawMessage `json:"usage" jsonschema:"rate-limit windows reported by the Codex account"`
}

func codexUsage(ctx context.Context, d tools.Deps, in idIn) (codexUsageOut, error) {
	if err := tools.RequirePositive("id", in.ID); err != nil {
		return codexUsageOut{}, err
	}
	raw, err := tools.Get[json.RawMessage](ctx, d.API, newapi.Request{Route: newapi.RouteChannelCodexUsage, Params: tools.ID(in.ID)})
	return codexUsageOut{Usage: raw}, err
}

func readDefinitions() []tools.Definition {
	return []tools.Definition{
		tools.Define(tools.Spec{
			Name: "channel_list", Toolset: "channel", Level: config.LevelRead, Title: "List channels",
			Description: "List upstream channels with status, disable reason, priority, weight and multi-key health. " +
				"Use status=disabled to find channels that were switched off. Keys are never returned.",
			Routes: []newapi.Route{newapi.RouteChannelList, newapi.RouteChannelSearch},
		}, list),
		tools.Define(tools.Spec{
			Name: "channel_get", Toolset: "channel", Level: config.LevelRead, Title: "Get a channel",
			Description: "Get one channel's full configuration: models, model mapping, status code mapping, overrides and settings. " +
				"Header override values and keys are withheld.",
			Routes: []newapi.Route{newapi.RouteChannelGet},
		}, get),
		tools.Define(tools.Spec{
			Name: "channel_routing", Toolset: "channel", Level: config.LevelRead, Title: "Channel routing policy",
			Description: "Show the retry count, whether failing channels are disabled automatically, and the priority order new-api uses to pick channels.",
			Routes:      []newapi.Route{newapi.RouteChannelOps, newapi.RouteChannelPriorityOrder},
		}, routing),
		tools.Define(tools.Spec{
			Name: "channel_codex_usage", Toolset: "channel", Level: config.LevelRead, Title: "Codex channel usage",
			Description: "Show the rate-limit usage windows of a ChatGPT Subscription (Codex) channel's account.",
			Routes:      []newapi.Route{newapi.RouteChannelCodexUsage},
		}, codexUsage),
	}
}
