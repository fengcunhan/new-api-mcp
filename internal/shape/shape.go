// Package shape holds pure helpers that keep tool output small and safe:
// pagination, truncation, time handling and money conversion.
package shape

import (
	"fmt"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	// TextLimit bounds a single free-text field such as an error message.
	TextLimit = 500
)

// Page is the pagination input shared by list tools.
type Page struct {
	Page     int `json:"page,omitempty" jsonschema:"page number starting at 1 (default 1)"`
	PageSize int `json:"page_size,omitempty" jsonschema:"items per page (default 20, max 100)"`
}

// Normalized returns the page with defaults and bounds applied.
func (p Page) Normalized() Page {
	page, size := p.Page, p.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = DefaultPageSize
	}
	return Page{Page: page, PageSize: min(size, MaxPageSize)}
}

// Query writes new-api's pagination parameters (p, page_size).
func (p Page) Query(q url.Values) {
	n := p.Normalized()
	q.Set("p", strconv.Itoa(n.Page))
	q.Set("page_size", strconv.Itoa(n.PageSize))
}

// PageInfo is the pagination output shared by list tools.
type PageInfo struct {
	Page     int  `json:"page"`
	PageSize int  `json:"page_size"`
	Total    int  `json:"total"`
	HasMore  bool `json:"has_more"`
}

func NewPageInfo(p Page, total int) PageInfo {
	n := p.Normalized()
	return PageInfo{Page: n.Page, PageSize: n.PageSize, Total: total, HasMore: n.Page*n.PageSize < total}
}

// Truncate shortens s to at most limit runes and marks the cut.
func Truncate(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + fmt.Sprintf("…[truncated %d chars]", len(runes)-limit)
}

// Time formats a unix timestamp in the process timezone (set TZ, for example
// TZ=Asia/Shanghai) so reports read the same as the console. Zero stays empty.
func Time(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).Format("2006-01-02 15:04:05")
}

// Range is a time window input. Timestamps are unix seconds; hours is a
// shorthand for "the last N hours" when no start is given.
type Range struct {
	StartTimestamp int64 `json:"start_timestamp,omitempty" jsonschema:"window start, unix seconds"`
	EndTimestamp   int64 `json:"end_timestamp,omitempty" jsonschema:"window end, unix seconds (default now)"`
	Hours          int   `json:"hours,omitempty" jsonschema:"shorthand for the last N hours when start_timestamp is not given"`
}

// Resolve returns concrete start and end, filling the end with now and the
// start from hours or defaultHours. maxDays bounds the window.
func (r Range) Resolve(now time.Time, defaultHours, maxDays int) (start, end int64, err error) {
	end = r.EndTimestamp
	if end <= 0 {
		end = now.Unix()
	}
	start = r.StartTimestamp
	if start <= 0 {
		hours := r.Hours
		if hours <= 0 {
			hours = defaultHours
		}
		start = end - int64(hours)*3600
	}
	if start >= end {
		return 0, 0, fmt.Errorf("start must be before end")
	}
	if maxDays > 0 && end-start > int64(maxDays)*86400 {
		return 0, 0, fmt.Errorf("the window cannot exceed %d days", maxDays)
	}
	return start, end, nil
}

// QuotaPerUSD is new-api's default quota units per US dollar.
const QuotaPerUSD = 500000.0

// USD converts quota units to dollars, rounded to 4 decimals.
func USD(quota int64) float64 {
	return float64(int64(float64(quota)/QuotaPerUSD*10000+0.5)) / 10000
}
