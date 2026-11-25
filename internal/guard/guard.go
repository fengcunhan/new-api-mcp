// Package guard runs configuration changes in two steps: a call without a
// confirm token returns the diff and a signed token, and the same call with
// the token applies the change if nothing moved in between. The service keeps
// no state; the token carries everything needed to check the second call.
package guard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fengcunhan/new-api-mcp/internal/newapi"
	"github.com/fengcunhan/new-api-mcp/internal/obs"
)

// Change is one field that a mutation alters.
type Change struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

// Mutation is a change expressed as load, pure plan, apply. Plan must not do
// I/O: the merge logic lives there and is tested without a network.
type Mutation[S any] interface {
	Load(ctx context.Context) (S, error)
	Plan(current S) (next S, changes []Change, err error)
	// Apply writes next. current is passed for writes that need the value
	// they replace, such as an expected version.
	Apply(ctx context.Context, current, next S) error
}

// Outcome is what a guarded tool returns to the caller.
type Outcome struct {
	Applied      bool     `json:"applied"`
	NoChange     bool     `json:"no_change,omitempty"`
	Changes      []Change `json:"changes"`
	ConfirmToken string   `json:"confirm_token,omitempty"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	Next         string   `json:"next"`
}

// Error is a guard rejection with a sentence the model can act on.
type Error struct {
	Reason string
	hint   string
}

func (e *Error) Error() string { return "confirm token rejected: " + e.Reason }
func (e *Error) Hint() string  { return e.hint }

var (
	errMalformed = &Error{"malformed", "The confirm_token is not valid. Run the tool without confirm_token to get a new preview."}
	errExpired   = &Error{"expired", "The confirm_token has expired. Run the tool without confirm_token to get a new preview."}
	errArgs      = &Error{"arguments differ", "The arguments differ from the previewed ones. Send exactly the previewed arguments, or preview again."}
	errCaller    = &Error{"different caller", "The confirm_token was issued to a different access token."}
	errTool      = &Error{"different tool", "The confirm_token was issued for a different tool."}
	errStale     = &Error{"state changed", "The target changed after the preview. Run the tool without confirm_token to review the new diff."}
)

// Hooks receives outcomes for metrics and logs; nil fields are skipped.
type Hooks struct {
	Outcome func(tool, outcome string)
}

// Guard signs and checks confirm tokens.
type Guard struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
	hooks  Hooks
}

func New(secret []byte, ttl time.Duration, now func() time.Time, hooks Hooks) *Guard {
	if now == nil {
		now = time.Now
	}
	return &Guard{secret: secret, ttl: ttl, now: now, hooks: hooks}
}

type payload struct {
	Tool     string `json:"t"`
	ArgsSHA  string `json:"a"`
	StateSHA string `json:"s"`
	Caller   string `json:"r"`
	Expires  int64  `json:"e"`
}

// Run executes a mutation in two steps. args are the tool arguments without
// the confirm token; they are hashed so the confirmation must repeat them.
func Run[S any](ctx context.Context, g *Guard, tool string, args any, confirmToken string, m Mutation[S]) (Outcome, error) {
	current, err := m.Load(ctx)
	if err != nil {
		return Outcome{}, err
	}
	next, changes, err := m.Plan(current)
	if err != nil {
		return Outcome{}, err
	}
	if len(changes) == 0 {
		g.report(tool, "no_change")
		return Outcome{NoChange: true, Changes: []Change{}, Next: "Nothing to change; the target already has these values."}, nil
	}
	argsSHA, err := digest(args)
	if err != nil {
		return Outcome{}, err
	}
	stateSHA, err := digest(current)
	if err != nil {
		return Outcome{}, err
	}
	caller := obs.TokenRef(newapi.CredentialFrom(ctx))

	if confirmToken == "" {
		expires := g.now().Add(g.ttl)
		token, err := g.sign(payload{Tool: tool, ArgsSHA: argsSHA, StateSHA: stateSHA, Caller: caller, Expires: expires.Unix()})
		if err != nil {
			return Outcome{}, err
		}
		g.report(tool, "previewed")
		return Outcome{
			Changes:      changes,
			ConfirmToken: token,
			ExpiresAt:    expires.UTC().Format(time.RFC3339),
			Next:         "Not applied. Show the changes to the user; if they approve, call this tool again with the same arguments plus confirm_token.",
		}, nil
	}

	p, err := g.verify(confirmToken)
	if err != nil {
		g.report(tool, err.(*Error).Reason)
		return Outcome{}, err
	}
	var reject *Error
	switch {
	case p.Tool != tool:
		reject = errTool
	case p.Caller != caller:
		reject = errCaller
	case p.ArgsSHA != argsSHA:
		reject = errArgs
	case p.StateSHA != stateSHA:
		reject = errStale
	}
	if reject != nil {
		g.report(tool, reject.Reason)
		return Outcome{}, reject
	}
	if err := m.Apply(ctx, current, next); err != nil {
		if newapi.IsKind(err, newapi.KindConflict) {
			g.report(tool, "conflict")
		}
		return Outcome{}, err
	}
	g.report(tool, "applied")
	return Outcome{Applied: true, Changes: changes, Next: "Applied."}, nil
}

func (g *Guard) report(tool, outcome string) {
	if g.hooks.Outcome != nil {
		g.hooks.Outcome(tool, outcome)
	}
}

func (g *Guard) sign(p payload) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(body) + "." + enc.EncodeToString(g.mac(body)), nil
}

func (g *Guard) verify(token string) (payload, error) {
	enc := base64.RawURLEncoding
	bodyPart, sigPart, ok := strings.Cut(token, ".")
	if !ok {
		return payload{}, errMalformed
	}
	body, err1 := enc.DecodeString(bodyPart)
	sig, err2 := enc.DecodeString(sigPart)
	if err1 != nil || err2 != nil || !hmac.Equal(sig, g.mac(body)) {
		return payload{}, errMalformed
	}
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return payload{}, errMalformed
	}
	if g.now().Unix() > p.Expires {
		return payload{}, errExpired
	}
	return p, nil
}

func (g *Guard) mac(body []byte) []byte {
	h := hmac.New(sha256.New, g.secret)
	h.Write(body)
	return h.Sum(nil)
}

// digest hashes a value through its JSON encoding, which sorts map keys and
// so is stable for equal values.
func digest(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("hashing state: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// IsRejection reports whether err is a guard rejection.
func IsRejection(err error) bool {
	var e *Error
	return errors.As(err, &e)
}
