// Package option holds the system option tools. Options whose value is a JSON
// object change by key-level patch, never by whole-value replacement. Which
// options a caller may change is decided by new-api from the caller's token.
package option

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
	"github.com/fengcunhan/new-api-mcp/internal/tools/pricing"
)

type rawOption struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func load(ctx context.Context, api newapi.Client) (map[string]string, error) {
	list, err := tools.Get[[]rawOption](ctx, api, newapi.Request{Route: newapi.RouteOptionList})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for _, o := range list {
		out[o.Key] = o.Value
	}
	return out, nil
}

type getIn struct {
	Keys   []string `json:"keys,omitempty" jsonschema:"option keys to read"`
	Prefix string   `json:"prefix,omitempty" jsonschema:"read every option whose key starts with this text"`
}

type getOut struct {
	Values map[string]string `json:"values,omitempty"`
	Keys   []string          `json:"keys,omitempty" jsonschema:"all readable option keys, returned when neither keys nor prefix is given"`
}

const maxValueChars = 8000

func get(ctx context.Context, d tools.Deps, in getIn) (getOut, error) {
	all, err := load(ctx, d.API)
	if err != nil {
		return getOut{}, err
	}
	if len(in.Keys) == 0 && in.Prefix == "" {
		return getOut{Keys: slices.Sorted(maps.Keys(all))}, nil
	}
	out := getOut{Values: map[string]string{}}
	for key, value := range all {
		if slices.Contains(in.Keys, key) || (in.Prefix != "" && strings.HasPrefix(key, in.Prefix)) {
			if len(value) > maxValueChars {
				value = value[:maxValueChars] + fmt.Sprintf("…[truncated %d bytes]", len(value)-maxValueChars)
			}
			out.Values[key] = value
		}
	}
	for _, key := range in.Keys {
		if _, ok := all[key]; !ok {
			out.Values[key] = "<not readable or not set>"
		}
	}
	return out, nil
}

// hidden stands for an option new-api does not return, such as a secret.
// Such options can still be set with value; their content is never shown.
const hidden = "\x00hidden"

type setIn struct {
	Key          string         `json:"key" jsonschema:"option key"`
	Value        *string        `json:"value,omitempty" jsonschema:"new value for a scalar option (text, number or true/false as text)"`
	Set          map[string]any `json:"set,omitempty" jsonschema:"for options holding a JSON object: entries to add or replace"`
	Unset        []string       `json:"unset,omitempty" jsonschema:"for options holding a JSON object: entries to remove"`
	ConfirmToken string         `json:"confirm_token,omitempty" jsonschema:"token from the preview; omit to preview"`
}

type mutation struct {
	api newapi.Client
	in  setIn
}

func (m mutation) Load(ctx context.Context) (string, error) {
	all, err := load(ctx, m.api)
	if err != nil {
		return "", err
	}
	value, ok := all[m.in.Key]
	if !ok {
		return hidden, nil
	}
	return value, nil
}

// Plan computes the new value. A JSON-object option is merged entry by entry
// so unrelated entries survive. It is pure.
func (m mutation) Plan(cur string) (string, []guard.Change, error) {
	var object map[string]any
	isObject := json.Unmarshal([]byte(cur), &object) == nil && object != nil
	patch := m.in.Set != nil || m.in.Unset != nil

	if cur == hidden {
		if patch || m.in.Value == nil {
			return "", nil, tools.Invalid("option %q is not readable (a secret or not yet set); set it with value", m.in.Key)
		}
		return *m.in.Value, []guard.Change{{Field: m.in.Key, Before: "<hidden or unset>", After: "<new value, not shown>"}}, nil
	}

	if !isObject {
		if patch {
			return "", nil, tools.Invalid("option %q does not hold a JSON object; use value", m.in.Key)
		}
		if m.in.Value == nil {
			return "", nil, tools.Invalid("value is required")
		}
		if *m.in.Value == cur {
			return cur, nil, nil
		}
		return *m.in.Value, []guard.Change{{Field: m.in.Key, Before: cur, After: *m.in.Value}}, nil
	}

	if m.in.Value != nil {
		return "", nil, tools.Invalid("option %q holds a JSON object; change it with set/unset so other entries are kept", m.in.Key)
	}
	if !patch {
		return "", nil, tools.Invalid("give set or unset")
	}
	next := maps.Clone(object)
	for _, k := range m.in.Unset {
		delete(next, k)
	}
	maps.Copy(next, m.in.Set)

	keys := slices.Collect(maps.Keys(next))
	for k := range object {
		if _, kept := next[k]; !kept {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var changes []guard.Change
	for _, k := range keys {
		before, had := object[k]
		after, has := next[k]
		if had == has && reflect.DeepEqual(before, after) {
			continue
		}
		changes = append(changes, guard.Change{Field: m.in.Key + "." + k, Before: before, After: after})
	}
	if len(changes) == 0 {
		return cur, nil, nil
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return "", nil, err
	}
	return string(encoded), changes, nil
}

func (m mutation) Apply(ctx context.Context, _, next string) error {
	_, err := m.api.Do(ctx, newapi.Request{Route: newapi.RouteOptionUpdate, Body: map[string]any{"key": m.in.Key, "value": next}}, nil)
	return err
}

func set(ctx context.Context, d tools.Deps, in setIn) (guard.Outcome, error) {
	if in.Key == "" {
		return guard.Outcome{}, tools.Invalid("key is required")
	}
	if slices.Contains(pricing.Keys, in.Key) {
		return guard.Outcome{}, tools.Invalid("%s is model pricing; use pricing_update, which changes one model at a time", in.Key)
	}
	token := in.ConfirmToken
	in.ConfirmToken = ""
	return guard.Run(ctx, d.Guard, "option_set", in, token, mutation{api: d.API, in: in})
}

// Definitions returns every option tool.
func Definitions() []tools.Definition {
	return []tools.Definition{
		tools.Define(tools.Spec{
			Name: "option_get", Toolset: "option", Level: config.LevelRead, Title: "Read system options",
			Description: "Read system options by key or key prefix; with no arguments, list the readable keys. Secrets are never returned.",
			Routes:      []newapi.Route{newapi.RouteOptionList},
		}, get),
		tools.Define(tools.Spec{
			Name: "option_set", Toolset: "option", Level: config.LevelWrite, Title: "Change a system option",
			Description: "Change one system option. Options holding a JSON object (GroupRatio, channel affinity rules, ...) are " +
				"patched entry by entry with set/unset; other entries are kept. Options new-api does not return (secrets) can be set with value. " +
				"Model pricing fields go through pricing_update. " +
				"Two steps: preview without confirm_token, then repeat with confirm_token to apply.",
			Routes: []newapi.Route{newapi.RouteOptionList, newapi.RouteOptionUpdate},
		}, set),
	}
}
