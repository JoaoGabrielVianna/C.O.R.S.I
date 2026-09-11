// Package api is the driven adapter that talks to GitHub's REST API.
//
// Written against net/http on purpose, the same call the LLM adapter made.
// The wire format is JSON over seven endpoints; a vendor SDK would add a
// dependency, a release cadence, and an opinion about authentication, in
// exchange for a few hundred lines that are easier to reason about than the
// SDK's own error taxonomy.
//
// ── What this package must never do ────────────────────────────────────
//   - put a token in a URL, a log line, an error message, or a returned
//     value. It goes in one place: the Authorization header.
//   - follow a redirect to a different host. The default http.Client would,
//     and it would take the header with it.
//   - build a path from a string the caller did not first resolve to an
//     authorized row. Every method takes *domain.Repository for that reason.
//   - read an unbounded response body.
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/corsi/backend/internal/integrations/github/domain"
	"github.com/corsi/backend/internal/integrations/github/ports"
)

const (
	// requestTimeout bounds one round trip to GitHub.
	//
	// Below the ten seconds Agents gives a whole tool execution, so a slow
	// GitHub produces this adapter's actionable message rather than the
	// executor's generic timeout. The caller's context still applies on top:
	// pressing stop cancels an in-flight request immediately.
	requestTimeout = 8 * time.Second
	// errBodyLimit bounds how much of a GitHub error body is read before
	// giving up. Four kibibytes, matching the rule the integrations
	// architecture states: an HTML error page must not fill a log line.
	errBodyLimit = 4 << 10
	// bodyLimit bounds a successful response body.
	//
	// Eight mebibytes. Far above any response these endpoints produce, and
	// finite — which is the whole point. An unbounded ReadAll on a remote
	// body is a memory budget the remote host controls.
	bodyLimit = 8 << 20

	apiVersionHeader = "X-GitHub-Api-Version"
	apiVersion       = "2022-11-28"
	acceptJSON       = "application/vnd.github+json"
	userAgent        = "corsi"
)

type Client struct {
	http *http.Client
	log  *slog.Logger
}

func New(log *slog.Logger) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &Client{
		http: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
			// ── Why redirects are refused rather than followed ──────────
			//
			// Every request here carries a credential in a header. Go's
			// default policy follows up to ten redirects and re-sends the
			// header to whatever host the response names — so a compromised
			// or misconfigured endpoint answering `302 Location:
			// https://elsewhere/` would be handed the token.
			//
			// The API does not need redirects: GitHub answers these
			// endpoints directly. So the policy is "never", and a redirect
			// becomes a visible upstream error rather than an invisible
			// credential disclosure.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		log: log,
	}
}

/* ── the request ─────────────────────────────────────────────────────── */

// do issues one authenticated GET and decodes the body into out.
//
// path must already be safe: it is built by the callers below from stored
// repository columns and url.PathEscape'd segments, never from a raw string
// a model produced.
func (c *Client) do(ctx context.Context, creds ports.Credentials, path string, query url.Values, out any) error {
	return c.doAccept(ctx, creds, path, query, "", out)
}

// doAccept is do with an Accept header other than the default, which
// exactly one endpoint needs. Empty `accept` means the default.
//
// Every request in this package goes through here, so the four rules that
// must hold on all of them — the credential in one header, no redirect
// following, a bounded body, a mapped status — hold in one place.
func (c *Client) doAccept(ctx context.Context, creds ports.Credentials, path string, query url.Values, accept string, out any) error {
	base := domain.NormalizeAPIBaseURL(creds.BaseURL)
	full := base + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		// The URL was assembled from a validated base and escaped segments,
		// so this is not reachable from user input. It still must not carry
		// `full` into the message — the base URL is harmless, but the habit
		// of formatting request details into errors is how a token ends up
		// in one on the day somebody adds a query parameter.
		return domain.Upstream("could not build a request to GitHub")
	}
	// The credential goes here and nowhere else.
	req.Header.Set("Authorization", "Bearer "+creds.Token)
	if accept == "" {
		accept = acceptJSON
	}
	req.Header.Set("Accept", accept)
	req.Header.Set(apiVersionHeader, apiVersion)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		// ── Why the transport error is not echoed verbatim ─────────────
		//
		// url.Error's message contains the request URL. That URL never
		// contains the token today, and the day somebody adds a query
		// parameter carelessly it would — and this line would publish it
		// into a 502 body. So the shape of the failure is reported and the
		// detail is dropped.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		c.log.Warn("github request failed", "path", path, "err", transportReason(err))
		return domain.Upstream("could not reach GitHub: " + transportReason(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return c.statusError(resp)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	if err != nil {
		return domain.Upstream("could not read GitHub's response")
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return domain.Upstream("GitHub returned a response this integration could not read")
	}
	return nil
}

// transportReason reduces a transport failure to its shape, without the URL
// the standard library's error carries.
func transportReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "the request timed out"
	case errors.Is(err, context.Canceled):
		return "the request was cancelled"
	default:
		return "the connection failed"
	}
}

// statusError turns a non-200 into the right domain error.
//
// ── Why these are told apart ───────────────────────────────────────────
// Because they lead to four different actions. A 401 means a human must
// reconnect. A 403-with-no-remaining means wait. A 403-with-remaining means
// the token lacks a permission, which is also a human. A 404 on a
// repository we know is authorized means it was deleted or access was
// removed at GitHub's end. Collapsing them into "upstream error" would make
// every one of those look like an outage.
func (c *Client) statusError(resp *http.Response) error {
	// Read a bounded prefix of the body for GitHub's own message. It is
	// short JSON on every documented failure.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
	var payload struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &payload)
	// GitHub's message is quoted because it is genuinely actionable
	// ("Resource not accessible by personal access token" names the fix).
	// It is bounded because it is remote text, and it never contains what
	// we sent — the API does not echo request headers.
	remote := truncate(strings.TrimSpace(payload.Message), 200)

	remaining, hasRemaining := headerInt(resp, "X-RateLimit-Remaining")
	reset, _ := headerInt(resp, "X-RateLimit-Reset")

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return domain.Unauthorized(
			"GitHub rejected the stored credential. Reconnect GitHub in Integrations with a valid token.")

	case http.StatusForbidden, http.StatusTooManyRequests:
		// The primary rate limit is a 403 or a 429 whose remaining count is
		// zero. A 403 with budget left is a permission problem wearing the
		// same status code, and the two must not read alike.
		if resp.StatusCode == http.StatusTooManyRequests || (hasRemaining && remaining == 0) {
			return domain.RateLimited(rateLimitMessage(resp, reset))
		}
		// A secondary rate limit answers 403 with a retry-after and a
		// message that says so, while leaving the primary budget intact.
		if retry := resp.Header.Get("Retry-After"); retry != "" {
			return domain.RateLimited(
				"GitHub applied a secondary rate limit. Try again in " + retry + " seconds.")
		}
		msg := "GitHub refused this request: the connected token does not have permission for it."
		if remote != "" {
			msg += " GitHub said: " + remote
		}
		return domain.Unauthorized(msg)

	case http.StatusNotFound:
		// Deliberately not domain.NotFound with a resource name the caller
		// chose: GitHub answers 404 both for "does not exist" and for "you
		// may not see it", and this integration must not turn that into a
		// probe that distinguishes them.
		return &domain.Error{
			Kind: domain.KindNotFound,
			Message: "GitHub has nothing at that address, or the connected token cannot see it. " +
				"Check the reference, path or number.",
		}

	case http.StatusUnprocessableEntity:
		msg := "GitHub could not process the request."
		if remote != "" {
			msg += " " + remote
		}
		return domain.Invalid(msg)

	default:
		if resp.StatusCode >= 500 {
			return domain.Upstream("GitHub is having trouble (HTTP " +
				strconv.Itoa(resp.StatusCode) + "). This is not a problem with your data.")
		}
		msg := "GitHub answered HTTP " + strconv.Itoa(resp.StatusCode) + "."
		if remote != "" {
			msg += " " + remote
		}
		return domain.Upstream(msg)
	}
}

func rateLimitMessage(resp *http.Response, reset int) string {
	resource := resp.Header.Get("X-RateLimit-Resource")
	what := "GitHub's rate limit"
	if resource == "code_search" || resource == "search" {
		// Worth naming: code search is limited to ten requests per minute
		// against five thousand per hour for everything else, so hitting it
		// means something very different from hitting the core limit.
		what = "GitHub's search rate limit (10 searches per minute)"
	}
	msg := what + " was reached."
	if reset > 0 {
		wait := int(time.Until(time.Unix(int64(reset), 0)).Seconds())
		if wait > 0 {
			msg += " It resets in about " + strconv.Itoa(wait) + " seconds."
		} else {
			msg += " It should have reset already; try once more."
		}
	}
	return msg + " Wait and ask again, or ask something narrower."
}

func headerInt(resp *http.Response, name string) (int, bool) {
	v := resp.Header.Get(name)
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

/* ── Viewer ──────────────────────────────────────────────────────────── */

type wireUser struct {
	Login     string `json:"login"`
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

func (c *Client) Viewer(ctx context.Context, creds ports.Credentials) (ports.Account, error) {
	var u wireUser
	if err := c.do(ctx, creds, "/user", nil, &u); err != nil {
		return ports.Account{}, err
	}
	if u.Login == "" || u.ID == 0 {
		return ports.Account{}, domain.Upstream(
			"GitHub answered without identifying the account behind this token")
	}
	typ := u.Type
	if typ != string(domain.AccountOrg) {
		typ = string(domain.AccountUser)
	}
	return ports.Account{
		Login:     u.Login,
		ID:        u.ID,
		Type:      typ,
		Name:      u.Name,
		AvatarURL: u.AvatarURL,
	}, nil
}

/* ── repositories ────────────────────────────────────────────────────── */

type wireRepo struct {
	ID    int64 `json:"id"`
	Owner struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"owner"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Private       bool      `json:"private"`
	Description   string    `json:"description"`
	DefaultBranch string    `json:"default_branch"`
	HTMLURL       string    `json:"html_url"`
	PushedAt      time.Time `json:"pushed_at"`
	Language      string    `json:"language"`
	// GitHub reports `size` in kibibytes on this endpoint. The field name
	// says so here because the API's does not, and a number whose unit lives
	// only in documentation is a number somebody will eventually read as
	// bytes.
	SizeKB   int  `json:"size"`
	Archived bool `json:"archived"`
	Fork     bool `json:"fork"`
}

func (r wireRepo) normalize() ports.RemoteRepository {
	ownerType := r.Owner.Type
	if ownerType != string(domain.AccountOrg) {
		ownerType = string(domain.AccountUser)
	}
	return ports.RemoteRepository{
		ID:            r.ID,
		Owner:         r.Owner.Login,
		OwnerType:     ownerType,
		Name:          r.Name,
		FullName:      r.FullName,
		Private:       r.Private,
		Description:   truncate(r.Description, 1000),
		DefaultBranch: r.DefaultBranch,
		HTMLURL:       r.HTMLURL,
		PushedAt:      r.PushedAt,
		Language:      truncate(r.Language, 40),
		SizeKB:        r.SizeKB,
		Archived:      r.Archived,
		Fork:          r.Fork,
	}
}

// AccessibleRepositories walks GET /user/repos.
//
// ── Why pagination is bounded by a page count and not by a Link header ──
// Following `Link: rel="next"` until it stops is how a client with a
// hundred-thousand-repository token spends its whole rate budget on one
// page load. The loop stops at the requested ceiling, and the caller is
// told what the ceiling was — see the `truncated` field the HTTP layer
// returns.
func (c *Client) AccessibleRepositories(ctx context.Context, creds ports.Credentials, limit int) ([]ports.RemoteRepository, error) {
	if limit <= 0 || limit > domain.MaxAccessibleRepositories {
		limit = domain.MaxAccessibleRepositories
	}
	out := make([]ports.RemoteRepository, 0, limit)
	for page := 1; len(out) < limit; page++ {
		q := url.Values{}
		q.Set("per_page", strconv.Itoa(domain.PageSize))
		q.Set("page", strconv.Itoa(page))
		// Most recently pushed first: the repositories somebody wants to
		// authorize are the ones they are working in.
		q.Set("sort", "pushed")
		q.Set("direction", "desc")
		// Everything the token can reach, which for a fine-grained PAT is
		// exactly the repositories it was scoped to at GitHub.
		q.Set("affiliation", "owner,collaborator,organization_member")

		var batch []wireRepo
		if err := c.do(ctx, creds, "/user/repos", q, &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			if len(out) == limit {
				break
			}
			out = append(out, r.normalize())
		}
		if len(batch) < domain.PageSize {
			break
		}
	}
	return out, nil
}

/* ── commits ─────────────────────────────────────────────────────────── */

type wireCommitAuthor struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Date  time.Time `json:"date"`
}

type wireCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message   string           `json:"message"`
		Author    wireCommitAuthor `json:"author"`
		Committer wireCommitAuthor `json:"committer"`
	} `json:"commit"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	HTMLURL string `json:"html_url"`
	Stats   struct {
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
	} `json:"stats"`
	Files []struct {
		Filename  string `json:"filename"`
		Status    string `json:"status"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
		Patch     string `json:"patch"`
	} `json:"files"`
}

func (w wireCommit) normalize() ports.Commit {
	date := w.Commit.Author.Date
	if date.IsZero() {
		date = w.Commit.Committer.Date
	}
	return ports.Commit{
		SHA: w.SHA,
		// Truncated here rather than at the tool, because a commit message
		// is remote text of unbounded length and the contract promises a
		// bounded value. A body-length essay in a merge commit must not be
		// able to consume a listing's whole budget.
		Message:     truncate(w.Commit.Message, domain.MaxCommitMessage),
		AuthorName:  w.Commit.Author.Name,
		AuthorLogin: w.Author.Login,
		Date:        date,
		HTMLURL:     w.HTMLURL,
	}
}

// repoPath builds "/repos/{owner}/{repo}" from STORED columns.
//
// ── The invariant this function carries ────────────────────────────────
// Its input is a *domain.Repository, which can only have come from a
// database row, which can only have been written from a GitHub response
// after passing domain.Repository.Validate. Owner and Name are therefore
// already known to match anchored patterns containing no slash and no dot
// segment. PathEscape is applied anyway — defence that costs nothing and
// does not depend on a claim about a different file staying true.
func repoPath(repo *domain.Repository) string {
	return "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)
}

func (c *Client) ListCommits(ctx context.Context, creds ports.Credentials, repo *domain.Repository, q ports.CommitQuery) ([]ports.Commit, error) {
	limit := q.Limit
	if limit <= 0 || limit > domain.MaxCommitsPerCall {
		limit = domain.DefaultCommits
	}
	query := url.Values{}
	query.Set("per_page", strconv.Itoa(limit))
	if q.Ref != "" {
		query.Set("sha", q.Ref)
	}
	if q.Path != "" {
		query.Set("path", q.Path)
	}

	var batch []wireCommit
	if err := c.do(ctx, creds, repoPath(repo)+"/commits", query, &batch); err != nil {
		return nil, err
	}
	out := make([]ports.Commit, 0, len(batch))
	for _, w := range batch {
		out = append(out, w.normalize())
	}
	return out, nil
}

// GetCommit reads one commit with its diff, bounded on two axes.
//
// GitHub returns up to 300 files inline and paginates beyond that; the
// patch of a large refactor is megabytes. Neither can be handed to a model,
// so both the file count and the total patch volume are capped here, and
// what was dropped is reported rather than silently missing.
func (c *Client) GetCommit(ctx context.Context, creds ports.Credentials, repo *domain.Repository, ref string) (*ports.CommitDetail, error) {
	var w wireCommit
	path := repoPath(repo) + "/commits/" + url.PathEscape(ref)
	if err := c.do(ctx, creds, path, nil, &w); err != nil {
		return nil, err
	}

	detail := &ports.CommitDetail{
		Commit:     w.normalize(),
		Additions:  w.Stats.Additions,
		Deletions:  w.Stats.Deletions,
		FilesTotal: len(w.Files),
	}
	budget := domain.MaxCommitPatchByte
	for i, f := range w.Files {
		if i >= domain.MaxCommitFiles {
			break
		}
		cf := ports.CommitFile{
			Path:      f.Filename,
			Status:    f.Status,
			Additions: f.Additions,
			Deletions: f.Deletions,
		}
		// A patch is included whole or not at all. Half a hunk is worse
		// than none: it reads as a complete diff of a smaller change, and
		// nothing in the payload would contradict that reading.
		if f.Patch != "" && len(f.Patch) <= budget {
			cf.Patch = f.Patch
			budget -= len(f.Patch)
		}
		detail.Files = append(detail.Files, cf)
	}
	return detail, nil
}

/* ── code search ─────────────────────────────────────────────────────── */

type wireSearchCode struct {
	TotalCount        int  `json:"total_count"`
	IncompleteResults bool `json:"incomplete_results"`
	Items             []struct {
		Path       string `json:"path"`
		HTMLURL    string `json:"html_url"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		TextMatches []struct {
			Fragment string `json:"fragment"`
		} `json:"text_matches"`
	} `json:"items"`
}

// SearchCode runs GET /search/code, scoped to authorized repositories.
//
// ── How the scope is enforced ──────────────────────────────────────────
// The `repo:` qualifiers are appended here, built from stored rows. The
// caller's query contributes the search TERMS and nothing else — it never
// contributes a qualifier that could widen the scope, because whatever it
// contains is ANDed with a fixed list of repositories. GitHub's search
// grammar has no operator that escapes a conjunction, and even if the query
// smuggled a `repo:` of its own, the result would be a search for a
// repository that is also one of ours: an empty result, not a leak.
//
// The result is then filtered against the same set anyway. That is not
// redundant paranoia — it is the check that does not depend on being right
// about a third party's query grammar.
func (c *Client) SearchCode(ctx context.Context, creds ports.Credentials, repos []domain.Repository, query string, limit int) (*ports.CodeSearchResult, error) {
	if len(repos) == 0 {
		// Not an error: a workspace with a connection and no authorized
		// repositories has nothing to search, and saying so is more useful
		// than a refusal the model will try to work around.
		return &ports.CodeSearchResult{Hits: []ports.CodeSearchHit{}}, nil
	}
	terms := strings.TrimSpace(query)
	if terms == "" {
		return nil, domain.Invalid("a code search needs at least one search term")
	}
	if utf8.RuneCountInString(terms) > domain.MaxSearchQueryLength {
		return nil, domain.Invalid("the search query is too long")
	}
	if limit <= 0 || limit > domain.MaxSearchResults {
		limit = domain.MaxSearchResults
	}

	// GitHub caps a search query at 256 characters plus five `AND`/`OR`/`NOT`
	// operators; qualifiers do not count against the term length but the URL
	// still has to be sane. Scoping to more than a handful of repositories
	// at once is also a query GitHub frequently answers with
	// incomplete_results, so the qualifier list is bounded and the caller is
	// told when it was.
	const maxRepoQualifiers = 10
	allowed := make(map[string]bool, len(repos))
	var b strings.Builder
	b.WriteString(terms)
	for i := range repos {
		allowed[strings.ToLower(repos[i].FullName)] = true
		if i < maxRepoQualifiers {
			b.WriteString(" repo:")
			b.WriteString(repos[i].FullName)
		}
	}

	q := url.Values{}
	q.Set("q", b.String())
	q.Set("per_page", strconv.Itoa(limit))

	// The text-match media type is what makes a hit useful: without it a
	// result is a filename, and the model has to open every one to find out
	// which is relevant. It is requested on this endpoint only.
	var w wireSearchCode
	if err := c.doAccept(ctx, creds, "/search/code", q,
		"application/vnd.github.text-match+json", &w); err != nil {
		return nil, err
	}

	res := &ports.CodeSearchResult{
		TotalCount: w.TotalCount,
		Incomplete: w.IncompleteResults,
		Hits:       make([]ports.CodeSearchHit, 0, len(w.Items)),
	}
	for _, item := range w.Items {
		// The second gate. A hit from a repository that is not authorized is
		// dropped, whatever GitHub thought the query meant.
		if !allowed[strings.ToLower(item.Repository.FullName)] {
			c.log.Warn("dropped a code search hit outside the authorized set",
				"repository", item.Repository.FullName)
			continue
		}
		hit := ports.CodeSearchHit{
			Repository: item.Repository.FullName,
			Path:       item.Path,
			HTMLURL:    item.HTMLURL,
		}
		for i, m := range item.TextMatches {
			if i >= domain.MaxSearchFragments {
				break
			}
			hit.Fragments = append(hit.Fragments, truncate(m.Fragment, domain.MaxSearchFragmentBytes))
		}
		res.Hits = append(res.Hits, hit)
		if len(res.Hits) == limit {
			break
		}
	}
	return res, nil
}

/* ── file contents ───────────────────────────────────────────────────── */

type wireContent struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	SHA      string `json:"sha"`
	Size     int    `json:"size"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	HTMLURL  string `json:"html_url"`
}

// GetFile reads one file at one ref.
//
// ── Why the path is escaped segment by segment ─────────────────────────
// Because `url.PathEscape` on the whole path would escape the separators
// too, and GitHub's contents endpoint addresses a file by its real path.
// Escaping per segment keeps the separators and neutralises everything
// else — and because each segment is escaped, a `..` cannot become a
// traversal of OUR url structure. It could still address a parent
// directory inside the repository, which is why `..` is refused outright:
// a request for `src/../../../etc/passwd` is not a path a caller has any
// legitimate reason to send, and GitHub resolving it is not a defence we
// control.
func (c *Client) GetFile(ctx context.Context, creds ports.Credentials, repo *domain.Repository, path, ref string) (*ports.FileContent, error) {
	clean, err := cleanRepoPath(path)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if ref != "" {
		q.Set("ref", ref)
	}

	var w wireContent
	if err := c.do(ctx, creds, repoPath(repo)+"/contents/"+clean, q, &w); err != nil {
		return nil, err
	}
	if w.Type == "dir" {
		return nil, domain.Invalid("that path is a directory, not a file")
	}
	if w.Type != "file" {
		return nil, domain.Invalid("that path is not a readable file")
	}
	// A file above 1 MiB comes back with an empty content field and
	// encoding "none". Reporting that as an empty file would be a lie the
	// model would answer with confidence.
	if w.Encoding == "none" || (w.Content == "" && w.Size > 0) {
		return nil, domain.Invalid(
			"that file is too large for GitHub to return through this API (" +
				strconv.Itoa(w.Size) + " bytes). Ask for a smaller file.")
	}
	if w.Encoding != "base64" {
		return nil, domain.Upstream("GitHub returned the file in an encoding this integration cannot read")
	}
	// GitHub wraps base64 at 60 characters; the standard decoder rejects
	// newlines, so they are removed before decoding.
	raw, decErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(w.Content, "\n", ""))
	if decErr != nil {
		return nil, domain.Upstream("GitHub returned file content this integration could not decode")
	}
	if !utf8.Valid(raw) {
		// A binary file rendered into a JSON string is bytes the model
		// cannot use and tokens the operator pays for.
		return nil, domain.Invalid("that file is binary, not text")
	}

	out := &ports.FileContent{
		Path:    w.Path,
		Ref:     ref,
		SHA:     w.SHA,
		Size:    w.Size,
		HTMLURL: w.HTMLURL,
	}
	if len(raw) > domain.MaxFileBytes {
		out.Text = string(truncateUTF8(raw, domain.MaxFileBytes))
		out.Truncated = true
	} else {
		out.Text = string(raw)
	}
	return out, nil
}

// cleanRepoPath validates and escapes a repository-relative path.
func cleanRepoPath(raw string) (string, error) {
	p := strings.Trim(strings.TrimSpace(raw), "/")
	if p == "" {
		return "", domain.Invalid("path required")
	}
	if len(p) > domain.MaxFilePathLength {
		return "", domain.Invalid("path is too long")
	}
	segments := strings.Split(p, "/")
	escaped := make([]string, 0, len(segments))
	for _, s := range segments {
		if s == "" || s == "." || s == ".." {
			return "", domain.Invalid(
				"path must be a plain repository path, without . or .. segments")
		}
		escaped = append(escaped, url.PathEscape(s))
	}
	return strings.Join(escaped, "/"), nil
}

/* ── pull requests ───────────────────────────────────────────────────── */

type wirePull struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Draft  bool   `json:"draft"`
	Merged bool   `json:"merged"`
	Body   string `json:"body"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	HTMLURL      string    `json:"html_url"`
	Additions    int       `json:"additions"`
	Deletions    int       `json:"deletions"`
	ChangedFiles int       `json:"changed_files"`
	Commits      int       `json:"commits"`
	Mergeable    *bool     `json:"mergeable"`
}

func (w wirePull) normalize() ports.PullRequest {
	return ports.PullRequest{
		Number:    w.Number,
		Title:     truncate(w.Title, 300),
		State:     w.State,
		Draft:     w.Draft,
		Merged:    w.Merged,
		Author:    w.User.Login,
		HeadRef:   w.Head.Ref,
		BaseRef:   w.Base.Ref,
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
		HTMLURL:   w.HTMLURL,
	}
}

func (c *Client) ListPullRequests(ctx context.Context, creds ports.Credentials, repo *domain.Repository, q ports.PullRequestQuery) ([]ports.PullRequest, error) {
	limit := q.Limit
	if limit <= 0 || limit > domain.MaxPullRequests {
		limit = domain.DefaultPullRequests
	}
	state := q.State
	switch state {
	case "open", "closed", "all":
	case "":
		state = "open"
	default:
		return nil, domain.Invalid("state must be open, closed or all")
	}

	query := url.Values{}
	query.Set("state", state)
	query.Set("per_page", strconv.Itoa(limit))
	query.Set("sort", "updated")
	query.Set("direction", "desc")

	var batch []wirePull
	if err := c.do(ctx, creds, repoPath(repo)+"/pulls", query, &batch); err != nil {
		return nil, err
	}
	out := make([]ports.PullRequest, 0, len(batch))
	for _, w := range batch {
		out = append(out, w.normalize())
	}
	return out, nil
}

func (c *Client) GetPullRequest(ctx context.Context, creds ports.Credentials, repo *domain.Repository, number int) (*ports.PullRequestDetail, error) {
	if number <= 0 {
		return nil, domain.Invalid("number must be a positive pull request number")
	}
	var w wirePull
	path := repoPath(repo) + "/pulls/" + strconv.Itoa(number)
	if err := c.do(ctx, creds, path, nil, &w); err != nil {
		return nil, err
	}
	return &ports.PullRequestDetail{
		PullRequest:  w.normalize(),
		Body:         truncate(w.Body, domain.MaxPullRequestBody),
		Additions:    w.Additions,
		Deletions:    w.Deletions,
		ChangedFiles: w.ChangedFiles,
		Commits:      w.Commits,
		Mergeable:    w.Mergeable,
	}, nil
}

/* ── helpers ─────────────────────────────────────────────────────────── */

// truncate cuts a string to n bytes on a rune boundary, appending an
// ellipsis so the cut is visible in the value itself. Used only for prose —
// never for anything a later call has to match exactly.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return string(truncateUTF8([]byte(s), n)) + "…"
}

// truncateUTF8 cuts a byte slice at or before n bytes without splitting a
// rune, so the result is still valid UTF-8 and still encodes as JSON.
func truncateUTF8(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return b[:cut]
}

// compile-time proof that the adapter satisfies the port it was written for.
var _ ports.API = (*Client)(nil)
