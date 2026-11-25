package channel

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/tools/toolstest"
)

const channel15 = `{
  "id": 15, "type": 59, "status": 3, "name": "upstream-a", "key": "sk-secret-must-not-leak",
  "base_url": "http://user:pass@203.0.113.10:8099", "models": "gpt-6.1,gpt-6.2-sol", "group": "default",
  "priority": 10, "weight": 1, "used_quota": 1000000, "tag": "codex", "auto_ban": 1,
  "other_info": "{\"status_reason\":\"upstream 503\",\"status_time\":1800000000}",
  "header_override": "{\"Authorization\":\"Bearer sk-inner\"}",
  "setting": "{\"proxy\":\"socks5://u:p@198.51.100.7:1080\"}",
  "channel_info": {"is_multi_key": true, "multi_key_size": 3, "multi_key_status_list": {"0": 3, "2": 2}}
}`

func TestListAndGetNeverLeakSecrets(t *testing.T) {
	fake := toolstest.New().
		Data(newapi.RouteChannelList, toolstest.JSON(`{"items":[`+channel15+`],"total":41}`)).
		Data(newapi.RouteChannelGet, toolstest.JSON(channel15))
	deps := toolstest.Deps(fake)
	defs := Definitions()

	out, err := toolstest.Run(toolstest.Find(defs, "channel_list"), deps, `{"status":"disabled","page_size":10}`)
	require.NoError(t, err)
	list := out.(listOut)
	require.Len(t, list.Items, 1)
	s := list.Items[0]
	assert.Equal(t, "auto_disabled", s.Status)
	assert.Equal(t, "upstream 503", s.DisabledReason)
	assert.Equal(t, "Sub2API", s.Type)
	assert.Equal(t, 2.0, s.UsedUSD)
	assert.Equal(t, &Keys{Size: 3, Disabled: 2}, s.MultiKey)
	assert.True(t, list.Page.HasMore)
	assert.Equal(t, "p=1&page_size=10&status=disabled", fake.Calls[0].Query)

	out, err = toolstest.Run(toolstest.Find(defs, "channel_get"), deps, `{"id":15}`)
	require.NoError(t, err)
	encoded, _ := json.Marshal(out)
	for _, secret := range []string{"sk-secret-must-not-leak", "sk-inner", "pass@", "u:p@"} {
		assert.NotContains(t, string(encoded), secret)
	}
	assert.Equal(t, []string{"Authorization"}, out.(Detail).HeaderOverrideKey)
}

func TestListUsesSearchForKeyword(t *testing.T) {
	fake := toolstest.New().Data(newapi.RouteChannelSearch, toolstest.JSON(`{"items":[],"total":0}`))
	_, err := toolstest.Run(toolstest.Find(Definitions(), "channel_list"), toolstest.Deps(fake), `{"model":"gpt-6"}`)
	require.NoError(t, err)
	assert.Len(t, fake.CallsTo(newapi.RouteChannelSearch), 1)

	_, err = toolstest.Run(toolstest.Find(Definitions(), "channel_list"), toolstest.Deps(fake), `{"status":"broken"}`)
	assert.Error(t, err)
}

func TestTestReportsFailureAsResult(t *testing.T) {
	fake := toolstest.New().On(newapi.RouteChannelTest, func(newapi.Request) (any, error) {
		return toolstest.RawEnvelope(`{"success":false,"message":"status 401","time":1.25,"error_code":"bad_key"}`),
			&newapi.Error{Kind: newapi.KindBusiness, Message: "status 401"}
	})
	out, err := toolstest.Run(toolstest.Find(Definitions(), "channel_test"), toolstest.Deps(fake), `{"id":15,"model":"gpt-6.1"}`)
	require.NoError(t, err)
	assert.Equal(t, testOut{OK: false, Seconds: 1.25, Message: "status 401", ErrorCode: "bad_key"}, out)
	assert.Equal(t, "model=gpt-6.1", fake.Calls[0].Query)
}

func TestSetStatusReportsBeforeAndAfter(t *testing.T) {
	status := 3
	fake := toolstest.New()
	fake.On(newapi.RouteChannelGet, func(newapi.Request) (any, error) {
		return map[string]any{"id": 15, "name": "upstream-a", "status": status}, nil
	})
	fake.On(newapi.RouteChannelStatus, func(req newapi.Request) (any, error) {
		status = req.Body.(map[string]int)["status"]
		return true, nil
	})
	out, err := toolstest.Run(toolstest.Find(Definitions(), "channel_set_status"), toolstest.Deps(fake), `{"ids":[15],"enabled":true}`)
	require.NoError(t, err)
	assert.Equal(t, []statusRow{{ID: 15, Name: "upstream-a", Before: "auto_disabled", After: "enabled"}}, out.(setStatusOut).Channels)

	_, err = toolstest.Run(toolstest.Find(Definitions(), "channel_set_status"), toolstest.Deps(fake), `{"ids":[1,1],"enabled":true}`)
	assert.ErrorContains(t, err, "duplicate")
	_, err = toolstest.Run(toolstest.Find(Definitions(), "channel_set_status"), toolstest.Deps(fake), `{"ids":[],"enabled":true}`)
	assert.Error(t, err)
}

func TestUpdatePlan(t *testing.T) {
	cur := state{Name: "a", Models: []string{"m1", "m2"}, Priority: 0, AutoBan: true}
	tests := []struct {
		name    string
		in      updateIn
		want    state
		fields  []string
		wantErr string
	}{
		{"add and remove models", updateIn{AddModels: []string{"m3", "m1"}, RemoveModels: []string{"m2"}},
			state{Name: "a", Models: []string{"m1", "m3"}, AutoBan: true}, []string{"models"}, ""},
		{"replace models deduped", updateIn{Models: []string{" m9 ", "m9"}},
			state{Name: "a", Models: []string{"m9"}, AutoBan: true}, []string{"models"}, ""},
		{"priority and auto ban", updateIn{Priority: ptr(int64(10)), AutoBan: ptr(false)},
			state{Name: "a", Models: []string{"m1", "m2"}, Priority: 10}, []string{"priority", "auto_ban"}, ""},
		{"same value is no change", updateIn{Name: ptr("a")}, cur, nil, ""},
		{"models and add together", updateIn{Models: []string{"x"}, AddModels: []string{"y"}}, state{}, nil, "either models"},
		{"remove every model", updateIn{RemoveModels: []string{"m1", "m2"}}, state{}, nil, "at least one model"},
		{"bad mapping", updateIn{ModelMapping: ptr("[1]")}, state{}, nil, "JSON object"},
		{"empty name", updateIn{Name: ptr(" ")}, state{}, nil, "name cannot be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, changes, err := updateMutation{in: tt.in}.Plan(cur)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, next)
			var fields []string
			for _, c := range changes {
				fields = append(fields, c.Field)
			}
			assert.Equal(t, tt.fields, fields)
		})
	}
	assert.Equal(t, []string{"m1", "m2"}, cur.Models, "plan must not mutate the current state")
}

func TestUpdatePreviewThenApplySendsOnlyChangedFields(t *testing.T) {
	fake := toolstest.New().
		Data(newapi.RouteChannelGet, toolstest.JSON(channel15)).
		Data(newapi.RouteChannelUpdate, true)
	deps := toolstest.Deps(fake)
	def := toolstest.Find(Definitions(), "channel_update")

	out, err := toolstest.Run(def, deps, `{"id":15,"priority":20}`)
	require.NoError(t, err)
	preview := out.(guard.Outcome)
	assert.False(t, preview.Applied)
	assert.Empty(t, fake.CallsTo(newapi.RouteChannelUpdate))

	out, err = toolstest.Run(def, deps, `{"id":15,"priority":20,"confirm_token":"`+preview.ConfirmToken+`"}`)
	require.NoError(t, err)
	assert.True(t, out.(guard.Outcome).Applied)
	calls := fake.CallsTo(newapi.RouteChannelUpdate)
	require.Len(t, calls, 1)
	assert.JSONEq(t, `{"id":15,"priority":20}`, string(calls[0].Body))
}

func ptr[T any](v T) *T { return &v }
