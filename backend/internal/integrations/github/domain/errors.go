package domain

import "errors"

// This mirrors chat/domain/errors.go and finance/domain/errors.go by
// design. Each context owns its own error vocabulary — sharing one would
// create exactly the cross-context import the architecture forbids, and
// would couple contexts that must be able to evolve separately. Thirty
// duplicated lines is the cheaper half of that trade.

type ErrorKind string

const (
	KindInvalid   ErrorKind = "invalid"
	KindNotFound  ErrorKind = "not_found"
	KindConflict  ErrorKind = "conflict"
	KindUpstream  ErrorKind = "upstream"
	KindNotConfig ErrorKind = "not_configured"
	// KindUnauthorized is GitHub refusing our credential — a revoked token,
	// an expired one, a token that never had the permission. Its own kind
	// because the fix is a human re-authorizing, not a retry and not a
	// smaller request.
	KindUnauthorized ErrorKind = "unauthorized"
	// KindRateLimited is GitHub refusing because we asked too often. Its own
	// kind because it is the one upstream failure that clears by waiting,
	// and telling it apart from a hard refusal is what lets both the model
	// and the operator do the right thing.
	KindRateLimited ErrorKind = "rate_limited"
)

type Error struct {
	Kind    ErrorKind
	Message string
	// Code overrides the wire code the HTTP layer would derive from Kind,
	// so one kind can carry several machine-readable reasons.
	Code string
}

func (e *Error) Error() string { return e.Message }

func Invalid(msg string) *Error   { return &Error{Kind: KindInvalid, Message: msg} }
func NotFound(what string) *Error { return &Error{Kind: KindNotFound, Message: what + " not found"} }
func Conflict(msg string) *Error  { return &Error{Kind: KindConflict, Message: msg} }

// Upstream wraps a failure that came from GitHub rather than from us. It
// maps to 502 at the HTTP boundary so a GitHub outage is never reported as
// a bug in this service.
func Upstream(msg string) *Error { return &Error{Kind: KindUpstream, Message: msg} }

// NotConfigured means the operation needs deployment config that is absent
// — currently only SECRETS_KEY. Maps to 503: retrying is pointless until an
// operator acts.
func NotConfigured(msg string) *Error { return &Error{Kind: KindNotConfig, Message: msg} }

// Unauthorized is GitHub rejecting the stored credential.
//
// The message must be actionable and must never quote what was sent. See
// the note on Sanitize.
func Unauthorized(msg string) *Error {
	return &Error{Kind: KindUnauthorized, Message: msg, Code: CodeGitHubUnauthorized}
}

// RateLimited is GitHub refusing because a limit was reached.
func RateLimited(msg string) *Error {
	return &Error{Kind: KindRateLimited, Message: msg, Code: CodeGitHubRateLimited}
}

// The machine-readable reasons a client and the model both branch on.
// Exported because "the token stopped working" and "we asked too fast" lead
// to two completely different sentences on screen, and neither is
// reconstructible from prose.
const (
	CodeGitHubUnauthorized = "github_unauthorized"
	CodeGitHubRateLimited  = "github_rate_limited"
	// CodeGitHubNotConnected is the absence of a connection, reported as a
	// conflict: the request is well formed and the caller is allowed, the
	// workspace is simply in a state that has no GitHub in it.
	CodeGitHubNotConnected = "github_not_connected"
	// CodeRepositoryNotAuthorized is the refusal that matters most in this
	// whole integration: a repository the model named that the operator did
	// not authorize. Distinct from not_found on purpose — see
	// app.ResolveRepository.
	CodeRepositoryNotAuthorized = "github_repository_not_authorized"
)

// NotConnected refuses an operation because this workspace has no live
// GitHub connection.
func NotConnected() *Error {
	return &Error{
		Kind:    KindConflict,
		Code:    CodeGitHubNotConnected,
		Message: "this workspace has no GitHub connection",
	}
}

// RepositoryNotAuthorized refuses a repository that is not in the
// authorized set.
//
// `named` is echoed back so a person reading a transcript can see which
// string was refused, and it is truncated by the caller before it gets
// here. It is never used to build a request.
func RepositoryNotAuthorized(named string) *Error {
	return &Error{
		Kind: KindConflict,
		Code: CodeRepositoryNotAuthorized,
		Message: "the repository " + named + " is not authorized for use in C.O.R.S.I.; " +
			"only repositories the operator selected can be read",
	}
}

// IsKind reports whether err is a domain error of the given kind. Use it at
// the HTTP boundary to map domain errors to status codes.
func IsKind(err error, k ErrorKind) bool {
	var de *Error
	if errors.As(err, &de) {
		return de.Kind == k
	}
	return false
}
