package logs

import (
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/tools/toolstest"
)

func TestSearchBuildsQueryAndShapesEntries(t *testing.T) {
	fake := toolstest.New().Data(newapi.RouteLogList, toolstest.JSON(`{"items":[{
		"id":9,"created_at":1800000000,"type":5,"username":"bob","model_name":"gpt-6.1","channel":15,"quota":500000,
		"content":"[{\"key\":\"upstream error: {{msg}}\",\"params\":{\"msg\":\"503\"}}]",
		"other":"{\"frt\":2300,\"admin_info\":{\"x\":1}}"}],"total":1}`))
	out, err := toolstest.Run(toolstest.Find(Definitions(), "logs_search"), toolstest.Deps(fake), `{"type":"error","channel":15,"hours":2}`)
	require.NoError(t, err)

	q, err := url.ParseQuery(fake.Calls[0].Query)
	require.NoError(t, err)
	assert.Equal(t, "5", q.Get("type"))
	assert.Equal(t, "15", q.Get("channel"))
	assert.Equal(t, strconv.FormatInt(toolstest.Clock.Unix()-7200, 10), q.Get("start_timestamp"))

	entry := out.(searchOut).Items[0]
	assert.Equal(t, "error", entry.Type)
	assert.Equal(t, "upstream error: 503", entry.UntrustedContent)
	assert.Equal(t, 1.0, entry.CostUSD)
	require.NotNil(t, entry.FirstTokenMs)
	assert.Equal(t, 2300.0, *entry.FirstTokenMs)
	assert.Nil(t, entry.UntrustedOther, "metadata only with detail=true")
}

func TestSearchRejectsWideWindowsAndUnknownTypes(t *testing.T) {
	def := toolstest.Find(Definitions(), "logs_search")
	deps := toolstest.Deps(toolstest.New())
	for _, args := range []string{`{"hours":200}`, `{"type":"debug"}`} {
		_, err := toolstest.Run(def, deps, args)
		assert.Error(t, err, args)
	}
}

func TestAffinityClearNeedsExplicitScope(t *testing.T) {
	fake := toolstest.New().Data(newapi.RouteAffinityCacheClear, toolstest.JSON(`{"deleted":3}`))
	def := toolstest.Find(Definitions(), "affinity_cache_clear")
	_, err := toolstest.Run(def, toolstest.Deps(fake), `{}`)
	assert.Error(t, err)
	_, err = toolstest.Run(def, toolstest.Deps(fake), `{"rule_name":"example rule"}`)
	require.NoError(t, err)
	assert.Equal(t, "rule_name=example+rule", fake.Calls[0].Query)
}
