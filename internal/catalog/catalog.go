// Package catalog lists every tool definition.
package catalog

import (
	"fmt"
	"slices"

	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
	"github.com/fengcunhan/new-api-mcp/internal/tools/channel"
	"github.com/fengcunhan/new-api-mcp/internal/tools/logs"
	"github.com/fengcunhan/new-api-mcp/internal/tools/ops"
	"github.com/fengcunhan/new-api-mcp/internal/tools/option"
	"github.com/fengcunhan/new-api-mcp/internal/tools/pricing"
	"github.com/fengcunhan/new-api-mcp/internal/tools/report"
	"github.com/fengcunhan/new-api-mcp/internal/tools/user"
)

// All returns every tool, in a stable order.
func All() []tools.Definition {
	return slices.Concat(
		ops.Definitions(),
		channel.Definitions(),
		logs.Definitions(),
		user.Definitions(),
		pricing.Definitions(),
		option.Definitions(),
		report.Definitions(),
	)
}

// Toolsets returns the toolset names in catalog order.
func Toolsets(defs []tools.Definition) []string {
	var out []string
	for _, d := range defs {
		if !slices.Contains(out, d.Spec.Toolset) {
			out = append(out, d.Spec.Toolset)
		}
	}
	return out
}

// Select keeps the tools allowed by level and toolsets. Unknown toolset
// names are an error so a typo in MCP_TOOLSETS does not silently hide tools.
func Select(defs []tools.Definition, maxLevel config.Level, toolsets []string) ([]tools.Definition, error) {
	known := Toolsets(defs)
	for _, name := range toolsets {
		if !slices.Contains(known, name) {
			return nil, fmt.Errorf("unknown toolset %q in MCP_TOOLSETS; known: %v", name, known)
		}
	}
	var out []tools.Definition
	for _, d := range defs {
		if d.Spec.Level > maxLevel {
			continue
		}
		if len(toolsets) > 0 && !slices.Contains(toolsets, d.Spec.Toolset) {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}
