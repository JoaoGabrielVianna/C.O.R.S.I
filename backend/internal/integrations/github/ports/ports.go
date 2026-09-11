// Package ports declares the GitHub integration's boundaries: the
// normalized contract it produces, and the storage it requires.
//
// ── Why the contract lives here and not in the client ──────────────────
// Because the contract belongs to the boundary, not to the adapter that
// happens to satisfy it. The types below carry no GitHub dialect that the
// consumer would otherwise have to learn: no `sha` nested three levels
// deep, no `_links`, no `node_id`. Swapping the adapter (a GraphQL client,
// a cached one, a GitHub Enterprise one) changes nothing above this line.
//
// ── What is deliberately absent ────────────────────────────────────────
// Every write. There is no CreateCommit, no OpenPullRequest, no
// UpdateFile — not commented out, not behind a flag, not in a struct with
// an unused field. A write capability that exists in the type system is one
// a later change can reach; the honest way to ship read-only is for the
// verbs not to exist.
package ports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/github/domain"
)

/* ── storage ─────────────────────────────────────────────────────────── */

// ConnectionRepo stores the linked account. Every method takes a workspace
// id and filters on it server-side; soft-deleted rows are never returned.
type ConnectionRepo interface {
	// Upsert writes the workspace's single connection, replacing whatever
	// was there. Connecting a second account is replacing the first, which
	// is the only reading the one-per-workspace index permits and the only
	// one a user means.
	Upsert(ctx context.Context, c *domain.Connection) error
	// FindByWorkspace returns the live connection. domain.NotConnected when
	// there is none — a typed refusal rather than a nil, because "no GitHub
	// here" is an answer the whole stack branches on.
	FindByWorkspace(ctx context.Context, workspaceID uuid.UUID) (*domain.Connection, error)
	// TouchVerified stamps last_verified_at. Called after a round trip that
	// proved the credential still works.
	TouchVerified(ctx context.Context, workspaceID, id uuid.UUID, at time.Time) error
	// Disconnect soft-deletes the connection. The authorized repositories go
	// with it via ON DELETE CASCADE — an authorization to read through a
	// credential that no longer exists is not a decision worth preserving.
	Disconnect(ctx context.Context, workspaceID uuid.UUID) error
}

// RepositoryRepo stores which repositories the operator authorized.
type RepositoryRepo interface {
	// ReplaceAll sets the authorized set to exactly `repos`. One statement
	// rather than a diff the caller computes, because the UI edits a set and
	// a set is what it should be able to send: an add and a revoke arriving
	// as two requests is a window in which the two disagree.
	ReplaceAll(ctx context.Context, workspaceID, connectionID uuid.UUID, repos []domain.Repository) error
	// ListByConnection returns the authorized set in a stable order.
	ListByConnection(ctx context.Context, workspaceID, connectionID uuid.UUID) ([]domain.Repository, error)
	// FindByFullName resolves a caller-supplied "owner/name" to a stored
	// row, case-insensitively. This is THE authorization check for every
	// tool: not found means not authorized, and the two are the same answer
	// on purpose — a caller who may not read a repository must not learn
	// whether it exists.
	FindByFullName(ctx context.Context, workspaceID, connectionID uuid.UUID, fullName string) (*domain.Repository, error)
	// CountByConnection is how many repositories are authorized.
	CountByConnection(ctx context.Context, workspaceID, connectionID uuid.UUID) (int64, error)
}

/* ── the normalized contract ─────────────────────────────────────────── */

// Credentials are the resolved, decrypted access details for one call.
//
// Passed per-request rather than held on the client, so a single client
// instance serves every workspace without a cache keyed by something a
// future refactor could get wrong. The same choice the LLM adapter made,
// for the same reason.
type Credentials struct {
	BaseURL string
	Token   string
}

// Account is who a credential turned out to be.
type Account struct {
	Login     string `json:"login"`
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// RemoteRepository is one repository as GitHub reports it — what the
// credential can reach, before the operator has decided anything about it.
//
// ── Why the last four fields are here and not in the stored row ────────
// Language, Size, Archived and Fork are what let a caller decide whether it
// already knows enough about a repository or has to open it. They are
// deliberately NOT persisted alongside the authorization: the authorized
// set records a DECISION, which does not change when somebody pushes, and a
// stored `pushed_at` would be a timestamp frozen on the day the operator
// ticked a checkbox. A frozen activity date is worse than none, because it
// reads as current.
//
// So they live only on the value GitHub just returned. Whoever wants them
// asks GitHub; whoever does not, does not pay for the round trip.
type RemoteRepository struct {
	ID            int64     `json:"id"`
	Owner         string    `json:"owner"`
	OwnerType     string    `json:"owner_type"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Private       bool      `json:"private"`
	Description   string    `json:"description,omitempty"`
	DefaultBranch string    `json:"default_branch"`
	HTMLURL       string    `json:"html_url,omitempty"`
	PushedAt      time.Time `json:"pushed_at,omitzero"`
	// Language is GitHub's own primary-language guess. Empty for a repository
	// with no code GitHub recognises, which is a real and reportable state.
	Language string `json:"language,omitempty"`
	// SizeKB is the repository's size as GitHub reports it, in kibibytes. It
	// is the cheapest available signal for "a stub or a system".
	SizeKB int `json:"size_kb,omitempty"`
	// Archived and Fork change what a repository MEANS. A fork is somebody
	// else's work with the caller's name on the URL, and an archived
	// repository is a finished thing rather than a neglected one — both are
	// facts a reader would otherwise infer wrongly from a stale push date.
	Archived bool `json:"archived,omitempty"`
	Fork     bool `json:"fork,omitempty"`
}

// Commit is one entry of a history listing. Deliberately shallow: a listing
// is for choosing, and choosing needs a sha, a line of message, an author
// and a date.
type Commit struct {
	SHA         string    `json:"sha"`
	Message     string    `json:"message"`
	AuthorName  string    `json:"author_name,omitempty"`
	AuthorLogin string    `json:"author_login,omitempty"`
	Date        time.Time `json:"date,omitzero"`
	HTMLURL     string    `json:"html_url,omitempty"`
}

// CommitDetail is one commit with its diff.
type CommitDetail struct {
	Commit
	Additions int          `json:"additions"`
	Deletions int          `json:"deletions"`
	Files     []CommitFile `json:"files"`
	// FilesTotal is how many files the commit actually touched, which is
	// not always len(Files) — see the truncation note on ToolResultLimits.
	FilesTotal int `json:"files_total"`
}

type CommitFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Patch is the unified diff hunk. Absent for a binary file, and absent
	// when the commit's diff budget ran out before this file — a patch is
	// included whole or not at all, because half a hunk reads as the
	// complete diff of a smaller change.
	//
	// What was NOT shown is recoverable from CommitDetail.FilesTotal, which
	// the tool turns into an explicit note rather than leaving the model to
	// compare two numbers it has no reason to compare.
	Patch string `json:"patch,omitempty"`
}

// CodeSearchHit is one file matching a code search, with the fragments that
// matched.
type CodeSearchHit struct {
	Repository string   `json:"repository"`
	Path       string   `json:"path"`
	HTMLURL    string   `json:"html_url,omitempty"`
	Fragments  []string `json:"fragments,omitempty"`
}

// CodeSearchResult is one search.
type CodeSearchResult struct {
	Hits []CodeSearchHit `json:"hits"`
	// TotalCount is GitHub's own count of matches, which is almost always
	// larger than len(Hits). Carried so a caller can say "showing 10 of 214"
	// rather than implying it saw everything.
	TotalCount int  `json:"total_count"`
	Incomplete bool `json:"incomplete"`
}

// FileContent is one file read at one ref.
type FileContent struct {
	Path string `json:"path"`
	Ref  string `json:"ref,omitempty"`
	SHA  string `json:"sha,omitempty"`
	// Size is the file's real size in bytes, which may be larger than
	// len(Text) when the read was capped.
	Size int    `json:"size"`
	Text string `json:"text"`
	// Truncated says the text was cut. It is a field rather than a suffix in
	// the text because a marker inside the content is a marker a consumer
	// can mistake for content.
	Truncated bool   `json:"truncated"`
	HTMLURL   string `json:"html_url,omitempty"`
}

// PullRequest is one entry of a pull request listing.
type PullRequest struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	Draft     bool      `json:"draft"`
	Merged    bool      `json:"merged"`
	Author    string    `json:"author,omitempty"`
	HeadRef   string    `json:"head_ref,omitempty"`
	BaseRef   string    `json:"base_ref,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	HTMLURL   string    `json:"html_url,omitempty"`
}

// PullRequestDetail is one pull request with its description and totals.
type PullRequestDetail struct {
	PullRequest
	Body         string `json:"body,omitempty"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changed_files"`
	Commits      int    `json:"commits"`
	Mergeable    *bool  `json:"mergeable,omitempty"`
}

/* ── the API ─────────────────────────────────────────────────────────── */

// CommitQuery narrows a history listing. Zero values mean "no narrowing",
// except Limit, which the caller is expected to set and the adapter bounds
// regardless — see the note on API.
type CommitQuery struct {
	// Ref is a branch, tag or sha. Empty means the default branch.
	Ref string
	// Path narrows the history to commits touching one path.
	Path  string
	Limit int
}

type PullRequestQuery struct {
	// State is "open", "closed" or "all". Empty means "open".
	State string
	Limit int
}

// API is everything this integration can do to GitHub.
//
// Every method is a read. Every method takes Credentials explicitly rather
// than reading them from anywhere, so there is no path by which a call
// happens with a credential the caller did not resolve for this workspace.
//
// Every method takes a *domain.Repository rather than an owner/name pair,
// and that is the load-bearing signature choice in this file: the type
// system will not let a caller pass a string that came from a model. To
// call one of these you must first have resolved an authorized row.
type API interface {
	// Viewer identifies the credential. It is also the connection test: the
	// cheapest authenticated round trip GitHub offers.
	Viewer(ctx context.Context, creds Credentials) (Account, error)
	// AccessibleRepositories lists what the credential can reach. Bounded by
	// the adapter; `limit` is a request, never a promise.
	AccessibleRepositories(ctx context.Context, creds Credentials, limit int) ([]RemoteRepository, error)

	ListCommits(ctx context.Context, creds Credentials, repo *domain.Repository, q CommitQuery) ([]Commit, error)
	GetCommit(ctx context.Context, creds Credentials, repo *domain.Repository, ref string) (*CommitDetail, error)
	// SearchCode searches within `repos`, which is always a subset of the
	// authorized set — the qualifier is built here from stored rows, never
	// taken from the caller's query string.
	SearchCode(ctx context.Context, creds Credentials, repos []domain.Repository, query string, limit int) (*CodeSearchResult, error)
	GetFile(ctx context.Context, creds Credentials, repo *domain.Repository, path, ref string) (*FileContent, error)
	ListPullRequests(ctx context.Context, creds Credentials, repo *domain.Repository, q PullRequestQuery) ([]PullRequest, error)
	GetPullRequest(ctx context.Context, creds Credentials, repo *domain.Repository, number int) (*PullRequestDetail, error)
}
