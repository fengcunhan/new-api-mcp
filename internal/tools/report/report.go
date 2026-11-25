// Package report holds the usage report tools.
package report

import (
	"cmp"
	"context"
	"net/url"
	"slices"
	"strconv"

	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/shape"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
)

type quotaIn struct {
	shape.Range
	GroupBy  string `json:"group_by,omitempty" jsonschema:"model (default), user or hour"`
	Username string `json:"username,omitempty" jsonschema:"only this user; works with group_by model or hour"`
	Top      int    `json:"top,omitempty" jsonschema:"rows to return, highest cost first (default 20, max 200)"`
}

type quotaRow struct {
	Username  string `json:"username"`
	ModelName string `json:"model_name"`
	CreatedAt int64  `json:"created_at"`
	Count     int64  `json:"count"`
	Quota     int64  `json:"quota"`
	TokenUsed int64  `json:"token_used"`
}

// Row is one aggregated report line.
type Row struct {
	Key      string  `json:"key"`
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens"`
	CostUSD  float64 `json:"cost_usd"`
	quota    int64
}

type quotaOut struct {
	From          string  `json:"from"`
	To            string  `json:"to"`
	GroupBy       string  `json:"group_by"`
	Rows          []Row   `json:"rows"`
	TotalRows     int     `json:"total_rows"`
	TotalRequests int64   `json:"total_requests"`
	TotalTokens   int64   `json:"total_tokens"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
}

func quota(ctx context.Context, d tools.Deps, in quotaIn) (quotaOut, error) {
	start, end, err := in.Range.Resolve(d.Now(), 24, 31)
	if err != nil {
		return quotaOut{}, tools.Invalid("%v", err)
	}
	groupBy := cmp.Or(in.GroupBy, "model")
	top := in.Top
	if top <= 0 {
		top = 20
	}
	top = min(top, 200)

	q := url.Values{"start_timestamp": {strconv.FormatInt(start, 10)}, "end_timestamp": {strconv.FormatInt(end, 10)}}
	var route newapi.Route
	switch groupBy {
	case "model", "hour":
		route = newapi.RouteDataQuota
		if in.Username != "" {
			q.Set("username", in.Username)
		}
	case "user":
		route = newapi.RouteDataQuotaByUser
	default:
		return quotaOut{}, tools.Invalid("group_by must be model, user or hour")
	}
	if in.Username != "" && route != newapi.RouteDataQuota {
		return quotaOut{}, tools.Invalid("username works only with group_by model or hour")
	}
	rows, err := tools.Get[[]quotaRow](ctx, d.API, newapi.Request{Route: route, Query: q})
	if err != nil {
		return quotaOut{}, err
	}
	agg := Aggregate(rows, groupBy)
	out := quotaOut{From: shape.Time(start), To: shape.Time(end), GroupBy: groupBy, TotalRows: len(agg)}
	var totalQuota int64
	for _, r := range agg {
		out.TotalRequests += r.Requests
		out.TotalTokens += r.Tokens
		totalQuota += r.quota
	}
	out.TotalCostUSD = shape.USD(totalQuota)
	if groupBy != "hour" && len(agg) > top {
		agg = agg[:top]
	}
	out.Rows = agg
	return out, nil
}

// Aggregate sums rows by the grouping key. Hours sort by time, everything
// else by cost, highest first.
func Aggregate(rows []quotaRow, groupBy string) []Row {
	index := map[string]int{}
	var out []Row
	for _, r := range rows {
		var key string
		switch groupBy {
		case "user":
			key = r.Username
		case "hour":
			key = shape.Time(r.CreatedAt - r.CreatedAt%3600)
		default:
			key = r.ModelName
		}
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, Row{Key: key})
		}
		out[i].Requests += r.Count
		out[i].Tokens += r.TokenUsed
		out[i].quota += r.Quota
	}
	for i := range out {
		out[i].CostUSD = shape.USD(out[i].quota)
	}
	if groupBy == "hour" {
		slices.SortFunc(out, func(a, b Row) int { return cmp.Compare(a.Key, b.Key) })
	} else {
		slices.SortFunc(out, func(a, b Row) int { return cmp.Or(cmp.Compare(b.quota, a.quota), cmp.Compare(a.Key, b.Key)) })
	}
	return out
}

// Definitions returns every report tool.
func Definitions() []tools.Definition {
	return []tools.Definition{
		tools.Define(tools.Spec{
			Name: "usage_quota_report", Toolset: "report", Level: config.LevelRead, Title: "Usage and cost report",
			Description: "Requests, tokens and cost over a window (default 24 hours, at most 31 days), grouped by model, user or hour. " +
				"Use it for 'who spent the most yesterday' or 'which model costs the most'.",
			Routes: []newapi.Route{newapi.RouteDataQuota, newapi.RouteDataQuotaByUser},
		}, quota),
	}
}
