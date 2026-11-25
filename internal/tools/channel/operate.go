package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/shape"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
)

const maxBatchStatus = 20

type testIn struct {
	ID           int    `json:"id" jsonschema:"channel id"`
	Model        string `json:"model,omitempty" jsonschema:"model to test (default: the channel's test model)"`
	EndpointType string `json:"endpoint_type,omitempty" jsonschema:"endpoint type to test, for example openai or anthropic"`
	Stream       bool   `json:"stream,omitempty" jsonschema:"test with a streaming request"`
}

type testOut struct {
	OK        bool    `json:"ok"`
	Seconds   float64 `json:"seconds"`
	Message   string  `json:"message,omitempty"`
	ErrorCode string  `json:"error_code,omitempty"`
}

func test(ctx context.Context, d tools.Deps, in testIn) (testOut, error) {
	if err := tools.RequirePositive("id", in.ID); err != nil {
		return testOut{}, err
	}
	q := url.Values{}
	if in.Model != "" {
		q.Set("model", in.Model)
	}
	if in.EndpointType != "" {
		q.Set("endpoint_type", in.EndpointType)
	}
	if in.Stream {
		q.Set("stream", "true")
	}
	env, err := d.API.Do(ctx, newapi.Request{Route: newapi.RouteChannelTest, Params: tools.ID(in.ID), Query: q}, nil)
	// A failed test is a result, not a tool error: new-api reports it as
	// success=false beside the elapsed time.
	if err != nil && !(newapi.IsKind(err, newapi.KindBusiness) && env != nil) {
		return testOut{}, err
	}
	var body struct {
		Success   bool    `json:"success"`
		Message   string  `json:"message"`
		Time      float64 `json:"time"`
		ErrorCode string  `json:"error_code"`
	}
	if err := json.Unmarshal(env.Raw, &body); err != nil {
		return testOut{}, err
	}
	return testOut{OK: body.Success, Seconds: body.Time, Message: shape.Truncate(body.Message, shape.TextLimit), ErrorCode: body.ErrorCode}, nil
}

type setStatusIn struct {
	IDs     []int `json:"ids" jsonschema:"channel ids, at most 20"`
	Enabled bool  `json:"enabled" jsonschema:"true to enable, false to disable manually"`
}

type statusRow struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type setStatusOut struct {
	Channels []statusRow `json:"channels"`
	Note     string      `json:"note,omitempty"`
}

func setStatus(ctx context.Context, d tools.Deps, in setStatusIn) (setStatusOut, error) {
	if len(in.IDs) == 0 || len(in.IDs) > maxBatchStatus {
		return setStatusOut{}, tools.Invalid("ids must hold 1 to %d channel ids", maxBatchStatus)
	}
	seen := map[int]bool{}
	for _, id := range in.IDs {
		if err := tools.RequirePositive("ids", id); err != nil {
			return setStatusOut{}, err
		}
		if seen[id] {
			return setStatusOut{}, tools.Invalid("duplicate channel id %d", id)
		}
		seen[id] = true
	}
	before := make(map[int]rawChannel, len(in.IDs))
	for _, id := range in.IDs {
		c, err := tools.Get[rawChannel](ctx, d.API, newapi.Request{Route: newapi.RouteChannelGet, Params: tools.ID(id)})
		if err != nil {
			return setStatusOut{}, err
		}
		before[id] = c
	}
	status := 2
	if in.Enabled {
		status = 1
	}
	var err error
	if len(in.IDs) == 1 {
		_, err = d.API.Do(ctx, newapi.Request{Route: newapi.RouteChannelStatus, Params: tools.ID(in.IDs[0]), Body: map[string]int{"status": status}}, nil)
	} else {
		_, err = d.API.Do(ctx, newapi.Request{Route: newapi.RouteChannelStatusBatch, Body: map[string]any{"ids": in.IDs, "status": status}}, nil)
	}
	if err != nil {
		return setStatusOut{}, err
	}
	out := setStatusOut{}
	for _, id := range in.IDs {
		after, err := tools.Get[rawChannel](ctx, d.API, newapi.Request{Route: newapi.RouteChannelGet, Params: tools.ID(id)})
		if err != nil {
			return setStatusOut{}, err
		}
		out.Channels = append(out.Channels, statusRow{ID: id, Name: after.Name, Before: statusName(before[id].Status), After: statusName(after.Status)})
		if in.Enabled && after.ChannelInfo.IsMultiKey && after.summary().MultiKey.Disabled == after.ChannelInfo.MultiKeySize {
			out.Note = "Some multi-key channels have every key disabled; enable keys with channel_multikey too, or requests will still fail."
		}
	}
	if !in.Enabled && out.Note == "" {
		out.Note = "Disabling closes open WebSocket sessions on these channels. Channel affinity does not fail over, so sessions pinned to them keep failing until their affinity entry is cleared."
	}
	return out, nil
}

type multiKeyIn struct {
	ID       int    `json:"id" jsonschema:"multi-key channel id"`
	Action   string `json:"action" jsonschema:"status, enable_key, disable_key, enable_all_keys or disable_all_keys"`
	KeyIndex *int   `json:"key_index,omitempty" jsonschema:"key index for enable_key and disable_key"`
	Filter   *int   `json:"filter_status,omitempty" jsonschema:"for status: 1 enabled, 2 manually disabled, 3 automatically disabled"`
	shape.Page
}

type keyRow struct {
	Index      int    `json:"index"`
	Status     string `json:"status"`
	DisabledAt string `json:"disabled_at,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type multiKeyOut struct {
	Message        string          `json:"message,omitempty"`
	Keys           []keyRow        `json:"keys,omitempty"`
	Page           *shape.PageInfo `json:"page,omitempty"`
	Enabled        int             `json:"enabled"`
	ManualDisabled int             `json:"manually_disabled"`
	AutoDisabled   int             `json:"auto_disabled"`
	ChannelStatus  string          `json:"channel_status"`
}

var multiKeyActions = map[string]string{
	"status": "get_key_status", "enable_key": "enable_key", "disable_key": "disable_key",
	"enable_all_keys": "enable_all_keys", "disable_all_keys": "disable_all_keys",
}

func multiKey(ctx context.Context, d tools.Deps, in multiKeyIn) (multiKeyOut, error) {
	if err := tools.RequirePositive("id", in.ID); err != nil {
		return multiKeyOut{}, err
	}
	action, ok := multiKeyActions[in.Action]
	if !ok {
		return multiKeyOut{}, tools.Invalid("action must be status, enable_key, disable_key, enable_all_keys or disable_all_keys")
	}
	needsIndex := action == "enable_key" || action == "disable_key"
	if needsIndex && (in.KeyIndex == nil || *in.KeyIndex < 0) {
		return multiKeyOut{}, tools.Invalid("key_index is required for %s", in.Action)
	}
	body := map[string]any{"channel_id": in.ID, "action": action}
	if needsIndex {
		body["key_index"] = *in.KeyIndex
	}
	page := in.Page.Normalized()
	body["page"], body["page_size"] = page.Page, page.PageSize
	if in.Filter != nil {
		body["status"] = *in.Filter
	}
	var status struct {
		Keys []struct {
			Index        int    `json:"index"`
			Status       int    `json:"status"`
			DisabledTime int64  `json:"disabled_time"`
			Reason       string `json:"reason"`
		} `json:"keys"`
		Total               int `json:"total"`
		EnabledCount        int `json:"enabled_count"`
		ManualDisabledCount int `json:"manual_disabled_count"`
		AutoDisabledCount   int `json:"auto_disabled_count"`
	}
	var target any
	if action == "get_key_status" {
		target = &status
	}
	env, err := d.API.Do(ctx, newapi.Request{Route: newapi.RouteChannelMultiKey, Body: body}, target)
	if err != nil {
		return multiKeyOut{}, err
	}
	out := multiKeyOut{Message: env.Message}
	if action == "get_key_status" {
		info := shape.NewPageInfo(in.Page, status.Total)
		out.Page = &info
		for _, k := range status.Keys {
			out.Keys = append(out.Keys, keyRow{Index: k.Index, Status: statusName(k.Status), DisabledAt: shape.Time(k.DisabledTime), Reason: shape.Truncate(k.Reason, shape.TextLimit)})
		}
		out.Enabled, out.ManualDisabled, out.AutoDisabled = status.EnabledCount, status.ManualDisabledCount, status.AutoDisabledCount
	}
	// new-api re-enables a channel when a key comes back only if the channel
	// was disabled because all keys were; report the resulting state so the
	// caller sees whether channel_set_status is still needed.
	c, err := tools.Get[rawChannel](ctx, d.API, newapi.Request{Route: newapi.RouteChannelGet, Params: tools.ID(in.ID)})
	if err != nil {
		return multiKeyOut{}, err
	}
	if !c.ChannelInfo.IsMultiKey {
		return multiKeyOut{}, errors.New("channel " + strconv.Itoa(in.ID) + " is not a multi-key channel")
	}
	out.ChannelStatus = statusName(c.Status)
	return out, nil
}

type fetchModelsOut struct {
	Upstream   []string `json:"upstream_models"`
	Missing    []string `json:"not_configured" jsonschema:"served upstream but not listed on the channel"`
	Obsolete   []string `json:"not_upstream" jsonschema:"listed on the channel but not served upstream"`
	Configured int      `json:"configured_count"`
}

func fetchModels(ctx context.Context, d tools.Deps, in idIn) (fetchModelsOut, error) {
	if err := tools.RequirePositive("id", in.ID); err != nil {
		return fetchModelsOut{}, err
	}
	upstream, err := tools.Get[[]string](ctx, d.API, newapi.Request{Route: newapi.RouteChannelFetchModels, Params: tools.ID(in.ID)})
	if err != nil {
		return fetchModelsOut{}, err
	}
	c, err := tools.Get[rawChannel](ctx, d.API, newapi.Request{Route: newapi.RouteChannelGet, Params: tools.ID(in.ID)})
	if err != nil {
		return fetchModelsOut{}, err
	}
	configured := splitModels(c.Models)
	have := map[string]bool{}
	for _, m := range configured {
		have[m] = true
	}
	served := map[string]bool{}
	out := fetchModelsOut{Upstream: upstream, Configured: len(configured), Missing: []string{}, Obsolete: []string{}}
	for _, m := range upstream {
		served[m] = true
		if !have[m] {
			out.Missing = append(out.Missing, m)
		}
	}
	for _, m := range configured {
		if !served[m] {
			out.Obsolete = append(out.Obsolete, m)
		}
	}
	return out, nil
}

func operateDefinitions() []tools.Definition {
	return []tools.Definition{
		tools.Define(tools.Spec{
			Name: "channel_test", Toolset: "channel", Level: config.LevelOperate, Title: "Test a channel",
			Description: "Send a real test request through a channel and report success, latency and the upstream error. " +
				"It costs a small upstream request and updates the channel's recorded response time. May take up to two minutes.",
			Routes:  []newapi.Route{newapi.RouteChannelTest},
			Timeout: 150 * time.Second,
		}, test),
		tools.Define(tools.Spec{
			Name: "channel_set_status", Toolset: "channel", Level: config.LevelOperate, Title: "Enable or disable channels",
			Description: "Enable or manually disable up to 20 channels and report each channel's status before and after. " +
				"Disabling cuts traffic immediately and closes open WebSocket sessions. Channel affinity has no failover: before disabling a " +
				"pinned channel make sure a healthy channel with the same or higher priority serves the same models.",
			Routes:     []newapi.Route{newapi.RouteChannelGet, newapi.RouteChannelStatus, newapi.RouteChannelStatusBatch},
			Idempotent: true,
		}, setStatus),
		tools.Define(tools.Spec{
			Name: "channel_multikey", Toolset: "channel", Level: config.LevelOperate, Title: "Manage keys of a multi-key channel",
			Description: "List key health of a multi-key channel (action=status), or enable/disable one key or all keys by index. " +
				"Key values are never shown. The result includes the channel status afterwards: new-api re-enables the channel only when " +
				"it was disabled because all keys failed; otherwise use channel_set_status as well.",
			Routes: []newapi.Route{newapi.RouteChannelMultiKey, newapi.RouteChannelGet},
		}, multiKey),
		tools.Define(tools.Spec{
			Name: "channel_fetch_upstream_models", Toolset: "channel", Level: config.LevelOperate, Title: "Compare upstream models",
			Description: "Ask the channel's upstream which models it serves and compare with the channel's configured model list. Changes nothing.",
			Routes:      []newapi.Route{newapi.RouteChannelFetchModels, newapi.RouteChannelGet},
			Idempotent:  true,
		}, fetchModels),
	}
}
