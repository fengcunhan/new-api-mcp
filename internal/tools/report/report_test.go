package report

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/tools/toolstest"
)

var rows = []quotaRow{
	{Username: "a", ModelName: "m1", CreatedAt: 3600, Count: 1, Quota: 500000, TokenUsed: 10},
	{Username: "b", ModelName: "m1", CreatedAt: 3600, Count: 2, Quota: 250000, TokenUsed: 20},
	{Username: "a", ModelName: "m2", CreatedAt: 7300, Count: 3, Quota: 1500000, TokenUsed: 30},
}

func TestAggregate(t *testing.T) {
	byModel := Aggregate(rows, "model")
	require.Len(t, byModel, 2)
	assert.Equal(t, "m2", byModel[0].Key, "highest cost first")
	assert.Equal(t, 3.0, byModel[0].CostUSD)
	assert.Equal(t, int64(3), byModel[1].Requests)

	byUser := Aggregate(rows, "user")
	assert.Equal(t, "a", byUser[0].Key)
	assert.Equal(t, int64(40), byUser[0].Tokens)

	byHour := Aggregate(rows, "hour")
	require.Len(t, byHour, 2)
	assert.Less(t, byHour[0].Key, byHour[1].Key, "hours in time order")
}

func TestQuotaReportTopAndTotals(t *testing.T) {
	fake := toolstest.New().Data(newapi.RouteDataQuota, rows)
	out, err := toolstest.Run(toolstest.Find(Definitions(), "usage_quota_report"), toolstest.Deps(fake), `{"top":1}`)
	require.NoError(t, err)
	report := out.(quotaOut)
	assert.Len(t, report.Rows, 1)
	assert.Equal(t, 2, report.TotalRows)
	assert.Equal(t, 4.5, report.TotalCostUSD)

	_, err = toolstest.Run(toolstest.Find(Definitions(), "usage_quota_report"), toolstest.Deps(fake), `{"group_by":"user","username":"a"}`)
	assert.Error(t, err)
}
