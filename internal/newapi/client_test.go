package newapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *HTTPClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL + "/prefix")
	require.NoError(t, err)
	return New(Options{BaseURL: base, RPS: 1000, Burst: 1000, Timeout: 5 * time.Second, RetryBackoff: time.Millisecond})
}

func TestDoSendsTokenAndDecodesData(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer nap_abc", r.Header.Get("Authorization"))
		assert.Equal(t, "/prefix/api/channel/42", r.URL.Path)
		assert.Equal(t, "1", r.URL.Query().Get("p"))
		_, _ = w.Write([]byte(`{"success":true,"message":"","data":{"id":42}}`))
	})
	var out struct {
		ID int `json:"id"`
	}
	ctx := WithCredential(context.Background(), "nap_abc")
	_, err := client.Do(ctx, Request{Route: RouteChannelGet, Params: map[string]string{"id": "42"}, Query: url.Values{"p": {"1"}}}, &out)
	require.NoError(t, err)
	assert.Equal(t, 42, out.ID)
}

func TestDoWithoutCredentialSendsNothing(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	_, err := client.Do(context.Background(), Request{Route: RouteChannelList}, nil)
	assert.True(t, IsKind(err, KindNoCredential))
	assert.Zero(t, calls.Load())
}

func TestDoRejectsUnsafePathParams(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("must not send") })
	ctx := WithCredential(context.Background(), "nap_abc")
	for _, value := range []string{"", "..", "../option", "1/2", "a?b", "http://evil"} {
		_, err := client.Do(ctx, Request{Route: RouteChannelGet, Params: map[string]string{"id": value}}, nil)
		assert.True(t, IsKind(err, KindInvalidRequest), "value %q", value)
	}
}

func TestDoClassifiesFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   Kind
	}{
		{"scope denied", 403, `{"success":false,"code":"ACCESS_TOKEN_SCOPE_DENIED","message":"x"}`, KindScopeDenied},
		{"undeclared route", 403, `{"success":false,"code":"ACCESS_TOKEN_ROUTE_UNDECLARED","message":"x"}`, KindIncompatible},
		{"session only", 403, `{"success":false,"code":"AUTH_SESSION_REQUIRED","message":"x"}`, KindSessionOnly},
		{"proof required", 403, `{"success":false,"code":"SECURITY_PROOF_REQUIRED","message":"x"}`, KindProofRequired},
		{"plain 403", 403, `{"success":false,"message":"x"}`, KindRoleDenied},
		{"expired", 401, `{"success":false,"message":"x"}`, KindUnauthenticated},
		{"conflict", 409, `{"success":false,"message":"changed"}`, KindConflict},
		{"bad request", 400, `{"success":false,"message":"bad"}`, KindInvalidRequest},
		{"business failure on 200", 200, `{"success":false,"message":"channel not found"}`, KindBusiness},
		{"not json", 502, `<html>bad gateway</html>`, KindUpstream},
		{"success but not json", 200, `<html>login</html>`, KindIncompatible},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := client.Do(WithCredential(context.Background(), "nap_abc"), Request{Route: RouteChannelStatus, Params: map[string]string{"id": "1"}}, nil)
			var e *Error
			require.ErrorAs(t, err, &e)
			assert.Equal(t, tt.want, e.Kind)
			assert.NotEmpty(t, e.Hint())
		})
	}
}

func TestDoReturnsEnvelopeWithBusinessError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"upstream 401","time":1.5}`))
	})
	env, err := client.Do(WithCredential(context.Background(), "nap_abc"), Request{Route: RouteChannelTest, Params: map[string]string{"id": "1"}}, nil)
	assert.True(t, IsKind(err, KindBusiness))
	require.NotNil(t, env)
	assert.Contains(t, string(env.Raw), `"time":1.5`)
}

func TestDoRetries(t *testing.T) {
	tests := []struct {
		name      string
		route     Route
		status    int
		wantCalls int32
	}{
		{"read retried after 5xx", RouteChannelList, 503, 2},
		{"write not retried after 5xx", RouteChannelStatus, 503, 1},
		{"write retried after 429", RouteChannelStatus, 429, 2},
		{"auth failure not retried", RouteChannelList, 401, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"success":false,"message":"x"}`))
			})
			_, err := client.Do(WithCredential(context.Background(), "nap_abc"), Request{Route: tt.route, Params: map[string]string{"id": "1"}}, nil)
			require.Error(t, err)
			assert.Equal(t, tt.wantCalls, calls.Load())
		})
	}
}

func TestRateLimiterWaitHonoursContext(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	client.limiter.SetLimit(0.001)
	client.limiter.SetBurst(1)
	ctx := WithCredential(context.Background(), "nap_abc")
	_, err := client.Do(ctx, Request{Route: RouteChannelList}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = client.Do(ctx, Request{Route: RouteChannelList}, nil)
	assert.True(t, IsKind(err, KindUpstream))
	assert.Less(t, time.Since(start), time.Second)
}
