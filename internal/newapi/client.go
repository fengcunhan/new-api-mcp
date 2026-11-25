// Package newapi is the only code that talks to new-api. It owns the URL
// paths, the response envelope, error classification, the outbound rate
// limit and timeouts.
package newapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/fengcunhan/new-api-mcp/internal/obs"
)

// Client calls new-api dashboard endpoints with the caller's access token.
type Client interface {
	// Do sends req and decodes the envelope's data field into out (if out is
	// not nil). The envelope is returned whenever a response body was parsed,
	// including alongside a KindBusiness error.
	Do(ctx context.Context, req Request, out any) (*Envelope, error)
}

// Request describes one call. Params fill ":name" segments of Route.Path.
type Request struct {
	Route  Route
	Params map[string]string
	Query  url.Values
	Body   any
}

// Envelope is new-api's {success, message, data} response. Raw keeps the
// whole body for endpoints that put fields beside data.
type Envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Code    string          `json:"code"`
	Data    json.RawMessage `json:"data"`
	Raw     json.RawMessage `json:"-"`
}

type credentialKey struct{}

// WithCredential returns a context carrying the caller's access token. The
// token lives only in the context of one tool call.
func WithCredential(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, credentialKey{}, token)
}

// CredentialFrom returns the access token stored by WithCredential.
func CredentialFrom(ctx context.Context) string {
	token, _ := ctx.Value(credentialKey{}).(string)
	return token
}

// Options configures HTTPClient.
type Options struct {
	BaseURL *url.URL
	RPS     float64
	Burst   int
	Timeout time.Duration // per request unless the context is shorter
	Metrics *obs.Metrics
	// RetryBackoff is the wait before the single retry; zero means 2s.
	RetryBackoff time.Duration
	Transport    http.RoundTripper // tests only
}

// HTTPClient is the production Client.
type HTTPClient struct {
	base    *url.URL
	http    *http.Client
	limiter *rate.Limiter
	timeout time.Duration
	metrics *obs.Metrics
	backoff time.Duration
}

const maxResponseBytes = 8 << 20

func New(opts Options) *HTTPClient {
	transport := opts.Transport
	if transport == nil {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 150 * time.Second, // channel tests can take two minutes
		}
	}
	backoff := opts.RetryBackoff
	if backoff == 0 {
		backoff = 2 * time.Second
	}
	return &HTTPClient{
		base:    opts.BaseURL,
		http:    &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		limiter: rate.NewLimiter(rate.Limit(opts.RPS), opts.Burst),
		timeout: opts.Timeout,
		metrics: opts.Metrics,
		backoff: backoff,
	}
}

func (c *HTTPClient) Do(ctx context.Context, req Request, out any) (*Envelope, error) {
	token := CredentialFrom(ctx)
	if token == "" && req.Route.Scope != "" {
		return nil, &Error{Kind: KindNoCredential, Route: req.Route, Message: "no access token in context"}
	}
	target, err := c.buildURL(req)
	if err != nil {
		return nil, &Error{Kind: KindInvalidRequest, Route: req.Route, Message: err.Error()}
	}
	var body []byte
	if req.Body != nil {
		if body, err = json.Marshal(req.Body); err != nil {
			return nil, &Error{Kind: KindInvalidRequest, Route: req.Route, Message: err.Error()}
		}
	}

	env, err := c.attempt(ctx, req.Route, target, token, body)
	if retryable(req.Route, err) {
		select {
		case <-ctx.Done():
			return nil, &Error{Kind: KindUpstream, Route: req.Route, Message: ctx.Err().Error()}
		case <-time.After(c.backoff):
		}
		env, err = c.attempt(ctx, req.Route, target, token, body)
	}
	if err != nil {
		return env, err
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return env, &Error{Kind: KindIncompatible, Route: req.Route, Message: "unexpected response shape: " + err.Error()}
		}
	}
	return env, nil
}

// retryable allows one retry after a rate limit, and after a transport or
// server failure only for reads, which are safe to repeat.
func retryable(route Route, err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Kind == KindRateLimited || (e.Kind == KindUpstream && route.Method == http.MethodGet && e.Status != 0)
}

func (c *HTTPClient) attempt(ctx context.Context, route Route, target string, token string, body []byte) (*Envelope, error) {
	waitStart := time.Now()
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, &Error{Kind: KindUpstream, Route: route, Message: "waiting for rate limiter: " + err.Error()}
	}
	if c.metrics != nil {
		c.metrics.RateLimitWait.Observe(time.Since(waitStart).Seconds())
	}

	if c.timeout > 0 {
		if _, has := ctx.Deadline(); !has {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.timeout)
			defer cancel()
		}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, route.Method, target, reader)
	if err != nil {
		return nil, &Error{Kind: KindInvalidRequest, Route: route, Message: err.Error()}
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "new-api-mcp")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		c.count(route, "error")
		return nil, &Error{Kind: KindUpstream, Route: route, Message: err.Error()}
	}
	defer resp.Body.Close()
	c.count(route, strconv.Itoa(resp.StatusCode))
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, &Error{Kind: KindUpstream, Status: resp.StatusCode, Route: route, Message: "reading response: " + err.Error()}
	}
	if len(raw) > maxResponseBytes {
		return nil, &Error{Kind: KindUpstream, Status: resp.StatusCode, Route: route, Message: "response exceeds 8 MB; narrow the query"}
	}

	env := &Envelope{}
	if err := json.Unmarshal(raw, env); err != nil {
		kind := classify(resp.StatusCode, "")
		if resp.StatusCode < 300 {
			kind = KindIncompatible
		}
		return nil, &Error{Kind: kind, Status: resp.StatusCode, Route: route, Message: fmt.Sprintf("HTTP %d with a non-JSON body: %s", resp.StatusCode, snippet(raw))}
	}
	env.Raw = raw
	if resp.StatusCode >= 300 {
		return env, &Error{Kind: classify(resp.StatusCode, env.Code), Status: resp.StatusCode, Code: env.Code, Route: route, Message: env.Message}
	}
	if !env.Success {
		return env, &Error{Kind: classify(resp.StatusCode, env.Code), Status: resp.StatusCode, Code: env.Code, Route: route, Message: env.Message}
	}
	return env, nil
}

func (c *HTTPClient) count(route Route, status string) {
	if c.metrics != nil {
		c.metrics.UpstreamRequests.WithLabelValues(route.String(), status).Inc()
	}
}

var paramPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// buildURL fills path parameters and appends the query to the fixed base. A
// parameter must be a plain token, so no input can change the host or climb
// out of the route's path.
func (c *HTTPClient) buildURL(req Request) (string, error) {
	segments := strings.Split(req.Route.Path, "/")
	for i, segment := range segments {
		name, ok := strings.CutPrefix(segment, ":")
		if !ok {
			continue
		}
		value, present := req.Params[name]
		if !present || !paramPattern.MatchString(value) || value == "." || value == ".." {
			return "", fmt.Errorf("path parameter %q is missing or invalid", name)
		}
		segments[i] = url.PathEscape(value)
	}
	u := *c.base
	u.Path = c.base.Path + strings.Join(segments, "/")
	u.RawQuery = req.Query.Encode()
	return u.String(), nil
}

func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
