// Package toolstest provides a fake new-api client for tool tests.
package toolstest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fengcunhan/new-api-mcp/internal/guard"
	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/tools"
)

// Call is one recorded request.
type Call struct {
	Route  newapi.Route
	Params map[string]string
	Query  string
	Body   json.RawMessage
}

// Responder answers a request; it returns the data value or an error.
type Responder func(req newapi.Request) (any, error)

// Fake routes requests to responders keyed by "METHOD /path" and records them.
type Fake struct {
	mu         sync.Mutex
	responders map[string]Responder
	Calls      []Call
}

func New() *Fake { return &Fake{responders: map[string]Responder{}} }

// On registers a responder for a route.
func (f *Fake) On(route newapi.Route, r Responder) *Fake {
	f.responders[route.String()] = r
	return f
}

// Data registers a fixed data value for a route.
func (f *Fake) Data(route newapi.Route, data any) *Fake {
	return f.On(route, func(newapi.Request) (any, error) { return data, nil })
}

func (f *Fake) Do(_ context.Context, req newapi.Request, out any) (*newapi.Envelope, error) {
	body, _ := json.Marshal(req.Body)
	f.mu.Lock()
	f.Calls = append(f.Calls, Call{Route: req.Route, Params: req.Params, Query: req.Query.Encode(), Body: body})
	r, ok := f.responders[req.Route.String()]
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("toolstest: no responder for %s", req.Route)
	}
	data, err := r(req)
	var env *newapi.Envelope
	if raw, ok := data.(RawEnvelope); ok {
		env = &newapi.Envelope{Raw: json.RawMessage(raw)}
		_ = json.Unmarshal(env.Raw, env)
		return env, err
	}
	if err != nil {
		return nil, err
	}
	encoded, mErr := json.Marshal(data)
	if mErr != nil {
		return nil, mErr
	}
	env = &newapi.Envelope{Success: true, Data: encoded}
	env.Raw, _ = json.Marshal(env)
	if out != nil {
		if err := json.Unmarshal(encoded, out); err != nil {
			return env, err
		}
	}
	return env, nil
}

// RawEnvelope makes a responder return this exact body as the envelope.
type RawEnvelope string

// CallsTo returns the recorded calls for a route.
func (f *Fake) CallsTo(route newapi.Route) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Call
	for _, c := range f.Calls {
		if c.Route == route {
			out = append(out, c)
		}
	}
	return out
}

// JSON parses a JSON literal for use as fixture data.
func JSON(s string) any {
	var v any
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &v); err != nil {
		panic(err)
	}
	return v
}

// Clock is fixed for deterministic tests.
var Clock = time.Unix(1_800_000_000, 0)

// Deps returns tool dependencies backed by the fake and a test guard.
func Deps(f *Fake) tools.Deps {
	return tools.Deps{
		API:   f,
		Guard: guard.New([]byte(strings.Repeat("t", 32)), 5*time.Minute, func() time.Time { return Clock }, guard.Hooks{}),
		Now:   func() time.Time { return Clock },
	}
}

// Ctx returns a context carrying a test access token.
func Ctx() context.Context {
	return newapi.WithCredential(context.Background(), "nap_test")
}

// Run calls a definition with JSON arguments.
func Run(def tools.Definition, deps tools.Deps, args string) (any, error) {
	return def.Call(Ctx(), deps, json.RawMessage(args))
}

// Find returns the named definition or panics.
func Find(defs []tools.Definition, name string) tools.Definition {
	for _, d := range defs {
		if d.Spec.Name == name {
			return d
		}
	}
	panic("toolstest: no tool " + name)
}
