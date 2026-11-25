// Package pricing holds the model pricing tools. Prices change through
// new-api's per-model pricing API, which carries a version per model, so a
// change can never overwrite a whole ratio table.
package pricing

import (
	"context"
	"encoding/json"
	"maps"
	"net/url"
	"reflect"
	"slices"

	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
)

// Keys are the pricing fields of one model (new-api modelPricingOptionKeys).
var Keys = []string{
	"ModelRatio", "CompletionRatio", "CacheRatio", "CreateCacheRatio", "ModelPrice",
	"ImageRatio", "AudioRatio", "AudioCompletionRatio",
	"billing_setting.billing_mode", "billing_setting.billing_expr", "billing_setting.plugin_billing_expr",
}

const maxModelsPerGet = 20

type entry struct {
	ModelName      string          `json:"model_name"`
	Version        string          `json:"version"`
	Configured     map[string]any  `json:"configured"`
	Effective      map[string]any  `json:"effective"`
	BillingDetails json.RawMessage `json:"billing_details"`
}

type snapshot struct {
	Entries      []entry `json:"entries"`
	EmptyVersion string  `json:"empty_version"`
}

type getIn struct {
	Models []string `json:"models,omitempty" jsonschema:"model names to show in full (at most 20); omit to list priced model names only"`
}

type priced struct {
	Model          string          `json:"model"`
	Configured     map[string]any  `json:"configured" jsonschema:"values saved for this model"`
	Effective      map[string]any  `json:"effective" jsonschema:"values in force, including built-in defaults"`
	BillingDetails json.RawMessage `json:"billing_details,omitempty" jsonschema:"prices per million tokens or per call as new-api bills them"`
}

type getOut struct {
	Models []priced `json:"models,omitempty"`
	Names  []string `json:"names,omitempty"`
	Count  int      `json:"count"`
}

func get(ctx context.Context, d tools.Deps, in getIn) (getOut, error) {
	if len(in.Models) > maxModelsPerGet {
		return getOut{}, tools.Invalid("at most %d models per call", maxModelsPerGet)
	}
	q := url.Values{}
	for _, m := range in.Models {
		q.Add("model", m)
	}
	snap, err := tools.Get[snapshot](ctx, d.API, newapi.Request{Route: newapi.RoutePricingGet, Query: q})
	if err != nil {
		return getOut{}, err
	}
	out := getOut{Count: len(snap.Entries)}
	for _, e := range snap.Entries {
		if len(in.Models) == 0 {
			out.Names = append(out.Names, e.ModelName)
			continue
		}
		out.Models = append(out.Models, priced{Model: e.ModelName, Configured: e.Configured, Effective: e.Effective, BillingDetails: e.BillingDetails})
	}
	return out, nil
}

type issuesOut struct {
	Issues json.RawMessage `json:"issues" jsonschema:"models served by enabled channels whose pricing is missing or inconsistent"`
}

func issues(ctx context.Context, d tools.Deps, _ struct{}) (issuesOut, error) {
	raw, err := tools.Get[json.RawMessage](ctx, d.API, newapi.Request{Route: newapi.RoutePricingIssues})
	return issuesOut{Issues: raw}, err
}

type updateIn struct {
	Model        string         `json:"model" jsonschema:"model name"`
	Set          map[string]any `json:"set,omitempty" jsonschema:"pricing fields to set, for example {\"ModelRatio\": 1.25, \"CompletionRatio\": 8, \"CacheRatio\": 0.1}"`
	Unset        []string       `json:"unset,omitempty" jsonschema:"pricing fields to remove so the built-in default applies"`
	ConfirmToken string         `json:"confirm_token,omitempty" jsonschema:"token from the preview; omit to preview"`
}

type state struct {
	Version    string         `json:"version"`
	Configured map[string]any `json:"configured"`
}

type mutation struct {
	api newapi.Client
	in  updateIn
}

func (m mutation) Load(ctx context.Context) (state, error) {
	snap, err := tools.Get[snapshot](ctx, m.api, newapi.Request{Route: newapi.RoutePricingGet, Query: url.Values{"model": {m.in.Model}}})
	if err != nil {
		return state{}, err
	}
	for _, e := range snap.Entries {
		if e.ModelName == m.in.Model {
			return state{Version: e.Version, Configured: e.Configured}, nil
		}
	}
	return state{Version: snap.EmptyVersion, Configured: map[string]any{}}, nil
}

// Plan merges set and unset into the model's saved pricing. It is pure.
func (m mutation) Plan(cur state) (state, []guard.Change, error) {
	next := state{Version: cur.Version, Configured: maps.Clone(cur.Configured)}
	if next.Configured == nil {
		next.Configured = map[string]any{}
	}
	for _, key := range m.in.Unset {
		delete(next.Configured, key)
	}
	maps.Copy(next.Configured, m.in.Set)

	var changes []guard.Change
	for _, key := range Keys {
		before, hadBefore := cur.Configured[key]
		after, hasAfter := next.Configured[key]
		if hadBefore == hasAfter && reflect.DeepEqual(before, after) {
			continue
		}
		changes = append(changes, guard.Change{Field: key, Before: before, After: after})
	}
	return next, changes, nil
}

func (m mutation) Apply(ctx context.Context, cur, next state) error {
	_, err := m.api.Do(ctx, newapi.Request{Route: newapi.RoutePricingUpdate, Body: map[string]any{
		"changes": []map[string]any{{"model_name": m.in.Model, "expected_version": cur.Version, "pricing": next.Configured}},
	}}, nil)
	return err
}

type updateOut struct {
	guard.Outcome
	EffectivePreview json.RawMessage `json:"effective_preview,omitempty" jsonschema:"how new-api would bill the model after the change"`
}

func update(ctx context.Context, d tools.Deps, in updateIn) (updateOut, error) {
	if in.Model == "" {
		return updateOut{}, tools.Invalid("model is required")
	}
	if len(in.Set) == 0 && len(in.Unset) == 0 {
		return updateOut{}, tools.Invalid("give set or unset")
	}
	for key := range in.Set {
		if !slices.Contains(Keys, key) {
			return updateOut{}, tools.Invalid("unknown pricing field %q; known fields: %v", key, Keys)
		}
	}
	for _, key := range in.Unset {
		if !slices.Contains(Keys, key) {
			return updateOut{}, tools.Invalid("unknown pricing field %q", key)
		}
		if _, alsoSet := in.Set[key]; alsoSet {
			return updateOut{}, tools.Invalid("field %q is both set and unset", key)
		}
	}
	token := in.ConfirmToken
	in.ConfirmToken = ""
	m := mutation{api: d.API, in: in}
	outcome, err := guard.Run(ctx, d.Guard, "pricing_update", in, token, m)
	if err != nil || outcome.Applied || outcome.NoChange {
		return updateOut{Outcome: outcome}, err
	}
	// Show the effective price of the previewed pricing so the change can be
	// judged in dollars, not ratios.
	cur, err := m.Load(ctx)
	if err != nil {
		return updateOut{Outcome: outcome}, nil
	}
	next, _, _ := m.Plan(cur)
	preview, err := tools.Get[json.RawMessage](ctx, d.API, newapi.Request{Route: newapi.RoutePricingPreview, Body: map[string]any{
		"model_name": in.Model, "pricing": next.Configured,
	}})
	if err != nil {
		return updateOut{Outcome: outcome}, err
	}
	return updateOut{Outcome: outcome, EffectivePreview: preview}, nil
}

// Definitions returns every pricing tool.
func Definitions() []tools.Definition {
	return []tools.Definition{
		tools.Define(tools.Spec{
			Name: "pricing_get", Toolset: "pricing", Level: config.LevelRead, Title: "Model pricing",
			Description: "Without models: list the names of priced models. With models: their saved and effective pricing and the resulting " +
				"price per million tokens or per call.",
			Routes: []newapi.Route{newapi.RoutePricingGet},
		}, get),
		tools.Define(tools.Spec{
			Name: "pricing_issues", Toolset: "pricing", Level: config.LevelRead, Title: "Pricing problems",
			Description: "Models served by enabled channels whose pricing is missing or inconsistent. An unpriced model falls back to a " +
				"high default ratio, so check this after adding a model to a channel.",
			Routes: []newapi.Route{newapi.RoutePricingIssues},
		}, issues),
		tools.Define(tools.Spec{
			Name: "pricing_update", Toolset: "pricing", Level: config.LevelWrite, Title: "Change a model's pricing",
			Description: "Set or remove pricing fields of one model; other models are never touched. Ratios are relative to 2 USD per " +
				"million input tokens for ModelRatio=1; ModelPrice is USD per call. Two steps: without confirm_token it returns the diff, " +
				"the resulting effective price and a confirm_token; call again with the same arguments plus confirm_token to apply.",
			Routes: []newapi.Route{newapi.RoutePricingGet, newapi.RoutePricingPreview, newapi.RoutePricingUpdate},
		}, update),
	}
}
