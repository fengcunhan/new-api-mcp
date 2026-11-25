package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/tools/toolstest"
)

const alice = `{"id":3,"username":"alice","display_name":"Alice","role":1,"status":1,"quota":1000000,"used_quota":250000,"group":"default","remark":"ops"}`

func TestAddQuotaPreviewThenSubtract(t *testing.T) {
	fake := toolstest.New().Data(newapi.RouteUserGet, toolstest.JSON(alice)).Data(newapi.RouteUserManage, nil)
	deps := toolstest.Deps(fake)
	def := toolstest.Find(Definitions(), "user_add_quota")

	out, err := toolstest.Run(def, deps, `{"id":3,"amount_usd":-0.5}`)
	require.NoError(t, err)
	preview := out.(guard.Outcome)
	assert.Equal(t, 2.0, preview.Changes[0].Before)
	assert.Equal(t, 1.5, preview.Changes[0].After)

	_, err = toolstest.Run(def, deps, `{"id":3,"amount_usd":-0.5,"confirm_token":"`+preview.ConfirmToken+`"}`)
	require.NoError(t, err)
	calls := fake.CallsTo(newapi.RouteUserManage)
	require.Len(t, calls, 1)
	assert.JSONEq(t, `{"id":3,"action":"add_quota","mode":"subtract","value":250000}`, string(calls[0].Body))
}

func TestAddQuotaBounds(t *testing.T) {
	fake := toolstest.New().Data(newapi.RouteUserGet, toolstest.JSON(alice))
	def := toolstest.Find(Definitions(), "user_add_quota")
	for _, args := range []string{`{"id":3,"amount_usd":0}`, `{"id":3,"amount_usd":-3}`} {
		_, err := toolstest.Run(def, toolstest.Deps(fake), args)
		assert.Error(t, err, args)
	}
	out, err := toolstest.Run(def, toolstest.Deps(fake), `{"id":3,"amount_usd":5000}`)
	require.NoError(t, err, "no ceiling beyond what new-api enforces")
	assert.Equal(t, 5002.0, out.(guard.Outcome).Changes[0].After)
}

func TestUpdateProfileSendsAllFourFields(t *testing.T) {
	fake := toolstest.New().Data(newapi.RouteUserGet, toolstest.JSON(alice)).Data(newapi.RouteUserUpdate, nil)
	deps := toolstest.Deps(fake)
	def := toolstest.Find(Definitions(), "user_update_profile")
	out, err := toolstest.Run(def, deps, `{"id":3,"group":"vip"}`)
	require.NoError(t, err)
	preview := out.(guard.Outcome)
	assert.Equal(t, []guard.Change{{Field: "group", Before: "default", After: "vip"}}, preview.Changes)

	_, err = toolstest.Run(def, deps, `{"id":3,"group":"vip","confirm_token":"`+preview.ConfirmToken+`"}`)
	require.NoError(t, err)
	calls := fake.CallsTo(newapi.RouteUserUpdate)
	require.Len(t, calls, 1)
	assert.JSONEq(t, `{"id":3,"username":"alice","display_name":"Alice","group":"vip","remark":"ops"}`, string(calls[0].Body))
}

func TestViewHidesNothingSecretAndConvertsMoney(t *testing.T) {
	fake := toolstest.New().Data(newapi.RouteUserGet, toolstest.JSON(alice))
	out, err := toolstest.Run(toolstest.Find(Definitions(), "user_get"), toolstest.Deps(fake), `{"id":3}`)
	require.NoError(t, err)
	u := out.(User)
	assert.Equal(t, "user", u.Role)
	assert.Equal(t, 2.0, u.BalanceUSD)
	assert.Equal(t, 0.5, u.UsedUSD)
}
