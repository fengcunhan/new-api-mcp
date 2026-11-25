package pricing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/tools/toolstest"
)

func TestPlanTouchesOnlyRequestedFields(t *testing.T) {
	cur := state{Version: "v1", Configured: map[string]any{"ModelRatio": 1.0, "CompletionRatio": 4.0}}
	next, changes, err := mutation{in: updateIn{Model: "m", Set: map[string]any{"CacheRatio": 0.2}, Unset: []string{"CompletionRatio"}}}.Plan(cur)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"ModelRatio": 1.0, "CacheRatio": 0.2}, next.Configured)
	assert.Equal(t, []guard.Change{
		{Field: "CompletionRatio", Before: 4.0, After: nil},
		{Field: "CacheRatio", Before: nil, After: 0.2},
	}, changes)
	assert.Equal(t, map[string]any{"ModelRatio": 1.0, "CompletionRatio": 4.0}, cur.Configured, "plan must not mutate")

	_, changes, err = mutation{in: updateIn{Model: "m", Set: map[string]any{"ModelRatio": 1.0}}}.Plan(cur)
	require.NoError(t, err)
	assert.Empty(t, changes)
}

func TestUpdateRejectsBadInput(t *testing.T) {
	deps := toolstest.Deps(toolstest.New())
	def := toolstest.Find(Definitions(), "pricing_update")
	for _, args := range []string{
		`{"model":"m"}`,
		`{"model":"m","set":{"GroupRatio":1}}`,
		`{"model":"m","set":{"ModelRatio":1},"unset":["ModelRatio"]}`,
		`{"model":"","set":{"ModelRatio":1}}`,
	} {
		_, err := toolstest.Run(def, deps, args)
		assert.Error(t, err, args)
	}
}

func TestUpdateSendsExpectedVersionForOneModel(t *testing.T) {
	fake := toolstest.New().
		Data(newapi.RoutePricingGet, toolstest.JSON(`{"entries":[{"model_name":"glm-5.4","version":"abc","configured":{"ModelRatio":2}}],"empty_version":"e"}`)).
		Data(newapi.RoutePricingPreview, toolstest.JSON(`{"effective":{"ModelRatio":1.5}}`)).
		Data(newapi.RoutePricingUpdate, toolstest.JSON(`{"updated_models":["glm-5.4"]}`))
	deps := toolstest.Deps(fake)
	def := toolstest.Find(Definitions(), "pricing_update")

	out, err := toolstest.Run(def, deps, `{"model":"glm-5.4","set":{"ModelRatio":1.5}}`)
	require.NoError(t, err)
	preview := out.(updateOut)
	assert.JSONEq(t, `{"effective":{"ModelRatio":1.5}}`, string(preview.EffectivePreview))
	assert.Empty(t, fake.CallsTo(newapi.RoutePricingUpdate))

	_, err = toolstest.Run(def, deps, `{"model":"glm-5.4","set":{"ModelRatio":1.5},"confirm_token":"`+preview.ConfirmToken+`"}`)
	require.NoError(t, err)
	calls := fake.CallsTo(newapi.RoutePricingUpdate)
	require.Len(t, calls, 1)
	assert.JSONEq(t, `{"changes":[{"model_name":"glm-5.4","expected_version":"abc","pricing":{"ModelRatio":1.5}}]}`, string(calls[0].Body))
}

func TestUpdateUnpricedModelUsesEmptyVersion(t *testing.T) {
	fake := toolstest.New().Data(newapi.RoutePricingGet, toolstest.JSON(`{"entries":[],"empty_version":"empty"}`))
	got, err := mutation{api: fake, in: updateIn{Model: "new-model"}}.Load(toolstest.Ctx())
	require.NoError(t, err)
	assert.Equal(t, state{Version: "empty", Configured: map[string]any{}}, got)
}
