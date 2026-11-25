package shape

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPage(t *testing.T) {
	assert.Equal(t, Page{1, 20}, Page{}.Normalized())
	assert.Equal(t, Page{3, 100}, Page{3, 500}.Normalized())
	q := url.Values{}
	Page{2, 50}.Query(q)
	assert.Equal(t, "p=2&page_size=50", q.Encode())
	assert.Equal(t, PageInfo{Page: 2, PageSize: 50, Total: 101, HasMore: true}, NewPageInfo(Page{2, 50}, 101))
	assert.False(t, NewPageInfo(Page{3, 50}, 150).HasMore)
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "短文本", Truncate("短文本", 5))
	assert.Equal(t, "一二…[truncated 3 chars]", Truncate("一二三四五", 2))
}

func TestRangeResolve(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	start, end, err := Range{}.Resolve(now, 24, 30)
	require.NoError(t, err)
	assert.Equal(t, now.Unix(), end)
	assert.Equal(t, now.Unix()-24*3600, start)

	start, _, err = Range{Hours: 2}.Resolve(now, 24, 30)
	require.NoError(t, err)
	assert.Equal(t, now.Unix()-7200, start)

	_, _, err = Range{StartTimestamp: now.Unix() - 40*86400}.Resolve(now, 24, 30)
	assert.ErrorContains(t, err, "30 days")

	_, _, err = Range{StartTimestamp: now.Unix() + 10}.Resolve(now, 24, 30)
	assert.Error(t, err)
}

func TestUSD(t *testing.T) {
	assert.Equal(t, 1.0, USD(500000))
	assert.Equal(t, 0.0025, USD(1250))
}
