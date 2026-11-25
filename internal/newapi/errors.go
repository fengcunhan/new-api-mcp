package newapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Kind classifies a failed call so callers decide with errors.As instead of
// parsing messages.
type Kind string

const (
	KindNoCredential    Kind = "no_credential"
	KindUnauthenticated Kind = "unauthenticated"
	KindScopeDenied     Kind = "scope_denied"
	KindRoleDenied      Kind = "role_denied"
	KindProofRequired   Kind = "proof_required"
	KindSessionOnly     Kind = "session_only"
	KindIncompatible    Kind = "incompatible"
	KindRateLimited     Kind = "rate_limited"
	KindConflict        Kind = "conflict"
	KindBusiness        Kind = "business"
	KindInvalidRequest  Kind = "invalid_request"
	KindUpstream        Kind = "upstream"
)

// Error is every failure returned by Client.
type Error struct {
	Kind    Kind
	Status  int    // HTTP status, 0 when no response arrived
	Code    string // new-api's stable error code, if any
	Message string // new-api's message or the transport error
	Route   Route
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s", e.Route, e.Kind, e.Message)
	if e.Code != "" {
		fmt.Fprintf(&b, " (%s)", e.Code)
	}
	return b.String()
}

// Hint is the actionable sentence shown to the model for this failure.
func (e *Error) Hint() string {
	switch e.Kind {
	case KindNoCredential:
		return "No new-api access token was sent. Configure the MCP client with an Authorization: Bearer nap_... header."
	case KindUnauthenticated:
		return "The new-api access token is invalid, expired or revoked. Create a new one in the new-api console under personal settings > access tokens."
	case KindScopeDenied:
		return fmt.Sprintf("The access token lacks scope %q needed by %s. Create a token that includes it.", e.Route.Scope, e.Route)
	case KindRoleDenied:
		return "The account behind this access token does not have the role this action needs."
	case KindProofRequired:
		return "new-api requires additional security verification for this action. Do it by hand in the new-api console."
	case KindSessionOnly:
		return "new-api accepts this action only from a browser sign-in, not from an access token. Do it by hand in the console."
	case KindIncompatible:
		return fmt.Sprintf("new-api does not serve %s for access tokens. The new-api version and this MCP server are out of step; run server_status and report the versions.", e.Route)
	case KindRateLimited:
		return "new-api rate limited the request. Wait a minute and retry with fewer calls."
	case KindConflict:
		return "The object changed after it was read. Run the tool again without confirm_token to get a fresh preview."
	case KindInvalidRequest:
		return "new-api rejected the request parameters: " + e.Message
	case KindUpstream:
		return "new-api is unreachable or failed: " + e.Message
	}
	return e.Message
}

// IsKind reports whether err is an *Error of kind k.
func IsKind(err error, k Kind) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == k
}

// classify maps an HTTP status and new-api envelope to a Kind. It is called
// only for failed responses.
func classify(status int, code string) Kind {
	switch code {
	case "ACCESS_TOKEN_SCOPE_DENIED":
		return KindScopeDenied
	case "ACCESS_TOKEN_ROUTE_UNDECLARED":
		return KindIncompatible
	case "AUTH_SESSION_REQUIRED":
		return KindSessionOnly
	case "ACCESS_TOKEN_EXPIRED", "ACCESS_TOKEN_LEGACY_RETIRED":
		return KindUnauthenticated
	}
	if strings.HasPrefix(code, "SECURITY_PROOF_") {
		return KindProofRequired
	}
	switch {
	case status == http.StatusUnauthorized:
		return KindUnauthenticated
	case status == http.StatusForbidden:
		return KindRoleDenied
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed:
		return KindIncompatible
	case status == http.StatusConflict:
		return KindConflict
	case status == http.StatusTooManyRequests:
		return KindRateLimited
	case status == http.StatusBadRequest || status == http.StatusRequestEntityTooLarge:
		return KindInvalidRequest
	case status >= 500:
		return KindUpstream
	}
	return KindBusiness
}
