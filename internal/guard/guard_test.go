package guard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/newapi"
)

// fakeMutation sets a value in an in-memory store.
type fakeMutation struct {
	store   *map[string]int
	key     string
	value   int
	applied int
	applyFn func() error
}

func (m *fakeMutation) Load(context.Context) (map[string]int, error) {
	return map[string]int{m.key: (*m.store)[m.key]}, nil
}

func (m *fakeMutation) Plan(cur map[string]int) (map[string]int, []Change, error) {
	if cur[m.key] == m.value {
		return cur, nil, nil
	}
	return map[string]int{m.key: m.value}, []Change{{Field: m.key, Before: cur[m.key], After: m.value}}, nil
}

func (m *fakeMutation) Apply(_ context.Context, _, next map[string]int) error {
	if m.applyFn != nil {
		if err := m.applyFn(); err != nil {
			return err
		}
	}
	m.applied++
	(*m.store)[m.key] = next[m.key]
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup() (*Guard, *clock, context.Context) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	g := New([]byte(strings.Repeat("k", 32)), 5*time.Minute, c.now, Hooks{})
	return g, c, newapi.WithCredential(context.Background(), "nap_alice")
}

type args struct {
	Key   string `json:"key"`
	Value int    `json:"value"`
}

func TestPreviewThenApply(t *testing.T) {
	g, _, ctx := setup()
	store := map[string]int{"a": 1}
	m := &fakeMutation{store: &store, key: "a", value: 2}

	preview, err := Run(ctx, g, "tool", args{"a", 2}, "", m)
	require.NoError(t, err)
	assert.False(t, preview.Applied)
	assert.NotEmpty(t, preview.ConfirmToken)
	assert.Equal(t, []Change{{Field: "a", Before: 1, After: 2}}, preview.Changes)
	assert.Equal(t, 1, store["a"], "preview must not write")

	done, err := Run(ctx, g, "tool", args{"a", 2}, preview.ConfirmToken, m)
	require.NoError(t, err)
	assert.True(t, done.Applied)
	assert.Equal(t, 2, store["a"])
}

func TestNoChangeIssuesNoToken(t *testing.T) {
	g, _, ctx := setup()
	store := map[string]int{"a": 2}
	out, err := Run(ctx, g, "tool", args{"a", 2}, "", &fakeMutation{store: &store, key: "a", value: 2})
	require.NoError(t, err)
	assert.True(t, out.NoChange)
	assert.Empty(t, out.ConfirmToken)
}

func TestConfirmRejections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(token string, c *clock, store map[string]int) (ctx context.Context, tool string, a args, tok string)
		want   *Error
	}{
		{"expired", func(tok string, c *clock, _ map[string]int) (context.Context, string, args, string) {
			c.t = c.t.Add(6 * time.Minute)
			return newapi.WithCredential(context.Background(), "nap_alice"), "tool", args{"a", 2}, tok
		}, errExpired},
		{"tampered", func(tok string, _ *clock, _ map[string]int) (context.Context, string, args, string) {
			return newapi.WithCredential(context.Background(), "nap_alice"), "tool", args{"a", 2}, "x" + tok
		}, errMalformed},
		{"garbage", func(string, *clock, map[string]int) (context.Context, string, args, string) {
			return newapi.WithCredential(context.Background(), "nap_alice"), "tool", args{"a", 2}, "abc"
		}, errMalformed},
		{"other caller", func(tok string, _ *clock, _ map[string]int) (context.Context, string, args, string) {
			return newapi.WithCredential(context.Background(), "nap_bob"), "tool", args{"a", 2}, tok
		}, errCaller},
		{"other tool", func(tok string, _ *clock, _ map[string]int) (context.Context, string, args, string) {
			return newapi.WithCredential(context.Background(), "nap_alice"), "other", args{"a", 2}, tok
		}, errTool},
		{"other args", func(tok string, _ *clock, _ map[string]int) (context.Context, string, args, string) {
			return newapi.WithCredential(context.Background(), "nap_alice"), "tool", args{"a", 3}, tok
		}, errArgs},
		{"state moved", func(tok string, _ *clock, store map[string]int) (context.Context, string, args, string) {
			store["a"] = 5
			return newapi.WithCredential(context.Background(), "nap_alice"), "tool", args{"a", 2}, tok
		}, errStale},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, c, ctx := setup()
			store := map[string]int{"a": 1}
			preview, err := Run(ctx, g, "tool", args{"a", 2}, "", &fakeMutation{store: &store, key: "a", value: 2})
			require.NoError(t, err)

			ctx2, tool, a, tok := tt.mutate(preview.ConfirmToken, c, store)
			m := &fakeMutation{store: &store, key: a.Key, value: a.Value}
			_, err = Run(ctx2, g, tool, a, tok, m)
			require.ErrorIs(t, err, tt.want)
			assert.Zero(t, m.applied)
		})
	}
}

func TestApplyErrorPropagates(t *testing.T) {
	g, _, ctx := setup()
	store := map[string]int{"a": 1}
	conflict := &newapi.Error{Kind: newapi.KindConflict, Message: "changed"}
	m := &fakeMutation{store: &store, key: "a", value: 2, applyFn: func() error { return conflict }}
	preview, err := Run(ctx, g, "tool", args{"a", 2}, "", m)
	require.NoError(t, err)
	_, err = Run(ctx, g, "tool", args{"a", 2}, preview.ConfirmToken, m)
	assert.True(t, errors.Is(err, conflict))
}
