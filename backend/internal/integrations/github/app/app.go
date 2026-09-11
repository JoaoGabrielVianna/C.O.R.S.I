// Package app is the GitHub integration's application layer.
//
// ── The one thing this package exists to guarantee ─────────────────────
// That nothing reaches GitHub except through a credential resolved for one
// workspace and a repository that workspace's operator authorized. Every
// method that touches the network goes through resolve() or
// ResolveRepository, and there is no exported way to obtain Credentials
// without one of them.
//
// It knows nothing about agents, tools, conversations or models. The tools
// package sits above it and translates; if that package were deleted, this
// one would still be a complete, testable integration.
package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/github/domain"
	"github.com/corsi/backend/internal/integrations/github/ports"
	"github.com/corsi/backend/internal/platform/secrets"
)

type Service struct {
	connections  ports.ConnectionRepo
	repositories ports.RepositoryRepo
	api          ports.API
	sealer       *secrets.Sealer
	log          *slog.Logger
}

func NewService(
	connections ports.ConnectionRepo,
	repositories ports.RepositoryRepo,
	api ports.API,
	sealer *secrets.Sealer,
	log *slog.Logger,
) *Service {
	return &Service{
		connections:  connections,
		repositories: repositories,
		api:          api,
		sealer:       sealer,
		log:          log,
	}
}

/* ── credential resolution ───────────────────────────────────────────── */

// resolve reads the workspace's connection and opens its token.
//
// ── Why the token is opened per call and never cached ──────────────────
// Because a cache keyed by workspace is a decision about lifetime that
// nobody has a reason to make, and the failure mode of getting it wrong is
// one workspace spending another's credential. Opening costs one AES-GCM
// operation on a hundred bytes, which is nothing next to the HTTP round
// trip it precedes. The rule is stated in the integrations architecture:
// "a credencial é decifrada apenas no momento da chamada".
func (s *Service) resolve(ctx context.Context, workspaceID uuid.UUID) (*domain.Connection, ports.Credentials, error) {
	conn, err := s.connections.FindByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, ports.Credentials{}, err
	}
	if !s.sealer.Enabled() {
		// The credential is on disk and unreadable. A 503 rather than a 500:
		// the fix is an environment variable, and retrying changes nothing.
		return nil, ports.Credentials{}, domain.NotConfigured(
			"SECRETS_KEY is not configured, so the stored GitHub credential cannot be read")
	}
	token, err := s.sealer.Open(conn.TokenCipher)
	if err != nil {
		// The blob did not authenticate. Almost always a rotated
		// SECRETS_KEY, which is documented to invalidate every stored
		// credential. Said plainly, because the fix is to reconnect.
		s.log.Warn("stored GitHub credential could not be opened", "workspace_id", workspaceID)
		return nil, ports.Credentials{}, domain.Unauthorized(
			"the stored GitHub credential could not be decrypted. This happens after " +
				"SECRETS_KEY is rotated. Reconnect GitHub in Integrations.")
	}
	return conn, ports.Credentials{BaseURL: conn.APIBaseURL, Token: token}, nil
}

// ResolvedRepository is an authorized repository together with the
// credential that may read it.
//
// The two travel as one value on purpose: every caller needs both, and
// returning them separately would create a shape in which somebody obtains
// credentials for one workspace and a repository from another. Here that
// combination cannot be constructed — resolveRepository is the only
// producer, and it reads both from the same connection.
type ResolvedRepository struct {
	Connection *domain.Connection
	Repository *domain.Repository
	Creds      ports.Credentials
}

// ResolveRepository turns a caller-supplied "owner/name" into an authorized
// row, or refuses.
//
// ── The four things it checks, in this order ───────────────────────────
//
//  1. the workspace has a live connection      → github_not_connected
//  2. the string is shaped like owner/name     → github_repository_not_authorized
//  3. a row with that name exists in THIS
//     workspace and THIS connection            → github_repository_not_authorized
//  4. the credential can be opened             → github_unauthorized
//
// Steps 2 and 3 answer with the same code deliberately. A caller that could
// tell "malformed" from "not authorized" from "does not exist" would have a
// probe, and this is precisely the surface a model — or a prompt injected
// into one — would probe.
//
// What comes back carries the STORED owner and name. Whatever string the
// caller sent is discarded here and never reaches a URL.
func (s *Service) ResolveRepository(ctx context.Context, workspaceID uuid.UUID, fullName string) (*ResolvedRepository, error) {
	conn, creds, err := s.resolve(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	named := truncate(strings.TrimSpace(fullName), 100)
	if !domain.ValidFullNameShape(fullName) {
		return nil, domain.RepositoryNotAuthorized(named)
	}
	repo, err := s.repositories.FindByFullName(ctx, workspaceID, conn.ID, fullName)
	if err != nil {
		return nil, err
	}
	return &ResolvedRepository{Connection: conn, Repository: repo, Creds: creds}, nil
}

/* ── connecting ──────────────────────────────────────────────────────── */

// ConnectInput is a request to link an account.
type ConnectInput struct {
	WorkspaceID uuid.UUID
	Token       string
	// APIBaseURL is optional and defaults to github.com. It exists so a
	// GitHub Enterprise host is configuration rather than a redeploy.
	APIBaseURL string
}

// Connect verifies a token, then stores it sealed.
//
// ── Why verification comes before storage ──────────────────────────────
// Because a stored credential that has never worked is worse than no
// credential: the interface would show a connected account, every tool
// would fail at the moment a user was mid-conversation, and the error would
// arrive three layers from the mistake. One round trip to GET /user turns
// that into an immediate, specific refusal on the screen where the token
// was pasted.
//
// It also means the account identity on the card is a fact GitHub asserted
// rather than something the user typed.
func (s *Service) Connect(ctx context.Context, in ConnectInput) (*domain.Connection, error) {
	if err := domain.ValidateToken(in.Token); err != nil {
		return nil, err
	}
	base := domain.NormalizeAPIBaseURL(in.APIBaseURL)
	if err := domain.ValidateAPIBaseURL(base); err != nil {
		return nil, err
	}
	if !s.sealer.Enabled() {
		return nil, domain.NotConfigured(
			"SECRETS_KEY is not configured, so a GitHub credential cannot be stored")
	}

	token := strings.TrimSpace(in.Token)
	account, err := s.api.Viewer(ctx, ports.Credentials{BaseURL: base, Token: token})
	if err != nil {
		return nil, err
	}

	cipher, err := s.sealer.Seal(token)
	if err != nil {
		return nil, domain.NotConfigured("the GitHub credential could not be encrypted")
	}
	now := time.Now().UTC()
	conn := &domain.Connection{
		ID:               uuid.New(),
		WorkspaceID:      in.WorkspaceID,
		AuthKind:         domain.AuthPAT,
		TokenCipher:      cipher,
		TokenHint:        secrets.Hint(token),
		AccountLogin:     account.Login,
		AccountID:        account.ID,
		AccountType:      domain.AccountType(account.Type),
		AccountName:      account.Name,
		AccountAvatarURL: account.AvatarURL,
		APIBaseURL:       base,
		LastVerifiedAt:   &now,
	}
	if err := conn.Validate(); err != nil {
		return nil, err
	}
	if err := s.connections.Upsert(ctx, conn); err != nil {
		return nil, err
	}
	// Deliberately no repository authorization here. Connecting an account
	// authorizes nothing to read: the operator selects repositories as a
	// separate, explicit act. A connect that pre-authorized everything the
	// token could see would make the two-layer model decorative.
	s.log.Info("github connected",
		"workspace_id", in.WorkspaceID, "account", account.Login, "auth_kind", domain.AuthPAT)
	return conn, nil
}

func (s *Service) Disconnect(ctx context.Context, workspaceID uuid.UUID) error {
	return s.connections.Disconnect(ctx, workspaceID)
}

/* ── the status read ─────────────────────────────────────────────────── */

// Status is what the Integrations page renders.
//
// It answers exactly one question — "what can C.O.R.S.I. reach through
// GitHub, and what has it been allowed to use?" — and nothing else. It is
// not a GitHub client in a page.
type Status struct {
	Connected  bool               `json:"connected"`
	Connection *domain.Connection `json:"connection,omitempty"`
	// Repositories is the authorized set, which is also the set the tools
	// can reach. Present even when empty, because "connected and nothing
	// authorized" is the state a person most needs to see explained.
	Repositories []domain.Repository `json:"repositories"`
	// Organizations is derived from the authorized repositories' owners
	// rather than read from GET /user/orgs.
	//
	// ── Why derived ────────────────────────────────────────────────────
	// A fine-grained PAT is scoped to one account and does not reliably
	// enumerate organizations; asking would produce an empty list for most
	// real tokens and a page that looks broken. What the page is actually
	// for is saying which owners C.O.R.S.I. can reach, and the authorized
	// set answers that exactly, always, with no extra request.
	Organizations []string `json:"organizations"`
}

func (s *Service) Status(ctx context.Context, workspaceID uuid.UUID) (*Status, error) {
	conn, err := s.connections.FindByWorkspace(ctx, workspaceID)
	if err != nil {
		if domain.IsKind(err, domain.KindConflict) {
			// Not connected is a state, not a failure. The page renders the
			// disconnected card from this.
			return &Status{Repositories: []domain.Repository{}, Organizations: []string{}}, nil
		}
		return nil, err
	}
	repos, err := s.repositories.ListByConnection(ctx, workspaceID, conn.ID)
	if err != nil {
		return nil, err
	}
	return &Status{
		Connected:     true,
		Connection:    conn,
		Repositories:  repos,
		Organizations: organizationsOf(repos),
	}, nil
}

func organizationsOf(repos []domain.Repository) []string {
	seen := make(map[string]bool, len(repos))
	out := make([]string, 0)
	for i := range repos {
		if repos[i].OwnerType != string(domain.AccountOrg) {
			continue
		}
		key := strings.ToLower(repos[i].Owner)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, repos[i].Owner)
	}
	return out
}

/* ── choosing repositories ───────────────────────────────────────────── */

// AvailableRepository is one repository the credential can reach, with
// whether the operator has authorized it.
type AvailableRepository struct {
	ports.RemoteRepository
	Authorized bool `json:"authorized"`
}

// AvailableRepositories is the picker's read: everything the token can see,
// each marked with whether it is currently allowed.
type AvailableRepositories struct {
	Items []AvailableRepository `json:"items"`
	// Limit is the ceiling the server applied, and Truncated says whether it
	// was reached. A picker that silently stops at three hundred is a picker
	// that tells somebody their repository does not exist.
	Limit     int  `json:"limit"`
	Truncated bool `json:"truncated"`
}

func (s *Service) AvailableRepositories(ctx context.Context, workspaceID uuid.UUID) (*AvailableRepositories, error) {
	conn, creds, err := s.resolve(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	remote, err := s.api.AccessibleRepositories(ctx, creds, domain.MaxAccessibleRepositories)
	if err != nil {
		return nil, err
	}
	// The round trip succeeded, so the credential demonstrably still works.
	// Recording that is what lets the page distinguish "connected and
	// healthy" from "connected, and we have not checked since".
	if err := s.connections.TouchVerified(ctx, workspaceID, conn.ID, time.Now().UTC()); err != nil {
		s.log.Warn("could not record github verification", "workspace_id", workspaceID, "err", err)
	}

	authorized, err := s.repositories.ListByConnection(ctx, workspaceID, conn.ID)
	if err != nil {
		return nil, err
	}
	on := make(map[string]bool, len(authorized))
	for i := range authorized {
		on[strings.ToLower(authorized[i].FullName)] = true
	}

	out := &AvailableRepositories{
		Items:     make([]AvailableRepository, 0, len(remote)),
		Limit:     domain.MaxAccessibleRepositories,
		Truncated: len(remote) >= domain.MaxAccessibleRepositories,
	}
	for _, r := range remote {
		out.Items = append(out.Items, AvailableRepository{
			RemoteRepository: r,
			Authorized:       on[strings.ToLower(r.FullName)],
		})
	}
	return out, nil
}

// SetAuthorizedRepositories replaces the authorized set.
//
// ── Why the names are resolved against GitHub and not trusted ──────────
// The request carries names a browser sent. Writing them straight into the
// table would let a client authorize a repository the credential cannot
// even see — a row that grants nothing today but is a claim the interface
// would repeat, and a claim a later change could act on. So the live set is
// fetched, the request is INTERSECTED with it, and anything outside is
// refused by name.
//
// Refused rather than dropped, for the same reason app/references.go
// refuses an unauthorized tool instead of filtering it: a silently smaller
// result is a screen that says something was saved when it was not.
func (s *Service) SetAuthorizedRepositories(ctx context.Context, workspaceID uuid.UUID, fullNames []string) (*Status, error) {
	conn, creds, err := s.resolve(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if len(fullNames) > domain.MaxAccessibleRepositories {
		return nil, domain.Invalid("too many repositories in one request")
	}

	wanted := make(map[string]bool, len(fullNames))
	for _, n := range fullNames {
		key := domain.NormalizeFullName(n)
		if key == "" {
			continue
		}
		if !domain.ValidFullNameShape(key) {
			return nil, domain.Invalid(truncate(n, 100) + " is not a valid owner/name")
		}
		wanted[key] = true
	}

	remote, err := s.api.AccessibleRepositories(ctx, creds, domain.MaxAccessibleRepositories)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]ports.RemoteRepository, len(remote))
	for _, r := range remote {
		byName[strings.ToLower(r.FullName)] = r
	}

	selected := make([]domain.Repository, 0, len(wanted))
	for key := range wanted {
		r, ok := byName[key]
		if !ok {
			return nil, domain.Invalid(truncate(key, 100) +
				" is not a repository the connected GitHub account can access")
		}
		row := domain.Repository{
			WorkspaceID:   workspaceID,
			ConnectionID:  conn.ID,
			GitHubID:      r.ID,
			Owner:         r.Owner,
			Name:          r.Name,
			FullName:      r.Owner + "/" + r.Name,
			Private:       r.Private,
			DefaultBranch: r.DefaultBranch,
			HTMLURL:       r.HTMLURL,
			Description:   r.Description,
			OwnerType:     r.OwnerType,
		}
		// Validated even though the values came from GitHub. The patterns
		// are what make path construction safe by construction downstream,
		// and a check that only runs when the data is suspicious is a check
		// that has already failed.
		if err := row.Validate(); err != nil {
			return nil, err
		}
		selected = append(selected, row)
	}

	if err := s.repositories.ReplaceAll(ctx, workspaceID, conn.ID, selected); err != nil {
		return nil, err
	}
	s.log.Info("github repository authorization updated",
		"workspace_id", workspaceID, "count", len(selected))
	return s.Status(ctx, workspaceID)
}

/* ── the reads the tools and the page share ──────────────────────────── */

// AuthorizedRepositories is the set every tool is bounded by.
func (s *Service) AuthorizedRepositories(ctx context.Context, workspaceID uuid.UUID) (*domain.Connection, []domain.Repository, error) {
	conn, err := s.connections.FindByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	repos, err := s.repositories.ListByConnection(ctx, workspaceID, conn.ID)
	if err != nil {
		return nil, nil, err
	}
	return conn, repos, nil
}

// RepositoryMetadata is what GitHub currently says about one repository,
// beyond the identity the authorization stored.
//
// Every field here answers the same question: is what I already know enough
// to say something true about this repository, or do I have to open it? A
// language and a size separate a system from a stub; a push date separates
// work in progress from something finished years ago; a fork says the code
// is somebody else's.
type RepositoryMetadata struct {
	Language string
	SizeKB   int
	PushedAt time.Time
	Archived bool
	Fork     bool
}

// ToolListRepositories is the authorized set, with live detail when GitHub
// answered and without it when GitHub did not.
//
// ── Why the enrichment is one call and not one per repository ──────────
// `GET /user/repos` returns everything the credential can reach, with all
// of these fields, in a single paginated read the integration already makes
// for the settings page. Asking per repository would be thirty-two requests
// against a rate limit counted in requests, to answer a question one
// request already answers.
//
// ── Why it is best-effort and the listing is not ───────────────────────
// The authorized set comes from our own database and is the tool's actual
// job: which repositories may be read. The metadata is an improvement on
// top of it. If GitHub is slow, rate-limited or down, a listing that still
// names every authorized repository is far better than a failed call, and
// the caller is told the detail is missing rather than left to read absent
// fields as absent facts.
//
// The map is keyed by GitHub's immutable id rather than by name, because a
// rename between the authorization and this read would silently mismatch
// every string-keyed join and quietly enrich the wrong row.
func (s *Service) ToolListRepositories(ctx context.Context, workspaceID uuid.UUID) ([]domain.Repository, map[int64]RepositoryMetadata, error) {
	conn, creds, err := s.resolve(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	repos, err := s.repositories.ListByConnection(ctx, workspaceID, conn.ID)
	if err != nil {
		return nil, nil, err
	}

	remote, err := s.api.AccessibleRepositories(ctx, creds, domain.MaxAccessibleRepositories)
	if err != nil {
		s.log.Warn("github repository metadata unavailable",
			"workspace_id", workspaceID, "err", err)
		return repos, nil, nil
	}

	meta := make(map[int64]RepositoryMetadata, len(remote))
	for i := range remote {
		r := &remote[i]
		meta[r.ID] = RepositoryMetadata{
			Language: r.Language,
			SizeKB:   r.SizeKB,
			PushedAt: r.PushedAt,
			Archived: r.Archived,
			Fork:     r.Fork,
		}
	}
	return repos, meta, nil
}

/* ── the tool-facing reads ───────────────────────────────────────────── */

// The methods below are what the tools call, and their shape is the point.
//
// ── Why they take a repository NAME and not a resolved repository ──────
// Because the alternative was an exported ResolveRepository plus an
// exported API handle, and a caller holding both can combine a credential
// resolved for one repository with a request for another. Nothing stops it,
// and nothing would notice. Here the resolution and the call are one
// method: there is no arrangement of these signatures that reaches GitHub
// without first passing the authorization check, because the only thing
// that produces Credentials is the same call that produces the row.
//
// Each returns the resolved repository alongside the result, so the tool
// can report the STORED name — which is what makes a transcript say the
// repository that was actually read rather than the string that was typed.

// ToolListCommits lists commits of an authorized repository.
func (s *Service) ToolListCommits(ctx context.Context, workspaceID uuid.UUID, named string, q ports.CommitQuery) (*domain.Repository, []ports.Commit, error) {
	res, err := s.ResolveRepository(ctx, workspaceID, named)
	if err != nil {
		return nil, nil, err
	}
	commits, err := s.api.ListCommits(ctx, res.Creds, res.Repository, q)
	if err != nil {
		return nil, nil, err
	}
	return res.Repository, commits, nil
}

// ToolGetCommit reads one commit of an authorized repository.
func (s *Service) ToolGetCommit(ctx context.Context, workspaceID uuid.UUID, named, ref string) (*domain.Repository, *ports.CommitDetail, error) {
	res, err := s.ResolveRepository(ctx, workspaceID, named)
	if err != nil {
		return nil, nil, err
	}
	detail, err := s.api.GetCommit(ctx, res.Creds, res.Repository, ref)
	if err != nil {
		return nil, nil, err
	}
	return res.Repository, detail, nil
}

// ToolGetFile reads one file of an authorized repository.
func (s *Service) ToolGetFile(ctx context.Context, workspaceID uuid.UUID, named, path, ref string) (*domain.Repository, *ports.FileContent, error) {
	res, err := s.ResolveRepository(ctx, workspaceID, named)
	if err != nil {
		return nil, nil, err
	}
	file, err := s.api.GetFile(ctx, res.Creds, res.Repository, path, ref)
	if err != nil {
		return nil, nil, err
	}
	return res.Repository, file, nil
}

// ToolListPullRequests lists pull requests of an authorized repository.
func (s *Service) ToolListPullRequests(ctx context.Context, workspaceID uuid.UUID, named string, q ports.PullRequestQuery) (*domain.Repository, []ports.PullRequest, error) {
	res, err := s.ResolveRepository(ctx, workspaceID, named)
	if err != nil {
		return nil, nil, err
	}
	pulls, err := s.api.ListPullRequests(ctx, res.Creds, res.Repository, q)
	if err != nil {
		return nil, nil, err
	}
	return res.Repository, pulls, nil
}

// ToolGetPullRequest reads one pull request of an authorized repository.
func (s *Service) ToolGetPullRequest(ctx context.Context, workspaceID uuid.UUID, named string, number int) (*domain.Repository, *ports.PullRequestDetail, error) {
	res, err := s.ResolveRepository(ctx, workspaceID, named)
	if err != nil {
		return nil, nil, err
	}
	pr, err := s.api.GetPullRequest(ctx, res.Creds, res.Repository, number)
	if err != nil {
		return nil, nil, err
	}
	return res.Repository, pr, nil
}

// ToolSearchCode searches either one authorized repository or all of them.
//
// ── Why the "all" case is not a special case ───────────────────────────
// Both branches produce a slice of stored rows, and that slice is what
// becomes the `repo:` qualifiers. There is no path in which the scope comes
// from anywhere but this table — which is what makes "the model cannot
// search a repository it was not given" a property of the code rather than
// a claim about GitHub's query grammar.
//
// An empty authorized set searches nothing and returns an empty result
// rather than an error: a workspace that has connected GitHub and
// authorized no repositories has a true, explainable answer, and a refusal
// would send the model looking for a workaround.
func (s *Service) ToolSearchCode(ctx context.Context, workspaceID uuid.UUID, named, query string) ([]domain.Repository, *ports.CodeSearchResult, error) {
	if named = strings.TrimSpace(named); named != "" {
		res, err := s.ResolveRepository(ctx, workspaceID, named)
		if err != nil {
			return nil, nil, err
		}
		scope := []domain.Repository{*res.Repository}
		out, err := s.api.SearchCode(ctx, res.Creds, scope, query, domain.MaxSearchResults)
		if err != nil {
			return nil, nil, err
		}
		return scope, out, nil
	}

	conn, creds, err := s.resolve(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	scope, err := s.repositories.ListByConnection(ctx, workspaceID, conn.ID)
	if err != nil {
		return nil, nil, err
	}
	out, err := s.api.SearchCode(ctx, creds, scope, query, domain.MaxSearchResults)
	if err != nil {
		return nil, nil, err
	}
	return scope, out, nil
}

// ActivityEntry is one recent commit, with the repository it came from.
type ActivityEntry struct {
	Repository string `json:"repository"`
	ports.Commit
}

// MaxActivityRepositories bounds how many repositories the activity feed
// reads from.
//
// The page shows "what has been happening"; it is not a dashboard. Five
// repositories at five commits each is one screen and five requests against
// a five-thousand-per-hour budget, which is a page somebody can refresh
// without thinking about it.
const (
	MaxActivityRepositories = 5
	ActivityCommitsPerRepo  = 5
)

// RecentActivity reads the newest commits across the authorized set.
//
// ── Why a failing repository does not fail the page ────────────────────
// One repository can 404 because it was deleted or access was withdrawn at
// GitHub's end, and that must not blank a page whose whole job is to say
// what is reachable. The failure is logged and the entry is skipped; the
// page reports how many repositories it managed to read.
func (s *Service) RecentActivity(ctx context.Context, workspaceID uuid.UUID) ([]ActivityEntry, error) {
	conn, creds, err := s.resolve(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	repos, err := s.repositories.ListByConnection(ctx, workspaceID, conn.ID)
	if err != nil {
		return nil, err
	}
	out := make([]ActivityEntry, 0, MaxActivityRepositories*ActivityCommitsPerRepo)
	for i := range repos {
		if i >= MaxActivityRepositories {
			break
		}
		commits, err := s.api.ListCommits(ctx, creds, &repos[i],
			ports.CommitQuery{Limit: ActivityCommitsPerRepo})
		if err != nil {
			// A revoked credential or an exhausted rate limit is not a
			// per-repository problem and must not be reported as an empty
			// feed. Everything else is skipped.
			if domain.IsKind(err, domain.KindUnauthorized) || domain.IsKind(err, domain.KindRateLimited) {
				return nil, err
			}
			s.log.Warn("skipping repository in github activity",
				"repository", repos[i].FullName, "err", err)
			continue
		}
		for _, c := range commits {
			out = append(out, ActivityEntry{Repository: repos[i].FullName, Commit: c})
		}
	}
	return out, nil
}

/* ── helpers ─────────────────────────────────────────────────────────── */

// truncate bounds a caller-supplied string before it goes into a message.
//
// Everything a model sends is echoed through here first. Without it, an
// error message is an unbounded channel from the model into a log file and
// an audit row.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
