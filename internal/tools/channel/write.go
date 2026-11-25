package channel

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
)

// updateIn lists the only channel fields this tool may change. Keys, status,
// type, overrides and settings are deliberately absent.
type updateIn struct {
	ID                int      `json:"id" jsonschema:"channel id"`
	Name              *string  `json:"name,omitempty"`
	BaseURL           *string  `json:"base_url,omitempty" jsonschema:"upstream base URL; new-api requires the channel sensitive_write permission for this field"`
	Models            []string `json:"models,omitempty" jsonschema:"replace the whole model list"`
	AddModels         []string `json:"add_models,omitempty" jsonschema:"models to add to the current list"`
	RemoveModels      []string `json:"remove_models,omitempty" jsonschema:"models to remove from the current list"`
	Group             *string  `json:"group,omitempty" jsonschema:"comma-separated user groups"`
	Priority          *int64   `json:"priority,omitempty" jsonschema:"higher is preferred"`
	Weight            *uint    `json:"weight,omitempty" jsonschema:"share among channels of the same priority"`
	Tag               *string  `json:"tag,omitempty"`
	Remark            *string  `json:"remark,omitempty"`
	TestModel         *string  `json:"test_model,omitempty"`
	AutoBan           *bool    `json:"auto_ban,omitempty" jsonschema:"whether new-api may disable this channel automatically on failures"`
	ModelMapping      *string  `json:"model_mapping,omitempty" jsonschema:"JSON object mapping requested model to upstream model, or empty string to clear"`
	StatusCodeMapping *string  `json:"status_code_mapping,omitempty" jsonschema:"JSON object mapping upstream status codes, or empty string to clear"`
	ConfirmToken      string   `json:"confirm_token,omitempty" jsonschema:"token from the preview; omit to preview"`
}

// state is the editable part of a channel, compared before and after.
type state struct {
	Name              string   `json:"name"`
	BaseURL           string   `json:"base_url"`
	Models            []string `json:"models"`
	Group             string   `json:"group"`
	Priority          int64    `json:"priority"`
	Weight            uint     `json:"weight"`
	Tag               string   `json:"tag"`
	Remark            string   `json:"remark"`
	TestModel         string   `json:"test_model"`
	AutoBan           bool     `json:"auto_ban"`
	ModelMapping      string   `json:"model_mapping"`
	StatusCodeMapping string   `json:"status_code_mapping"`
}

func stateOf(c rawChannel) state {
	return state{
		Name: c.Name, BaseURL: deref(c.BaseURL), Models: splitModels(c.Models), Group: c.Group,
		Priority: deref(c.Priority), Weight: deref(c.Weight), Tag: deref(c.Tag), Remark: deref(c.Remark),
		TestModel: deref(c.TestModel), AutoBan: c.AutoBan == nil || *c.AutoBan != 0,
		ModelMapping: deref(c.ModelMapping), StatusCodeMapping: deref(c.StatusCodeMapping),
	}
}

type updateMutation struct {
	api newapi.Client
	in  updateIn
}

func (m updateMutation) Load(ctx context.Context) (state, error) {
	c, err := tools.Get[rawChannel](ctx, m.api, newapi.Request{Route: newapi.RouteChannelGet, Params: tools.ID(m.in.ID)})
	if err != nil {
		return state{}, err
	}
	return stateOf(c), nil
}

// Plan applies the requested edits to the current state. It is pure.
func (m updateMutation) Plan(cur state) (state, []guard.Change, error) {
	in := m.in
	if in.Models != nil && (in.AddModels != nil || in.RemoveModels != nil) {
		return state{}, nil, tools.Invalid("use either models or add_models/remove_models, not both")
	}
	for _, field := range []struct {
		name  string
		value *string
	}{{"model_mapping", in.ModelMapping}, {"status_code_mapping", in.StatusCodeMapping}} {
		if field.value == nil || strings.TrimSpace(*field.value) == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(*field.value), &obj); err != nil || obj == nil {
			return state{}, nil, tools.Invalid("%s must be a JSON object", field.name)
		}
	}

	next := cur
	next.Models = slices.Clone(cur.Models)
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&next.Name, in.Name)
	set(&next.BaseURL, in.BaseURL)
	set(&next.Group, in.Group)
	set(&next.Tag, in.Tag)
	set(&next.Remark, in.Remark)
	set(&next.TestModel, in.TestModel)
	set(&next.ModelMapping, in.ModelMapping)
	set(&next.StatusCodeMapping, in.StatusCodeMapping)
	if in.Priority != nil {
		next.Priority = *in.Priority
	}
	if in.Weight != nil {
		next.Weight = *in.Weight
	}
	if in.AutoBan != nil {
		next.AutoBan = *in.AutoBan
	}
	if in.Models != nil {
		next.Models = dedupe(in.Models)
	}
	for _, model := range in.AddModels {
		if model = strings.TrimSpace(model); model != "" && !slices.Contains(next.Models, model) {
			next.Models = append(next.Models, model)
		}
	}
	for _, model := range in.RemoveModels {
		next.Models = slices.DeleteFunc(next.Models, func(m string) bool { return m == strings.TrimSpace(model) })
	}
	if next.Name == "" {
		return state{}, nil, tools.Invalid("name cannot be empty")
	}
	if len(next.Models) == 0 {
		return state{}, nil, tools.Invalid("a channel must keep at least one model")
	}

	var changes []guard.Change
	add := func(field string, before, after any) {
		changes = append(changes, guard.Change{Field: field, Before: before, After: after})
	}
	if next.Name != cur.Name {
		add("name", cur.Name, next.Name)
	}
	if next.BaseURL != cur.BaseURL {
		add("base_url", redactURL(cur.BaseURL), redactURL(next.BaseURL))
	}
	if !slices.Equal(next.Models, cur.Models) {
		added, removed := diffModels(cur.Models, next.Models)
		add("models", map[string]any{"removed": removed, "count": len(cur.Models)}, map[string]any{"added": added, "count": len(next.Models)})
	}
	if next.Group != cur.Group {
		add("group", cur.Group, next.Group)
	}
	if next.Priority != cur.Priority {
		add("priority", cur.Priority, next.Priority)
	}
	if next.Weight != cur.Weight {
		add("weight", cur.Weight, next.Weight)
	}
	if next.Tag != cur.Tag {
		add("tag", cur.Tag, next.Tag)
	}
	if next.Remark != cur.Remark {
		add("remark", cur.Remark, next.Remark)
	}
	if next.TestModel != cur.TestModel {
		add("test_model", cur.TestModel, next.TestModel)
	}
	if next.AutoBan != cur.AutoBan {
		add("auto_ban", cur.AutoBan, next.AutoBan)
	}
	if next.ModelMapping != cur.ModelMapping {
		add("model_mapping", cur.ModelMapping, next.ModelMapping)
	}
	if next.StatusCodeMapping != cur.StatusCodeMapping {
		add("status_code_mapping", cur.StatusCodeMapping, next.StatusCodeMapping)
	}
	return next, changes, nil
}

// Apply sends only the changed fields; new-api's update leaves the others alone.
func (m updateMutation) Apply(ctx context.Context, cur, next state) error {
	body := map[string]any{"id": m.in.ID}
	if next.Name != cur.Name {
		body["name"] = next.Name
	}
	if next.BaseURL != cur.BaseURL {
		body["base_url"] = next.BaseURL
	}
	if !slices.Equal(next.Models, cur.Models) {
		body["models"] = strings.Join(next.Models, ",")
	}
	if next.Group != cur.Group {
		body["group"] = next.Group
	}
	if next.Priority != cur.Priority {
		body["priority"] = next.Priority
	}
	if next.Weight != cur.Weight {
		body["weight"] = next.Weight
	}
	if next.Tag != cur.Tag {
		body["tag"] = next.Tag
	}
	if next.Remark != cur.Remark {
		body["remark"] = next.Remark
	}
	if next.TestModel != cur.TestModel {
		body["test_model"] = next.TestModel
	}
	if next.AutoBan != cur.AutoBan {
		autoBan := 0
		if next.AutoBan {
			autoBan = 1
		}
		body["auto_ban"] = autoBan
	}
	if next.ModelMapping != cur.ModelMapping {
		body["model_mapping"] = next.ModelMapping
	}
	if next.StatusCodeMapping != cur.StatusCodeMapping {
		body["status_code_mapping"] = next.StatusCodeMapping
	}
	_, err := m.api.Do(ctx, newapi.Request{Route: newapi.RouteChannelUpdate, Body: body}, nil)
	return err
}

func update(ctx context.Context, d tools.Deps, in updateIn) (guard.Outcome, error) {
	if err := tools.RequirePositive("id", in.ID); err != nil {
		return guard.Outcome{}, err
	}
	token := in.ConfirmToken
	in.ConfirmToken = ""
	return guard.Run(ctx, d.Guard, "channel_update", in, token, updateMutation{api: d.API, in: in})
}

func dedupe(models []string) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

func diffModels(before, after []string) (added, removed []string) {
	added, removed = []string{}, []string{}
	for _, m := range after {
		if !slices.Contains(before, m) {
			added = append(added, m)
		}
	}
	for _, m := range before {
		if !slices.Contains(after, m) {
			removed = append(removed, m)
		}
	}
	return added, removed
}

func writeDefinitions() []tools.Definition {
	return []tools.Definition{
		tools.Define(tools.Spec{
			Name: "channel_update", Toolset: "channel", Level: config.LevelWrite, Title: "Change a channel's routing settings",
			Description: "Change a channel's name, base URL, models, groups, priority, weight, tag, remark, test model, auto-ban or mappings. " +
				"Two steps: without confirm_token it returns the diff and a confirm_token and changes nothing; call again with the same " +
				"arguments plus confirm_token to apply. Keys, status, type, overrides and proxy settings cannot be changed here.",
			Routes: []newapi.Route{newapi.RouteChannelGet, newapi.RouteChannelUpdate},
		}, update),
	}
}

// Definitions returns every channel tool.
func Definitions() []tools.Definition {
	return slices.Concat(readDefinitions(), operateDefinitions(), writeDefinitions())
}
