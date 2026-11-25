// Package channel holds the channel tools.
package channel

import (
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/fengcunhan/new-api-mcp/internal/shape"
)

// rawChannel decodes new-api's channel. It has no key field, so a key can
// never be decoded, let alone returned.
type rawChannel struct {
	ID                int     `json:"id"`
	Type              int     `json:"type"`
	Status            int     `json:"status"`
	Name              string  `json:"name"`
	Weight            *uint   `json:"weight"`
	CreatedTime       int64   `json:"created_time"`
	TestTime          int64   `json:"test_time"`
	ResponseTime      int     `json:"response_time"`
	BaseURL           *string `json:"base_url"`
	Balance           float64 `json:"balance"`
	Models            string  `json:"models"`
	Group             string  `json:"group"`
	UsedQuota         int64   `json:"used_quota"`
	ModelMapping      *string `json:"model_mapping"`
	StatusCodeMapping *string `json:"status_code_mapping"`
	Priority          *int64  `json:"priority"`
	AutoBan           *int    `json:"auto_ban"`
	OtherInfo         string  `json:"other_info"`
	Tag               *string `json:"tag"`
	Setting           *string `json:"setting"`
	ParamOverride     *string `json:"param_override"`
	HeaderOverride    *string `json:"header_override"`
	Remark            *string `json:"remark"`
	TestModel         *string `json:"test_model"`
	ChannelInfo       struct {
		IsMultiKey         bool           `json:"is_multi_key"`
		MultiKeySize       int            `json:"multi_key_size"`
		MultiKeyStatusList map[string]int `json:"multi_key_status_list"`
		MultiKeyMode       string         `json:"multi_key_mode"`
	} `json:"channel_info"`
}

// Summary is the compact channel view used by lists.
type Summary struct {
	ID             int     `json:"id"`
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	Status         string  `json:"status"`
	DisabledReason string  `json:"disabled_reason,omitempty"`
	StatusChanged  string  `json:"status_changed_at,omitempty"`
	Group          string  `json:"group"`
	Tag            string  `json:"tag,omitempty"`
	Priority       int64   `json:"priority"`
	Weight         uint    `json:"weight"`
	BaseURL        string  `json:"base_url,omitempty"`
	ModelCount     int     `json:"model_count"`
	ResponseTimeMs int     `json:"last_test_response_ms"`
	LastTestAt     string  `json:"last_test_at,omitempty"`
	UsedUSD        float64 `json:"used_usd"`
	MultiKey       *Keys   `json:"multi_key,omitempty"`
}

// Keys summarises a multi-key channel without revealing any key.
type Keys struct {
	Size     int    `json:"size"`
	Disabled int    `json:"disabled"`
	Mode     string `json:"mode,omitempty"`
}

// Detail adds configuration to Summary.
type Detail struct {
	Summary
	Models            []string `json:"models"`
	TestModel         string   `json:"test_model,omitempty"`
	AutoBan           bool     `json:"auto_ban"`
	ModelMapping      string   `json:"model_mapping,omitempty"`
	StatusCodeMapping string   `json:"status_code_mapping,omitempty"`
	ParamOverride     string   `json:"param_override,omitempty"`
	HeaderOverrideKey []string `json:"header_override_names,omitempty" jsonschema:"header names only; values are withheld because they often hold credentials"`
	Setting           string   `json:"setting,omitempty"`
	Remark            string   `json:"remark,omitempty"`
	CreatedAt         string   `json:"created_at"`
	BalanceUSD        float64  `json:"balance_usd"`
}

var statusNames = map[int]string{1: "enabled", 2: "manually_disabled", 3: "auto_disabled"}

func statusName(status int) string {
	if name, ok := statusNames[status]; ok {
		return name
	}
	return "unknown"
}

func (c rawChannel) summary() Summary {
	s := Summary{
		ID:             c.ID,
		Name:           c.Name,
		Type:           typeName(c.Type),
		Status:         statusName(c.Status),
		Group:          c.Group,
		Tag:            deref(c.Tag),
		Priority:       deref(c.Priority),
		Weight:         deref(c.Weight),
		BaseURL:        redactURL(deref(c.BaseURL)),
		ModelCount:     len(splitModels(c.Models)),
		ResponseTimeMs: c.ResponseTime,
		LastTestAt:     shape.Time(c.TestTime),
		UsedUSD:        shape.USD(c.UsedQuota),
	}
	var info struct {
		StatusReason string `json:"status_reason"`
		StatusTime   int64  `json:"status_time"`
	}
	if json.Unmarshal([]byte(c.OtherInfo), &info) == nil && c.Status != 1 {
		s.DisabledReason = shape.Truncate(info.StatusReason, shape.TextLimit)
		s.StatusChanged = shape.Time(info.StatusTime)
	}
	if c.ChannelInfo.IsMultiKey {
		disabled := 0
		for _, status := range c.ChannelInfo.MultiKeyStatusList {
			if status != 1 {
				disabled++
			}
		}
		s.MultiKey = &Keys{Size: c.ChannelInfo.MultiKeySize, Disabled: disabled, Mode: c.ChannelInfo.MultiKeyMode}
	}
	return s
}

func (c rawChannel) detail() Detail {
	d := Detail{
		Summary:           c.summary(),
		Models:            splitModels(c.Models),
		TestModel:         deref(c.TestModel),
		AutoBan:           c.AutoBan == nil || *c.AutoBan != 0,
		ModelMapping:      shape.Truncate(deref(c.ModelMapping), 4000),
		StatusCodeMapping: deref(c.StatusCodeMapping),
		ParamOverride:     shape.Truncate(deref(c.ParamOverride), 4000),
		Setting:           redactSetting(deref(c.Setting)),
		Remark:            deref(c.Remark),
		CreatedAt:         shape.Time(c.CreatedTime),
		BalanceUSD:        c.Balance,
	}
	var headers map[string]any
	if json.Unmarshal([]byte(deref(c.HeaderOverride)), &headers) == nil {
		for name := range headers {
			d.HeaderOverrideKey = append(d.HeaderOverrideKey, name)
		}
		slices.Sort(d.HeaderOverrideKey)
	}
	return d
}

func splitModels(models string) []string {
	var out []string
	for m := range strings.SplitSeq(models, ",") {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}

// redactURL drops user info, which some base URLs and proxies carry.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("REDACTED")
	return u.String()
}

// redactSetting removes credentials from the proxy URL in a channel setting.
func redactSetting(raw string) string {
	var setting map[string]any
	if raw == "" || json.Unmarshal([]byte(raw), &setting) != nil {
		return shape.Truncate(raw, 2000)
	}
	if proxy, ok := setting["proxy"].(string); ok {
		setting["proxy"] = redactURL(proxy)
	}
	out, err := json.Marshal(setting)
	if err != nil {
		return ""
	}
	return shape.Truncate(string(out), 2000)
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// Channel type names from new-api constant.ChannelTypeNames.
var typeNames = map[int]string{
	0: "Unknown", 1: "OpenAI", 2: "Midjourney", 3: "Azure", 4: "Ollama", 5: "MidjourneyPlus", 6: "OpenAIMax", 7: "OhMyGPT",
	8: "Custom", 9: "AILS", 10: "AIProxy", 11: "PaLM", 12: "API2GPT", 13: "AIGC2D", 14: "Anthropic", 15: "Baidu", 16: "Zhipu",
	17: "Ali", 18: "Xunfei", 19: "360", 20: "OpenRouter", 21: "AIProxyLibrary", 22: "FastGPT", 23: "Tencent", 24: "Gemini",
	25: "Moonshot", 27: "Perplexity", 31: "LingYiWanWu", 33: "AWS", 34: "Cohere", 35: "MiniMax", 36: "SunoAPI", 37: "Dify",
	38: "Jina", 40: "SiliconFlow", 41: "VertexAI", 42: "Mistral", 43: "DeepSeek", 44: "MokaAI", 45: "VolcEngine",
	46: "BaiduV2", 47: "Xinference", 48: "xAI", 49: "Coze", 50: "Kling", 51: "Jimeng", 52: "Vidu", 53: "Submodel",
	54: "Doubao", 55: "Sora", 56: "Replicate", 57: "ChatGPT Subscription (Codex)", 58: "Advanced Custom", 59: "Sub2API",
	60: "New API", 61: "Task Plugin", 62: "vLLM", 63: "SGLang",
}

func typeName(t int) string {
	if name, ok := typeNames[t]; ok {
		return name
	}
	return "type " + strconv.Itoa(t)
}
