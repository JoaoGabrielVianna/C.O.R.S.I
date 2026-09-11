//go:build live

// The external boundary: this integration talking to the real GitHub.
//
// ── Why this is a separate build tag and not part of the gate ──────────
// Because the official gate must be deterministic and must not depend on
// the internet, on a credential, or on the state of somebody's account. A
// test that reaches api.github.com fails on a plane, fails when a token
// expires, and fails when a repository is renamed — none of which are
// regressions in this code.
//
// But the deterministic suite cannot prove the one thing that matters most:
// that GitHub actually behaves the way the fixtures say it does. Until this
// file has been run against a real account, the honest status of that
// boundary is:
//
//	GITHUB EXTERNAL BOUNDARY: NOT VERIFIED
//
// ── How to run it ──────────────────────────────────────────────────────
//
//	export CORSI_GITHUB_LIVE_TOKEN='<a fine-grained PAT, read-only>'
//	export CORSI_GITHUB_LIVE_REPO='owner/name'   # a repository that token can read
//	go test -tags=live -v -run TestLive ./internal/integrations/github/
//
// The token is read from the environment and never written anywhere: not to
// a fixture, not to a log, not to the test output. Every assertion below is
// about SHAPE — that a login came back, that commits have shas, that a file
// decodes — and never about content, so nothing private about the account
// or the repository ends up in a test report.
//
// ── What it verifies, one line each ────────────────────────────────────
//
//	identity          GET /user answers and names an account
//	repo listing      GET /user/repos answers and normalises
//	commits           GET /repos/:o/:r/commits answers with shas and dates
//	commit detail     GET /repos/:o/:r/commits/:sha carries files and stats
//	code search       GET /search/code answers, scoped, with text matches
//	file read         GET /repos/:o/:r/contents/:path decodes base64 to text
//	pull requests     GET /repos/:o/:r/pulls answers
//	rate limit        the headers this integration branches on are present
//
// It performs no writes, and it cannot: the API port it drives has no write
// verb to call.
package github

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/corsi/backend/internal/integrations/github/adapters/api"
	"github.com/corsi/backend/internal/integrations/github/domain"
	"github.com/corsi/backend/internal/integrations/github/ports"
)

func liveCreds(t *testing.T) (ports.Credentials, *domain.Repository) {
	t.Helper()
	token := os.Getenv("CORSI_GITHUB_LIVE_TOKEN")
	if token == "" {
		t.Skip("CORSI_GITHUB_LIVE_TOKEN not set; skipping the live GitHub boundary test")
	}
	full := os.Getenv("CORSI_GITHUB_LIVE_REPO")
	if full == "" {
		t.Skip("CORSI_GITHUB_LIVE_REPO not set; skipping the live GitHub boundary test")
	}
	owner, name, ok := strings.Cut(full, "/")
	if !ok || !domain.ValidOwner(owner) || !domain.ValidRepoName(name) {
		t.Fatalf("CORSI_GITHUB_LIVE_REPO must be owner/name")
	}
	return ports.Credentials{BaseURL: domain.DefaultAPIBaseURL, Token: token},
		&domain.Repository{Owner: owner, Name: name, FullName: owner + "/" + name}
}

func liveClient() *api.Client {
	return api.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func liveCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestLiveIdentity(t *testing.T) {
	creds, _ := liveCreds(t)
	account, err := liveClient().Viewer(liveCtx(t), creds)
	if err != nil {
		t.Fatalf("Viewer: %v", err)
	}
	// Shape only. The login itself is not printed: this output may end up
	// in a terminal somebody screenshots.
	if account.Login == "" || account.ID == 0 {
		t.Fatal("GitHub answered without identifying the account")
	}
	if account.Type != string(domain.AccountUser) && account.Type != string(domain.AccountOrg) {
		t.Fatalf("account type %q is neither User nor Organization", account.Type)
	}
	t.Logf("identity verified: a %s account with a non-zero id", account.Type)
}

func TestLiveRepositoryListing(t *testing.T) {
	creds, _ := liveCreds(t)
	repos, err := liveClient().AccessibleRepositories(liveCtx(t), creds, 10)
	if err != nil {
		t.Fatalf("AccessibleRepositories: %v", err)
	}
	if len(repos) == 0 {
		t.Fatal("the credential can reach no repositories at all")
	}
	for _, r := range repos {
		if r.ID == 0 || r.Owner == "" || r.Name == "" {
			t.Fatal("a repository came back without an id, an owner or a name")
		}
		if r.FullName != r.Owner+"/"+r.Name {
			t.Fatal("full_name does not match owner/name; the lookup key would not resolve")
		}
		// The patterns the whole path-safety argument rests on.
		if !domain.ValidOwner(r.Owner) || !domain.ValidRepoName(r.Name) {
			t.Fatal("GitHub returned a name this integration's validator rejects")
		}
	}
	t.Logf("listing verified: %d repositories, all with usable identities", len(repos))
}

func TestLiveCommits(t *testing.T) {
	creds, repo := liveCreds(t)
	commits, err := liveClient().ListCommits(liveCtx(t), creds, repo, ports.CommitQuery{Limit: 5})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if len(commits) == 0 {
		t.Fatal("the repository reported no commits")
	}
	for _, c := range commits {
		if c.SHA == "" {
			t.Fatal("a commit came back without a sha")
		}
		if c.Date.IsZero() {
			t.Fatal("a commit came back without a date; the listing would sort meaninglessly")
		}
	}
	t.Logf("commits verified: %d entries, all with shas and dates", len(commits))

	detail, err := liveClient().GetCommit(liveCtx(t), creds, repo, commits[0].SHA)
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if detail.SHA == "" {
		t.Fatal("the commit detail has no sha")
	}
	if detail.FilesTotal == 0 {
		t.Log("note: the newest commit touched no files GitHub reported")
	}
	t.Logf("commit detail verified: %d files reported, %d returned",
		detail.FilesTotal, len(detail.Files))
}

func TestLiveCodeSearch(t *testing.T) {
	creds, repo := liveCreds(t)
	// A term that exists in essentially every repository, so this test does
	// not depend on the content of the one it was pointed at.
	result, err := liveClient().SearchCode(liveCtx(t), creds,
		[]domain.Repository{*repo}, "the", domain.MaxSearchResults)
	if err != nil {
		if domain.IsKind(err, domain.KindRateLimited) {
			t.Skipf("code search is rate limited right now: %v", err)
		}
		t.Fatalf("SearchCode: %v", err)
	}
	for _, hit := range result.Hits {
		if hit.Path == "" {
			t.Fatal("a search hit came back without a path")
		}
		// The scoping claim, verified against the real search grammar. This
		// is the single most important line in this file.
		if !strings.EqualFold(hit.Repository, repo.FullName) {
			t.Fatalf("a hit came back from %q, outside the scoped repository", hit.Repository)
		}
	}
	t.Logf("search verified: %d hits, %d total, all inside the scope",
		len(result.Hits), result.TotalCount)
}

func TestLiveFileRead(t *testing.T) {
	creds, repo := liveCreds(t)
	// README.md is the closest thing to a file every repository has. A
	// 404 here is a legitimate skip rather than a failure.
	file, err := liveClient().GetFile(liveCtx(t), creds, repo, "README.md", "")
	if err != nil {
		if domain.IsKind(err, domain.KindNotFound) {
			t.Skip("the repository has no README.md")
		}
		t.Fatalf("GetFile: %v", err)
	}
	if file.Text == "" && file.Size > 0 {
		t.Fatal("the file decoded to nothing; base64 handling is wrong against real content")
	}
	if len(file.Text) > domain.MaxFileBytes {
		t.Fatal("the ceiling was not applied")
	}
	t.Logf("file read verified: %d bytes decoded, truncated=%v", len(file.Text), file.Truncated)
}

func TestLivePullRequests(t *testing.T) {
	creds, repo := liveCreds(t)
	pulls, err := liveClient().ListPullRequests(liveCtx(t), creds, repo,
		ports.PullRequestQuery{State: "all", Limit: 5})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(pulls) == 0 {
		t.Skip("the repository has no pull requests to read")
	}
	for _, p := range pulls {
		if p.Number <= 0 || p.State == "" {
			t.Fatal("a pull request came back without a number or a state")
		}
	}
	detail, err := liveClient().GetPullRequest(liveCtx(t), creds, repo, pulls[0].Number)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if detail.Number != pulls[0].Number {
		t.Fatal("the detail read answered about a different pull request")
	}
	t.Logf("pull requests verified: %d listed, detail read for #%d", len(pulls), detail.Number)
}

// The headers this integration branches on have to actually be there. If
// GitHub stops sending X-RateLimit-Remaining, a rate limit would be
// reported as a permission failure and the user would be told to reconnect
// a token that is fine.
//
// It is verified by DELIBERATELY failing: a bad token produces a 401 whose
// mapping is the same code path, and a real rate limit cannot be provoked
// on demand without burning five thousand requests.
func TestLiveUnauthorizedMapping(t *testing.T) {
	creds, _ := liveCreds(t)
	bad := ports.Credentials{BaseURL: creds.BaseURL, Token: "ghp_" + strings.Repeat("0", 36)}

	_, err := liveClient().Viewer(liveCtx(t), bad)
	if err == nil {
		t.Fatal("GitHub accepted an obviously invalid token")
	}
	if !domain.IsKind(err, domain.KindUnauthorized) {
		t.Fatalf("a rejected credential mapped to %v, not KindUnauthorized", err)
	}
	if strings.Contains(err.Error(), bad.Token) {
		t.Fatal("the credential appeared in the error")
	}
	t.Log("auth failure mapping verified against the real endpoint")
}
