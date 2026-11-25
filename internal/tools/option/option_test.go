package option

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/tools/toolstest"
)

func TestPlanMergesObjectOptions(t *testing.T) {
	cur := `{"default":1,"vip":0.8,"svip":0.5}`
	tests := []struct {
		name    string
		in      setIn
		want    map[string]any
		fields  []string
		wantErr string
	}{
		{"set one entry keeps the rest", setIn{Key: "GroupRatio", Set: map[string]any{"vip": 0.7}},
			map[string]any{"default": 1.0, "vip": 0.7, "svip": 0.5}, []string{"GroupRatio.vip"}, ""},
		{"add and remove", setIn{Key: "GroupRatio", Set: map[string]any{"team": 0.9}, Unset: []string{"svip"}},
			map[string]any{"default": 1.0, "vip": 0.8, "team": 0.9}, []string{"GroupRatio.svip", "GroupRatio.team"}, ""},
		{"whole value refused", setIn{Key: "GroupRatio", Value: ptr(`{"default":1}`)}, nil, nil, "set/unset"},
		{"patch needs set or unset", setIn{Key: "GroupRatio"}, nil, nil, "give set or unset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, changes, err := mutation{in: tt.in}.Plan(cur)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(next), &got))
			assert.Equal(t, tt.want, got)
			var fields []string
			for _, c := range changes {
				fields = append(fields, c.Field)
			}
			assert.Equal(t, tt.fields, fields)
		})
	}
}

func TestPlanScalarOptions(t *testing.T) {
	next, changes, err := mutation{in: setIn{Key: "RetryTimes", Value: ptr("2")}}.Plan("3")
	require.NoError(t, err)
	assert.Equal(t, "2", next)
	assert.Equal(t, []guard.Change{{Field: "RetryTimes", Before: "3", After: "2"}}, changes)

	_, changes, err = mutation{in: setIn{Key: "RetryTimes", Value: ptr("3")}}.Plan("3")
	require.NoError(t, err)
	assert.Empty(t, changes)

	_, _, err = mutation{in: setIn{Key: "RetryTimes", Set: map[string]any{"a": 1}}}.Plan("3")
	assert.ErrorContains(t, err, "does not hold a JSON object")
}

func TestSetSendsPricingFieldsToPricingUpdate(t *testing.T) {
	deps := toolstest.Deps(toolstest.New())
	def := toolstest.Find(Definitions(), "option_set")
	for _, key := range []string{"ModelRatio", "CacheRatio"} {
		_, err := toolstest.Run(def, deps, `{"key":"`+key+`","value":"x"}`)
		assert.ErrorContains(t, err, "pricing_update", key)
	}
}

// Whether a caller may change an option is new-api's decision: sign-in and
// payment options pass through, and unreadable secrets can be set without
// their value appearing in the diff.
func TestSetLeavesPermissionToNewAPI(t *testing.T) {
	fake := toolstest.New().
		Data(newapi.RouteOptionList, []rawOption{{Key: "PasswordLoginEnabled", Value: "true"}}).
		Data(newapi.RouteOptionUpdate, nil)
	deps := toolstest.Deps(fake)
	def := toolstest.Find(Definitions(), "option_set")

	out, err := toolstest.Run(def, deps, `{"key":"PasswordLoginEnabled","value":"false"}`)
	require.NoError(t, err)
	assert.NotEmpty(t, out.(guard.Outcome).ConfirmToken)

	out, err = toolstest.Run(def, deps, `{"key":"StripeApiSecret","value":"sk_live_x"}`)
	require.NoError(t, err)
	preview := out.(guard.Outcome)
	assert.Equal(t, []guard.Change{{Field: "StripeApiSecret", Before: "<hidden or unset>", After: "<new value, not shown>"}}, preview.Changes)
	_, err = toolstest.Run(def, deps, `{"key":"StripeApiSecret","value":"sk_live_x","confirm_token":"`+preview.ConfirmToken+`"}`)
	require.NoError(t, err)
	calls := fake.CallsTo(newapi.RouteOptionUpdate)
	require.Len(t, calls, 1)
	assert.JSONEq(t, `{"key":"StripeApiSecret","value":"sk_live_x"}`, string(calls[0].Body))

	_, err = toolstest.Run(def, deps, `{"key":"StripeApiSecret","set":{"a":1}}`)
	assert.ErrorContains(t, err, "set it with value")
}

func TestSetPreviewThenApplySendsMergedObject(t *testing.T) {
	fake := toolstest.New().
		Data(newapi.RouteOptionList, []rawOption{{Key: "GroupRatio", Value: `{"default":1,"vip":0.8}`}}).
		Data(newapi.RouteOptionUpdate, nil)
	deps := toolstest.Deps(fake)
	def := toolstest.Find(Definitions(), "option_set")
	out, err := toolstest.Run(def, deps, `{"key":"GroupRatio","set":{"vip":0.6}}`)
	require.NoError(t, err)
	token := out.(guard.Outcome).ConfirmToken
	require.NotEmpty(t, token)

	_, err = toolstest.Run(def, deps, `{"key":"GroupRatio","set":{"vip":0.6},"confirm_token":"`+token+`"}`)
	require.NoError(t, err)
	calls := fake.CallsTo(newapi.RouteOptionUpdate)
	require.Len(t, calls, 1)
	assert.JSONEq(t, `{"key":"GroupRatio","value":"{\"default\":1,\"vip\":0.6}"}`, string(calls[0].Body))
}

func TestGetListsKeysAndTruncates(t *testing.T) {
	fake := toolstest.New().Data(newapi.RouteOptionList, []rawOption{{Key: "B", Value: "2"}, {Key: "A", Value: "1"}})
	out, err := toolstest.Run(toolstest.Find(Definitions(), "option_get"), toolstest.Deps(fake), `{}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B"}, out.(getOut).Keys)
}

func ptr[T any](v T) *T { return &v }
