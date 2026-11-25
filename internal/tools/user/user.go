// Package user holds the user tools. Actions that new-api guards
// with additional security verification (delete, disable, role changes,
// password and 2FA resets) are deliberately absent.
package user

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/shape"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
)

type rawUser struct {
	ID           int    `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	Role         int    `json:"role"`
	Status       int    `json:"status"`
	Email        string `json:"email"`
	Quota        int64  `json:"quota"`
	UsedQuota    int64  `json:"used_quota"`
	RequestCount int    `json:"request_count"`
	Group        string `json:"group"`
	Remark       string `json:"remark"`
	CreatedAt    int64  `json:"created_at"`
	LastLoginAt  int64  `json:"last_login_at"`
}

// User is the view returned by user tools.
type User struct {
	ID           int     `json:"id"`
	Username     string  `json:"username"`
	DisplayName  string  `json:"display_name,omitempty"`
	Role         string  `json:"role"`
	Status       string  `json:"status"`
	Email        string  `json:"email,omitempty"`
	Group        string  `json:"group"`
	BalanceUSD   float64 `json:"balance_usd"`
	Quota        int64   `json:"quota" jsonschema:"remaining quota in units (500000 per USD)"`
	UsedUSD      float64 `json:"used_usd"`
	RequestCount int     `json:"request_count"`
	Remark       string  `json:"remark,omitempty"`
	CreatedAt    string  `json:"created_at,omitempty"`
	LastLoginAt  string  `json:"last_login_at,omitempty"`
}

var roleNames = map[int]string{0: "guest", 1: "user", 5: "department_admin", 10: "admin", 100: "root"}

func (u rawUser) view() User {
	role, ok := roleNames[u.Role]
	if !ok {
		role = "role " + strconv.Itoa(u.Role)
	}
	status := "enabled"
	if u.Status != 1 {
		status = "disabled"
	}
	return User{
		ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: role, Status: status, Email: u.Email,
		Group: u.Group, BalanceUSD: shape.USD(u.Quota), Quota: u.Quota, UsedUSD: shape.USD(u.UsedQuota),
		RequestCount: u.RequestCount, Remark: u.Remark, CreatedAt: shape.Time(u.CreatedAt), LastLoginAt: shape.Time(u.LastLoginAt),
	}
}

type searchIn struct {
	shape.Page
	Keyword string `json:"keyword,omitempty" jsonschema:"match id, username, display name or email"`
	Group   string `json:"group,omitempty"`
}

type searchOut struct {
	Items []User         `json:"items"`
	Page  shape.PageInfo `json:"page"`
}

func search(ctx context.Context, d tools.Deps, in searchIn) (searchOut, error) {
	q := url.Values{"keyword": {in.Keyword}}
	in.Page.Query(q)
	if in.Group != "" {
		q.Set("group", in.Group)
	}
	raw, err := tools.Get[struct {
		Items []rawUser `json:"items"`
		Total int       `json:"total"`
	}](ctx, d.API, newapi.Request{Route: newapi.RouteUserSearch, Query: q})
	if err != nil {
		return searchOut{}, err
	}
	out := searchOut{Items: make([]User, 0, len(raw.Items)), Page: shape.NewPageInfo(in.Page, raw.Total)}
	for _, u := range raw.Items {
		out.Items = append(out.Items, u.view())
	}
	return out, nil
}

type idIn struct {
	ID int `json:"id" jsonschema:"user id"`
}

func get(ctx context.Context, d tools.Deps, in idIn) (User, error) {
	if err := tools.RequirePositive("id", in.ID); err != nil {
		return User{}, err
	}
	u, err := tools.Get[rawUser](ctx, d.API, newapi.Request{Route: newapi.RouteUserGet, Params: tools.ID(in.ID)})
	return u.view(), err
}

type addQuotaIn struct {
	ID           int     `json:"id" jsonschema:"user id"`
	AmountUSD    float64 `json:"amount_usd" jsonschema:"dollars to add; negative to subtract"`
	ConfirmToken string  `json:"confirm_token,omitempty" jsonschema:"token from the preview; omit to preview"`
}

type quotaMutation struct {
	api newapi.Client
	in  addQuotaIn
}

func (m quotaMutation) Load(ctx context.Context) (rawUser, error) {
	return tools.Get[rawUser](ctx, m.api, newapi.Request{Route: newapi.RouteUserGet, Params: tools.ID(m.in.ID)})
}

func (m quotaMutation) Plan(cur rawUser) (rawUser, []guard.Change, error) {
	delta := int64(m.in.AmountUSD * shape.QuotaPerUSD)
	next := cur
	next.Quota = cur.Quota + delta
	if next.Quota < 0 {
		return rawUser{}, nil, tools.Invalid("the balance would drop below zero (current %.4f USD)", shape.USD(cur.Quota))
	}
	return next, []guard.Change{{
		Field:  fmt.Sprintf("balance_usd of %s (id %d)", cur.Username, cur.ID),
		Before: shape.USD(cur.Quota), After: shape.USD(next.Quota),
	}}, nil
}

func (m quotaMutation) Apply(ctx context.Context, cur, next rawUser) error {
	delta := next.Quota - cur.Quota
	mode, value := "add", delta
	if delta < 0 {
		mode, value = "subtract", -delta
	}
	_, err := m.api.Do(ctx, newapi.Request{Route: newapi.RouteUserManage, Body: map[string]any{
		"id": m.in.ID, "action": "add_quota", "mode": mode, "value": value,
	}}, nil)
	return err
}

func addQuota(ctx context.Context, d tools.Deps, in addQuotaIn) (guard.Outcome, error) {
	if err := tools.RequirePositive("id", in.ID); err != nil {
		return guard.Outcome{}, err
	}
	if in.AmountUSD == 0 {
		return guard.Outcome{}, tools.Invalid("amount_usd must be non-zero")
	}
	token := in.ConfirmToken
	in.ConfirmToken = ""
	return guard.Run(ctx, d.Guard, "user_add_quota", in, token, quotaMutation{api: d.API, in: in})
}

type profileIn struct {
	ID           int     `json:"id" jsonschema:"user id"`
	DisplayName  *string `json:"display_name,omitempty"`
	Group        *string `json:"group,omitempty" jsonschema:"user group; changing it signs the user out of the console"`
	Remark       *string `json:"remark,omitempty"`
	ConfirmToken string  `json:"confirm_token,omitempty" jsonschema:"token from the preview; omit to preview"`
}

type profile struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Group       string `json:"group"`
	Remark      string `json:"remark"`
}

type profileMutation struct {
	api newapi.Client
	in  profileIn
}

func (m profileMutation) Load(ctx context.Context) (profile, error) {
	u, err := tools.Get[rawUser](ctx, m.api, newapi.Request{Route: newapi.RouteUserGet, Params: tools.ID(m.in.ID)})
	return profile{Username: u.Username, DisplayName: u.DisplayName, Group: u.Group, Remark: u.Remark}, err
}

func (m profileMutation) Plan(cur profile) (profile, []guard.Change, error) {
	next := cur
	var changes []guard.Change
	for _, f := range []struct {
		name string
		dst  *string
		src  *string
	}{{"display_name", &next.DisplayName, m.in.DisplayName}, {"group", &next.Group, m.in.Group}, {"remark", &next.Remark, m.in.Remark}} {
		if f.src == nil {
			continue
		}
		value := strings.TrimSpace(*f.src)
		if value != *f.dst {
			changes = append(changes, guard.Change{Field: f.name, Before: *f.dst, After: value})
			*f.dst = value
		}
	}
	if next.Group == "" {
		return profile{}, nil, tools.Invalid("group cannot be empty")
	}
	return next, changes, nil
}

// Apply sends all four fields because new-api's user update overwrites
// username, display name, group and remark together.
func (m profileMutation) Apply(ctx context.Context, _, next profile) error {
	_, err := m.api.Do(ctx, newapi.Request{Route: newapi.RouteUserUpdate, Body: map[string]any{
		"id": m.in.ID, "username": next.Username, "display_name": next.DisplayName, "group": next.Group, "remark": next.Remark,
	}}, nil)
	return err
}

func updateProfile(ctx context.Context, d tools.Deps, in profileIn) (guard.Outcome, error) {
	if err := tools.RequirePositive("id", in.ID); err != nil {
		return guard.Outcome{}, err
	}
	token := in.ConfirmToken
	in.ConfirmToken = ""
	return guard.Run(ctx, d.Guard, "user_update_profile", in, token, profileMutation{api: d.API, in: in})
}

// Definitions returns every user tool.
func Definitions() []tools.Definition {
	return []tools.Definition{
		tools.Define(tools.Spec{
			Name: "user_search", Toolset: "user", Level: config.LevelRead, Title: "Search users",
			Description: "Search users by id, username, display name or email; shows role, status, group, balance and usage.",
			Routes:      []newapi.Route{newapi.RouteUserSearch},
		}, search),
		tools.Define(tools.Spec{
			Name: "user_get", Toolset: "user", Level: config.LevelRead, Title: "Get a user",
			Description: "Get one user's role, status, group, balance and usage.",
			Routes:      []newapi.Route{newapi.RouteUserGet},
		}, get),
		tools.Define(tools.Spec{
			Name: "user_add_quota", Toolset: "user", Level: config.LevelWrite, Title: "Adjust a user's balance",
			Description: "Add or subtract balance in USD. Two steps: without confirm_token it previews the new balance; " +
				"call again with the same arguments plus confirm_token to apply.",
			Routes: []newapi.Route{newapi.RouteUserGet, newapi.RouteUserManage},
		}, addQuota),
		tools.Define(tools.Spec{
			Name: "user_update_profile", Toolset: "user", Level: config.LevelWrite, Title: "Change a user's group or remark",
			Description: "Change a user's display name, group or remark. Changing the group signs the user out of the console. " +
				"Two steps: preview without confirm_token, then repeat with confirm_token to apply.",
			Routes: []newapi.Route{newapi.RouteUserGet, newapi.RouteUserUpdate},
		}, updateProfile),
	}
}
