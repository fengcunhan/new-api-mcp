package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/config"
)

type echoIn struct {
	ID    int    `json:"id" jsonschema:"required id"`
	Label string `json:"label,omitempty"`
}

type echoOut struct {
	ID int `json:"id"`
}

func TestDefineValidatesArguments(t *testing.T) {
	def := Define(Spec{Name: "echo", Level: config.LevelRead}, func(_ context.Context, _ Deps, in echoIn) (echoOut, error) {
		return echoOut{ID: in.ID}, nil
	})
	assert.Equal(t, "read", def.Spec.LevelName)
	assert.Equal(t, []string{"id"}, def.InputSchema.Required)

	out, err := def.Call(context.Background(), Deps{}, json.RawMessage(`{"id":7}`))
	require.NoError(t, err)
	assert.Equal(t, echoOut{ID: 7}, out)

	for _, bad := range []string{`{}`, `{"id":"7"}`, `{"id":7,"extra":1}`, `[1]`} {
		_, err := def.Call(context.Background(), Deps{}, json.RawMessage(bad))
		var inputErr *InputError
		assert.ErrorAs(t, err, &inputErr, bad)
	}
}
