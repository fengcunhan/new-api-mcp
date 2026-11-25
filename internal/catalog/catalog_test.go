package catalog

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fengcunhan/new-api-mcp/internal/config"
)

// TestCatalogIsConsistent protects invariants every tool must hold: a unique
// valid name, a description, declared routes, and an inferable object schema.
func TestCatalogIsConsistent(t *testing.T) {
	names := map[string]bool{}
	valid := regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
	for _, d := range All() {
		spec := d.Spec
		assert.True(t, valid.MatchString(spec.Name), spec.Name)
		assert.False(t, names[spec.Name], "duplicate tool %s", spec.Name)
		names[spec.Name] = true
		assert.NotEmpty(t, spec.Description, spec.Name)
		assert.NotEmpty(t, spec.Routes, spec.Name)
		assert.Equal(t, "object", d.InputSchema.Type, spec.Name)
		assert.Equal(t, "object", d.OutputSchema.Type, spec.Name)
		if spec.Level == config.LevelWrite {
			_, hasConfirm := d.InputSchema.Properties["confirm_token"]
			assert.True(t, hasConfirm, "write tool %s must take confirm_token", spec.Name)
		}
	}
}

func TestSelect(t *testing.T) {
	defs := All()
	read, err := Select(defs, config.LevelRead, nil)
	require.NoError(t, err)
	for _, d := range read {
		assert.Equal(t, config.LevelRead, d.Spec.Level)
	}
	all, err := Select(defs, config.LevelWrite, nil)
	require.NoError(t, err)
	assert.Len(t, all, len(defs))
	assert.Less(t, len(read), len(all))

	channelOnly, err := Select(defs, config.LevelWrite, []string{"channel"})
	require.NoError(t, err)
	for _, d := range channelOnly {
		assert.Equal(t, "channel", d.Spec.Toolset)
	}

	_, err = Select(defs, config.LevelRead, []string{"channels"})
	assert.ErrorContains(t, err, "unknown toolset")
}
