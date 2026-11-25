// Package tools defines the tool framework: a declarative Spec plus a typed
// handler. It does not import the MCP SDK or net/http; the server package
// adapts Definitions to MCP.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/fengcunhan/new-api-mcp/internal/config"
	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
)

// Spec declares a tool. It is data: the server filters on it, the manifest
// exports it and the contract check compares Routes with new-api.
type Spec struct {
	Name        string         `json:"name"`
	Toolset     string         `json:"toolset"`
	Level       config.Level   `json:"-"`
	LevelName   string         `json:"level"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Routes      []newapi.Route `json:"routes"`
	Idempotent  bool           `json:"idempotent"`
	// Timeout overrides the default per-call timeout; channel tests need more.
	Timeout time.Duration `json:"-"`
}

// Deps are what handlers may use.
type Deps struct {
	API   newapi.Client
	Guard *guard.Guard // nil unless write tools are enabled
	Now   func() time.Time
}

// Handler is the signature every tool implements.
type Handler[In, Out any] func(ctx context.Context, deps Deps, in In) (Out, error)

// Definition is a tool with its types erased, ready for the server.
type Definition struct {
	Spec         Spec
	InputSchema  *jsonschema.Schema
	OutputSchema *jsonschema.Schema
	// Call validates and decodes raw arguments, then runs the handler.
	Call func(ctx context.Context, deps Deps, args json.RawMessage) (any, error)
}

// InputError is an argument problem the model can fix.
type InputError struct{ Message string }

func (e *InputError) Error() string { return e.Message }
func (e *InputError) Hint() string  { return "Invalid arguments: " + e.Message }

// Invalid returns an InputError with a formatted message.
func Invalid(format string, args ...any) error {
	return &InputError{Message: fmt.Sprintf(format, args...)}
}

// schemaOptions describes json.RawMessage, used for pass-through report
// data, as any JSON value instead of a byte array.
var schemaOptions = &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[json.RawMessage](): {},
}}

// Define builds a Definition from a typed handler. Input and output schemas
// are inferred from In and Out; jsonschema struct tags become descriptions,
// and fields without omitempty are required.
func Define[In, Out any](spec Spec, h Handler[In, Out]) Definition {
	spec.LevelName = spec.Level.String()
	in, err := jsonschema.For[In](schemaOptions)
	if err != nil {
		panic(fmt.Sprintf("tool %s: input schema: %v", spec.Name, err))
	}
	out, err := jsonschema.For[Out](schemaOptions)
	if err != nil {
		panic(fmt.Sprintf("tool %s: output schema: %v", spec.Name, err))
	}
	resolved, err := in.Resolve(nil)
	if err != nil {
		panic(fmt.Sprintf("tool %s: resolve input schema: %v", spec.Name, err))
	}
	return Definition{
		Spec:         spec,
		InputSchema:  in,
		OutputSchema: out,
		Call: func(ctx context.Context, deps Deps, args json.RawMessage) (any, error) {
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			var generic map[string]any
			if err := json.Unmarshal(args, &generic); err != nil {
				return nil, Invalid("arguments must be a JSON object: %v", err)
			}
			if err := resolved.Validate(generic); err != nil {
				return nil, Invalid("%v", err)
			}
			var input In
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, Invalid("%v", err)
			}
			return h(ctx, deps, input)
		},
	}
}

// Get calls a read endpoint and decodes data into a new T.
func Get[T any](ctx context.Context, api newapi.Client, req newapi.Request) (T, error) {
	var out T
	_, err := api.Do(ctx, req, &out)
	return out, err
}

// ID formats an integer path parameter.
func ID(id int) map[string]string {
	return map[string]string{"id": fmt.Sprint(id)}
}

// RequirePositive checks an id argument.
func RequirePositive(name string, v int) error {
	if v <= 0 {
		return Invalid("%s must be a positive integer", name)
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

// Ptr returns a pointer to v, for optional fields.
func Ptr[T any](v T) *T { return ptr(v) }
