package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/corsi/backend/internal/integrations/github/domain"
	"github.com/corsi/backend/internal/integrations/github/ports"
)

// The GitHub HTTP boundary, tested against a fake server rather than
// against GitHub.
//
// ── What this file is for, and what it deliberately is not ─────────────
// It exercises the REAL transport: the URL that gets built, the method, the
// headers, pagination, status codes, JSON decoding, base64 decoding, and
// every error the boundary has to tell apart. The fake speaks HTTP; nothing
// here mocks the client's own methods, because a test that stubs the thing
// under test only proves that our abstraction calls our abstraction.
//
// What it cannot prove is that GitHub actually behaves this way. That is
// the external boundary, it is declared unverified, and the shape of these
// fixtures came from GitHub's published API reference rather than from a
// captured live response — which is also why no private repository data is
// in this file.

const testToken = "ghp_fixture_not_a_real_token"

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// capture records what the fake server was actually asked for. Every
// assertion about "we sent the right request" reads this rather than
// trusting the client's own account of itself.
type capture struct {
	method  string
	path    string
	rawURL  string
	query   url.Values
	headers http.Header
	calls   int
}

// fakeGitHub serves one handler and records the request.
func fakeGitHub(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *capture) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.calls++
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.rawURL = r.URL.String()
		cap.query = r.URL.Query()
		cap.headers = r.Header.Clone()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func creds(srv *httptest.Server) ports.Credentials {
	return ports.Credentials{BaseURL: srv.URL, Token: testToken}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
}

// testRepo is an authorized row, as the database would have produced it.
// Every method under test takes one of these, which is the type-level
// reason a model-supplied string can never reach a URL.
func testRepo() *domain.Repository {
	return &domain.Repository{
		Owner: "acme", Name: "website", FullName: "acme/website", DefaultBranch: "main",
	}
}

/* ── the request itself ──────────────────────────────────────────────── */

func TestEveryRequestCarriesTheCredentialInOneHeaderAndNowhereElse(t *testing.T) {
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, wireUser{Login: "joao", ID: 7, Type: "User"})
	})

	if _, err := New(testLogger()).Viewer(context.Background(), creds(srv)); err != nil {
		t.Fatalf("Viewer: %v", err)
	}

	if got := cap.headers.Get("Authorization"); got != "Bearer "+testToken {
		t.Fatalf("Authorization = %q, want the bearer token", got)
	}
	// The property that matters more than the one above: it is in the
	// header and in NO other part of the request. A token in a query string
	// lands in every proxy log between here and GitHub.
	if strings.Contains(cap.rawURL, testToken) {
		t.Fatal("the token appeared in the request URL")
	}
	for name, values := range cap.headers {
		if strings.EqualFold(name, "Authorization") {
			continue
		}
		for _, v := range values {
			if strings.Contains(v, testToken) {
				t.Fatalf("the token appeared in header %s", name)
			}
		}
	}
}

func TestRequestsAnnounceTheApiVersionAndAcceptJSON(t *testing.T) {
	// Not decoration: GitHub's REST API is versioned by header, and a
	// request without it is served by whatever the default happens to be on
	// the day it runs.
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, wireUser{Login: "joao", ID: 7, Type: "User"})
	})
	if _, err := New(testLogger()).Viewer(context.Background(), creds(srv)); err != nil {
		t.Fatalf("Viewer: %v", err)
	}
	if got := cap.headers.Get("X-GitHub-Api-Version"); got != apiVersion {
		t.Fatalf("X-GitHub-Api-Version = %q, want %q", got, apiVersion)
	}
	if got := cap.headers.Get("Accept"); got != acceptJSON {
		t.Fatalf("Accept = %q, want %q", got, acceptJSON)
	}
	if cap.method != http.MethodGet {
		t.Fatalf("method = %s, want GET — this integration is read-only", cap.method)
	}
}

func TestEveryMethodUsesGET(t *testing.T) {
	// The read-only claim, asserted at the transport rather than by reading
	// the code. If a write ever appears, this fails before it ships.
	methods := map[string]func(c *Client, cr ports.Credentials) error{
		"Viewer": func(c *Client, cr ports.Credentials) error {
			_, err := c.Viewer(context.Background(), cr)
			return err
		},
		"AccessibleRepositories": func(c *Client, cr ports.Credentials) error {
			_, err := c.AccessibleRepositories(context.Background(), cr, 10)
			return err
		},
		"ListCommits": func(c *Client, cr ports.Credentials) error {
			_, err := c.ListCommits(context.Background(), cr, testRepo(), ports.CommitQuery{})
			return err
		},
		"GetCommit": func(c *Client, cr ports.Credentials) error {
			_, err := c.GetCommit(context.Background(), cr, testRepo(), "abc123")
			return err
		},
		"SearchCode": func(c *Client, cr ports.Credentials) error {
			_, err := c.SearchCode(context.Background(), cr,
				[]domain.Repository{*testRepo()}, "budget", 5)
			return err
		},
		"GetFile": func(c *Client, cr ports.Credentials) error {
			_, err := c.GetFile(context.Background(), cr, testRepo(), "README.md", "")
			return err
		},
		"ListPullRequests": func(c *Client, cr ports.Credentials) error {
			_, err := c.ListPullRequests(context.Background(), cr, testRepo(), ports.PullRequestQuery{})
			return err
		},
		"GetPullRequest": func(c *Client, cr ports.Credentials) error {
			_, err := c.GetPullRequest(context.Background(), cr, testRepo(), 1)
			return err
		},
	}
	for name, call := range methods {
		t.Run(name, func(t *testing.T) {
			srv, cap := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				// Enough of a body for each decoder; the assertion is on the
				// method, so correctness of the payload does not matter here.
				switch {
				case strings.HasPrefix(r.URL.Path, "/repos/") && strings.Contains(r.URL.Path, "/contents/"):
					writeJSON(t, w, wireContent{
						Type: "file", Path: "README.md", Encoding: "base64",
						Content: base64.StdEncoding.EncodeToString([]byte("hi")), Size: 2,
					})
				case strings.Contains(r.URL.Path, "/pulls/"):
					writeJSON(t, w, wirePull{Number: 1, Title: "t", State: "open"})
				case strings.HasSuffix(r.URL.Path, "/pulls"):
					writeJSON(t, w, []wirePull{})
				case strings.Contains(r.URL.Path, "/commits/"):
					writeJSON(t, w, wireCommit{SHA: "abc123"})
				case strings.HasSuffix(r.URL.Path, "/commits"), strings.HasSuffix(r.URL.Path, "/repos"):
					_, _ = w.Write([]byte("[]"))
				case r.URL.Path == "/search/code":
					writeJSON(t, w, wireSearchCode{})
				default:
					writeJSON(t, w, wireUser{Login: "j", ID: 1, Type: "User"})
				}
			})
			if err := call(New(testLogger()), creds(srv)); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if cap.method != http.MethodGet {
				t.Fatalf("%s used %s; every GitHub call in this build must be a GET", name, cap.method)
			}
		})
	}
}

func TestARedirectIsRefusedRatherThanFollowed(t *testing.T) {
	// The failure this prevents: Go's default policy follows a redirect and
	// re-sends the Authorization header to whatever host the response names.
	// A single misconfigured or hostile endpoint would be handed the token.
	//
	// The fake redirects to a second server that records what it receives.
	// If the client ever starts following redirects, `leaked` flips.
	leaked := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked = true
		}
		writeJSON(t, w, wireUser{Login: "attacker", ID: 1, Type: "User"})
	}))
	defer elsewhere.Close()

	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, elsewhere.URL+"/user", http.StatusFound)
	})

	_, err := New(testLogger()).Viewer(context.Background(), creds(srv))
	if leaked {
		t.Fatal("the credential was sent to a redirect target")
	}
	if err == nil {
		t.Fatal("a redirect was followed and reported as success")
	}
}

func TestCancellationStopsARequest(t *testing.T) {
	// The user pressing stop must reach the NETWORK, not just the loop
	// above it. The handler blocks; the context is cancelled; the in-flight
	// request has to abort.
	//
	// ── Why the elapsed time is the real assertion ─────────────────────
	// A mutation test found this. Asserting only on the error message
	// passes even when the request is issued with a detached context: the
	// caller's context is still checked after the fact, so the right error
	// comes back — eight seconds later, after the client's own timeout,
	// with a connection held open the whole time. The message was right for
	// the wrong reason.
	//
	// Returning promptly is the property. It is what makes "stop" mean stop
	// rather than "stop reporting".
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := New(testLogger()).Viewer(ctx, creds(srv))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a cancelled request reported success")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("err = %v, want the context's own cancellation", err)
	}
	// Generous against the 8 second client timeout, tight enough that a
	// request which ran to that timeout cannot pass.
	if elapsed > 2*time.Second {
		t.Fatalf("the call took %v to come back; the cancellation did not reach the "+
			"in-flight request, it was only reported afterwards", elapsed)
	}
}

/* ── error mapping ───────────────────────────────────────────────────── */

func TestUnauthorizedIsToldApartAndIsActionable(t *testing.T) {
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	})

	_, err := New(testLogger()).Viewer(context.Background(), creds(srv))
	if !domain.IsKind(err, domain.KindUnauthorized) {
		t.Fatalf("401 produced %v, want KindUnauthorized", err)
	}
	// Actionable, per the brief: it has to say what to do.
	if !strings.Contains(err.Error(), "Reconnect") {
		t.Fatalf("the 401 message does not say what to do: %q", err)
	}
	// And it must never quote what was sent.
	if strings.Contains(err.Error(), testToken) {
		t.Fatal("the credential appeared in the error message")
	}
}

func TestPrimaryRateLimitIsDistinguishableFromAPermissionRefusal(t *testing.T) {
	// Both are 403. Collapsing them would make "wait a minute" and
	// "reconnect with more permissions" the same sentence, and neither
	// would be right.
	t.Run("403 with no budget left is a rate limit", func(t *testing.T) {
		reset := time.Now().Add(90 * time.Second).Unix()
		srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
			w.Header().Set("X-RateLimit-Resource", "core")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		})
		_, err := New(testLogger()).Viewer(context.Background(), creds(srv))
		if !domain.IsKind(err, domain.KindRateLimited) {
			t.Fatalf("err = %v, want KindRateLimited", err)
		}
		if !strings.Contains(err.Error(), "seconds") {
			t.Fatalf("the rate limit message does not say when it clears: %q", err)
		}
	})

	t.Run("403 with budget left is a permission problem", func(t *testing.T) {
		srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "4998")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
		})
		_, err := New(testLogger()).Viewer(context.Background(), creds(srv))
		if !domain.IsKind(err, domain.KindUnauthorized) {
			t.Fatalf("err = %v, want KindUnauthorized", err)
		}
		if domain.IsKind(err, domain.KindRateLimited) {
			t.Fatal("a permission refusal was reported as a rate limit")
		}
		// GitHub's own sentence names the fix, so it is carried through.
		if !strings.Contains(err.Error(), "not accessible by personal access token") {
			t.Fatalf("GitHub's actionable message was dropped: %q", err)
		}
	})

	t.Run("429 is a rate limit whatever the counters say", func(t *testing.T) {
		srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		})
		_, err := New(testLogger()).Viewer(context.Background(), creds(srv))
		if !domain.IsKind(err, domain.KindRateLimited) {
			t.Fatalf("err = %v, want KindRateLimited", err)
		}
	})
}

func TestSearchRateLimitNamesTheSearchBudget(t *testing.T) {
	// Ten per minute against five thousand per hour. A user told only
	// "rate limited" would wait an hour for something that clears in
	// seconds.
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Resource", "code_search")
		w.WriteHeader(http.StatusForbidden)
	})
	_, err := New(testLogger()).SearchCode(context.Background(), creds(srv),
		[]domain.Repository{*testRepo()}, "budget", 5)
	if !domain.IsKind(err, domain.KindRateLimited) {
		t.Fatalf("err = %v, want KindRateLimited", err)
	}
	if !strings.Contains(err.Error(), "search rate limit") {
		t.Fatalf("the search budget was not named: %q", err)
	}
}

func TestNotFoundDoesNotDistinguishAbsentFromInvisible(t *testing.T) {
	// GitHub answers 404 both for "there is no such thing" and for "you may
	// not see it". Reporting them differently would turn this integration
	// into a probe for the existence of private repositories.
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	})
	_, err := New(testLogger()).GetCommit(context.Background(), creds(srv), testRepo(), "deadbeef")
	if !domain.IsKind(err, domain.KindNotFound) {
		t.Fatalf("err = %v, want KindNotFound", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "exist") &&
		!strings.Contains(err.Error(), "cannot see it") {
		t.Fatalf("the message asserts existence: %q", err)
	}
}

func TestAServerErrorIsUpstreamAndNotOurFault(t *testing.T) {
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	_, err := New(testLogger()).Viewer(context.Background(), creds(srv))
	if !domain.IsKind(err, domain.KindUpstream) {
		t.Fatalf("err = %v, want KindUpstream", err)
	}
}

func TestAnUnreadableBodyIsAFailureAndNeverAnEmptyResult(t *testing.T) {
	// The specific bug this prevents: a 200 whose body is an HTML error
	// page decoding into a zero-valued struct, which every layer above
	// would then report as "no commits".
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>maintenance</html>"))
	})
	_, err := New(testLogger()).ListCommits(context.Background(), creds(srv), testRepo(), ports.CommitQuery{})
	if err == nil {
		t.Fatal("an HTML body was accepted as an empty commit list")
	}
	if !domain.IsKind(err, domain.KindUpstream) {
		t.Fatalf("err = %v, want KindUpstream", err)
	}
}

func TestAViewerWithoutAnIdentityIsRefused(t *testing.T) {
	// A 200 with an empty body would otherwise store a connection whose
	// account is blank, and the card would render an account nobody owns.
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	_, err := New(testLogger()).Viewer(context.Background(), creds(srv))
	if err == nil {
		t.Fatal("a response with no login was accepted as an identity")
	}
}

/* ── pagination ──────────────────────────────────────────────────────── */

func TestRepositoryListingPaginatesAndStopsAtTheCeiling(t *testing.T) {
	// Two properties in one test because they are one behaviour: it keeps
	// asking while there is more, and it stops when it has enough. The
	// second is what prevents a token with thousands of repositories from
	// spending the whole rate budget on one page load.
	const totalPages = 4
	pagesSeen := map[string]bool{}
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pagesSeen[page] = true
		n, _ := strconv.Atoi(page)
		batch := make([]wireRepo, 0, domain.PageSize)
		for i := 0; i < domain.PageSize; i++ {
			id := int64((n-1)*domain.PageSize + i + 1)
			var repo wireRepo
			repo.ID = id
			repo.Owner.Login = "acme"
			repo.Owner.Type = "Organization"
			repo.Name = "repo" + strconv.FormatInt(id, 10)
			repo.FullName = "acme/" + repo.Name
			batch = append(batch, repo)
		}
		if n >= totalPages {
			batch = batch[:1] // short page ends the walk
		}
		writeJSON(t, w, batch)
	})

	got, err := New(testLogger()).AccessibleRepositories(context.Background(), creds(srv), 250)
	if err != nil {
		t.Fatalf("AccessibleRepositories: %v", err)
	}
	if len(got) != 250 {
		t.Fatalf("got %d repositories, want the requested ceiling of 250", len(got))
	}
	if pagesSeen["4"] {
		t.Fatal("kept paginating after the ceiling was reached")
	}
	if !pagesSeen["3"] {
		t.Fatal("did not paginate past the first page")
	}
	if got[0].OwnerType != string(domain.AccountOrg) {
		t.Fatalf("owner type = %q, want the normalized Organization", got[0].OwnerType)
	}
}

func TestRepositoryListingStopsOnAShortPage(t *testing.T) {
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		var repo wireRepo
		repo.ID = 1
		repo.Owner.Login = "acme"
		repo.Name = "website"
		repo.FullName = "acme/website"
		writeJSON(t, w, []wireRepo{repo})
	})
	got, err := New(testLogger()).AccessibleRepositories(context.Background(), creds(srv), 300)
	if err != nil {
		t.Fatalf("AccessibleRepositories: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	// A page shorter than per_page means there is no more. Asking again
	// would be one wasted request per page load, forever.
	if cap.calls != 1 {
		t.Fatalf("made %d requests for a single short page", cap.calls)
	}
}

/* ── the path is built from stored columns ───────────────────────────── */

func TestTheRequestPathComesFromTheStoredRowAndNotFromAnyInput(t *testing.T) {
	// The core traversal defence, asserted where it is observable: whatever
	// a caller may have typed, the URL is built from the row's own columns.
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	repo := &domain.Repository{Owner: "acme", Name: "website", FullName: "acme/website"}

	if _, err := New(testLogger()).ListCommits(context.Background(), creds(srv), repo, ports.CommitQuery{}); err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if cap.path != "/repos/acme/website/commits" {
		t.Fatalf("path = %q, want /repos/acme/website/commits", cap.path)
	}
}

func TestFilePathRefusesDotDotSegments(t *testing.T) {
	// Escaping alone would neutralise a traversal of OUR url structure, but
	// GitHub resolving `..` inside a repository is not a defence we control.
	// So it is refused, and refused before any request is made.
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a traversal path reached the network")
	})
	for _, bad := range []string{"../secrets", "src/../../etc/passwd", "a/./b", "/", ""} {
		_, err := New(testLogger()).GetFile(context.Background(), creds(srv), testRepo(), bad, "")
		if err == nil {
			t.Fatalf("path %q was accepted", bad)
		}
		if !domain.IsKind(err, domain.KindInvalid) {
			t.Fatalf("path %q produced %v, want KindInvalid", bad, err)
		}
	}
	if cap.calls != 0 {
		t.Fatalf("%d requests were made for paths that should never leave the process", cap.calls)
	}
}

func TestFilePathSegmentsAreEscapedButSeparatorsSurvive(t *testing.T) {
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, wireContent{
			Type: "file", Path: "a b/c.go", Encoding: "base64",
			Content: base64.StdEncoding.EncodeToString([]byte("package main")), Size: 12,
		})
	})
	if _, err := New(testLogger()).GetFile(context.Background(), creds(srv), testRepo(), "a b/c.go", ""); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	// The separator is a separator; the space inside a segment is escaped.
	if cap.path != "/repos/acme/website/contents/a b/c.go" {
		t.Fatalf("path = %q", cap.path)
	}
	if !strings.Contains(cap.rawURL, "a%20b/c.go") {
		t.Fatalf("raw url = %q, want the space escaped inside the segment", cap.rawURL)
	}
}

/* ── file reading ────────────────────────────────────────────────────── */

func TestFileContentIsDecodedAndBoundedWithTruncationDeclared(t *testing.T) {
	big := strings.Repeat("x", domain.MaxFileBytes+5000)
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, wireContent{
			Type: "file", Path: "big.txt", Encoding: "base64", Size: len(big),
			// GitHub wraps base64 at 60 characters, which the standard
			// decoder rejects. Reproduced here because it is exactly the
			// detail a hand-rolled fixture would omit and production would
			// then hit.
			Content: wrap(base64.StdEncoding.EncodeToString([]byte(big)), 60),
		})
	})
	got, err := New(testLogger()).GetFile(context.Background(), creds(srv), testRepo(), "big.txt", "")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if len(got.Text) > domain.MaxFileBytes {
		t.Fatalf("returned %d bytes, above the %d ceiling", len(got.Text), domain.MaxFileBytes)
	}
	if !got.Truncated {
		t.Fatal("the text was cut and the result does not say so")
	}
	if got.Size != len(big) {
		t.Fatalf("Size = %d, want the file's real size %d", got.Size, len(big))
	}
}

func wrap(s string, n int) string {
	var b strings.Builder
	for i := 0; i < len(s); i += n {
		end := min(i+n, len(s))
		b.WriteString(s[i:end])
		b.WriteString("\n")
	}
	return b.String()
}

func TestBinaryAndOversizeAndDirectoryFilesAreRefusedRatherThanReturnedEmpty(t *testing.T) {
	cases := map[string]wireContent{
		"binary": {
			Type: "file", Path: "logo.png", Encoding: "base64", Size: 4,
			Content: base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0xff}),
		},
		// Above 1 MiB GitHub answers with an empty content field and
		// encoding "none". Returning that as an empty file would be a lie
		// the model would answer confidently.
		"too large": {Type: "file", Path: "huge.bin", Encoding: "none", Size: 2 << 20},
		"directory": {Type: "dir", Path: "src"},
	}
	for name, fixture := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, fixture)
			})
			got, err := New(testLogger()).GetFile(context.Background(), creds(srv), testRepo(), "x", "")
			if err == nil {
				t.Fatalf("accepted %s and returned %+v", name, got)
			}
			if !domain.IsKind(err, domain.KindInvalid) {
				t.Fatalf("err = %v, want KindInvalid", err)
			}
		})
	}
}

func TestRefTravelsAsAQueryParameter(t *testing.T) {
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, wireContent{
			Type: "file", Path: "a.go", Encoding: "base64",
			Content: base64.StdEncoding.EncodeToString([]byte("x")), Size: 1,
		})
	})
	if _, err := New(testLogger()).GetFile(context.Background(), creds(srv), testRepo(), "a.go", "release/1.0"); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got := cap.query.Get("ref"); got != "release/1.0" {
		t.Fatalf("ref = %q, want release/1.0", got)
	}
}

/* ── commits ─────────────────────────────────────────────────────────── */

func TestCommitDiffIsBoundedOnFilesAndOnPatchVolume(t *testing.T) {
	var fixture wireCommit
	fixture.SHA = "abc123"
	fixture.Commit.Message = "big refactor"
	fixture.Stats.Additions = 9000
	for i := 0; i < domain.MaxCommitFiles+40; i++ {
		fixture.Files = append(fixture.Files, struct {
			Filename  string `json:"filename"`
			Status    string `json:"status"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
			Patch     string `json:"patch"`
		}{
			Filename: "f" + strconv.Itoa(i) + ".go",
			Status:   "modified",
			Patch:    strings.Repeat("+line\n", 400),
		})
	}
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, fixture)
	})

	got, err := New(testLogger()).GetCommit(context.Background(), creds(srv), testRepo(), "abc123")
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if len(got.Files) > domain.MaxCommitFiles {
		t.Fatalf("returned %d files, above the %d ceiling", len(got.Files), domain.MaxCommitFiles)
	}
	// The count of what was NOT shown has to survive the cut, or the model
	// reads a partial diff as a complete one.
	if got.FilesTotal != len(fixture.Files) {
		t.Fatalf("FilesTotal = %d, want the real %d", got.FilesTotal, len(fixture.Files))
	}
	total := 0
	for _, f := range got.Files {
		total += len(f.Patch)
	}
	if total > domain.MaxCommitPatchByte {
		t.Fatalf("patch volume %d, above the %d budget", total, domain.MaxCommitPatchByte)
	}
	// A patch is included whole or not at all: half a hunk reads as the
	// complete diff of a smaller change.
	for _, f := range got.Files {
		if f.Patch != "" && !strings.HasSuffix(f.Patch, "\n") {
			t.Fatalf("file %s carries a patch that was cut mid-hunk", f.Path)
		}
	}
}

func TestCommitMessagesAreBoundedInAListing(t *testing.T) {
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		var c wireCommit
		c.SHA = "abc"
		c.Commit.Message = strings.Repeat("m", domain.MaxCommitMessage*3)
		c.Commit.Author.Date = time.Now()
		writeJSON(t, w, []wireCommit{c})
	})
	got, err := New(testLogger()).ListCommits(context.Background(), creds(srv), testRepo(), ports.CommitQuery{})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if len([]rune(got[0].Message)) > domain.MaxCommitMessage+1 {
		t.Fatalf("commit message survived at %d characters", len(got[0].Message))
	}
}

func TestCommitQueryTranslatesToGitHubsParameterNames(t *testing.T) {
	// `ref` is `sha` on the wire. Getting this wrong silently returns the
	// default branch's history for every branch anyone asks about.
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	_, err := New(testLogger()).ListCommits(context.Background(), creds(srv), testRepo(),
		ports.CommitQuery{Ref: "develop", Path: "internal/chat", Limit: 7})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if got := cap.query.Get("sha"); got != "develop" {
		t.Fatalf("sha = %q, want develop", got)
	}
	if got := cap.query.Get("path"); got != "internal/chat" {
		t.Fatalf("path = %q", got)
	}
	if got := cap.query.Get("per_page"); got != "7" {
		t.Fatalf("per_page = %q, want 7", got)
	}
}

func TestCommitLimitIsClampedToTheCeiling(t *testing.T) {
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	_, err := New(testLogger()).ListCommits(context.Background(), creds(srv), testRepo(),
		ports.CommitQuery{Limit: 5000})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	got, _ := strconv.Atoi(cap.query.Get("per_page"))
	if got > domain.MaxCommitsPerCall {
		t.Fatalf("per_page = %d, above the %d ceiling", got, domain.MaxCommitsPerCall)
	}
}

/* ── code search ─────────────────────────────────────────────────────── */

func TestSearchScopesToAuthorizedRepositoriesInTheQuery(t *testing.T) {
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, wireSearchCode{})
	})
	scope := []domain.Repository{
		{Owner: "acme", Name: "website", FullName: "acme/website"},
		{Owner: "acme", Name: "api", FullName: "acme/api"},
	}
	if _, err := New(testLogger()).SearchCode(context.Background(), creds(srv), scope, "budget", 5); err != nil {
		t.Fatalf("SearchCode: %v", err)
	}
	q := cap.query.Get("q")
	for _, want := range []string{"budget", "repo:acme/website", "repo:acme/api"} {
		if !strings.Contains(q, want) {
			t.Fatalf("query %q is missing %q", q, want)
		}
	}
}

func TestSearchDropsHitsOutsideTheAuthorizedSet(t *testing.T) {
	// The second gate, and the one that does not depend on being right
	// about GitHub's query grammar. If a hit for a repository we did not
	// scope to ever comes back — a GitHub bug, a qualifier that behaved
	// differently, a proxy — it is discarded rather than shown.
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		var payload wireSearchCode
		payload.TotalCount = 2
		payload.Items = append(payload.Items, struct {
			Path       string `json:"path"`
			HTMLURL    string `json:"html_url"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			TextMatches []struct {
				Fragment string `json:"fragment"`
			} `json:"text_matches"`
		}{Path: "ok.go"})
		payload.Items[0].Repository.FullName = "acme/website"
		payload.Items = append(payload.Items, payload.Items[0])
		payload.Items[1].Path = "secret.go"
		payload.Items[1].Repository.FullName = "someoneelse/private"
		writeJSON(t, w, payload)
	})

	got, err := New(testLogger()).SearchCode(context.Background(), creds(srv),
		[]domain.Repository{{Owner: "acme", Name: "website", FullName: "acme/website"}}, "x", 10)
	if err != nil {
		t.Fatalf("SearchCode: %v", err)
	}
	if len(got.Hits) != 1 {
		t.Fatalf("got %d hits, want the unauthorized one dropped", len(got.Hits))
	}
	if got.Hits[0].Repository != "acme/website" {
		t.Fatalf("kept a hit from %q", got.Hits[0].Repository)
	}
}

func TestSearchRequestsTextMatchesAndBoundsFragments(t *testing.T) {
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		var payload wireSearchCode
		payload.TotalCount = 1
		item := struct {
			Path       string `json:"path"`
			HTMLURL    string `json:"html_url"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			TextMatches []struct {
				Fragment string `json:"fragment"`
			} `json:"text_matches"`
		}{Path: "a.go"}
		item.Repository.FullName = "acme/website"
		for i := 0; i < domain.MaxSearchFragments+5; i++ {
			item.TextMatches = append(item.TextMatches, struct {
				Fragment string `json:"fragment"`
			}{Fragment: strings.Repeat("f", domain.MaxSearchFragmentBytes*2)})
		}
		payload.Items = append(payload.Items, item)
		writeJSON(t, w, payload)
	})

	got, err := New(testLogger()).SearchCode(context.Background(), creds(srv),
		[]domain.Repository{{Owner: "acme", Name: "website", FullName: "acme/website"}}, "f", 10)
	if err != nil {
		t.Fatalf("SearchCode: %v", err)
	}
	// Without text-match, a hit is a filename and the model has to open
	// every result to find out which one mattered.
	if !strings.Contains(cap.headers.Get("Accept"), "text-match") {
		t.Fatalf("Accept = %q, want the text-match media type", cap.headers.Get("Accept"))
	}
	if len(got.Hits[0].Fragments) > domain.MaxSearchFragments {
		t.Fatalf("kept %d fragments", len(got.Hits[0].Fragments))
	}
	for _, f := range got.Hits[0].Fragments {
		if len(f) > domain.MaxSearchFragmentBytes+4 {
			t.Fatalf("fragment of %d bytes survived", len(f))
		}
	}
}

func TestSearchWithNoAuthorizedRepositoriesAsksGitHubNothing(t *testing.T) {
	// An unscoped code search against a token that can see private code is
	// the exact query this integration must never send.
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a search with no scope reached GitHub")
	})
	got, err := New(testLogger()).SearchCode(context.Background(), creds(srv), nil, "budget", 10)
	if err != nil {
		t.Fatalf("SearchCode: %v", err)
	}
	if len(got.Hits) != 0 {
		t.Fatalf("got %d hits from an empty scope", len(got.Hits))
	}
	if cap.calls != 0 {
		t.Fatalf("made %d requests with nothing authorized", cap.calls)
	}
}

func TestSearchRefusesAnEmptyOrOversizeQuery(t *testing.T) {
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an invalid query reached GitHub")
	})
	scope := []domain.Repository{*testRepo()}
	for _, bad := range []string{"", "   ", strings.Repeat("q", domain.MaxSearchQueryLength+1)} {
		if _, err := New(testLogger()).SearchCode(context.Background(), creds(srv), scope, bad, 10); err == nil {
			t.Fatalf("query %q was accepted", bad)
		}
	}
	if cap.calls != 0 {
		t.Fatalf("made %d requests", cap.calls)
	}
}

/* ── pull requests ───────────────────────────────────────────────────── */

func TestPullRequestStateIsValidatedBeforeItReachesGitHub(t *testing.T) {
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	c := New(testLogger())
	if _, err := c.ListPullRequests(context.Background(), creds(srv), testRepo(),
		ports.PullRequestQuery{State: "merged-ish"}); err == nil {
		t.Fatal("an invalid state was accepted")
	}
	if cap.calls != 0 {
		t.Fatal("an invalid state reached GitHub")
	}
	if _, err := c.ListPullRequests(context.Background(), creds(srv), testRepo(),
		ports.PullRequestQuery{}); err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if got := cap.query.Get("state"); got != "open" {
		t.Fatalf("default state = %q, want open", got)
	}
}

func TestPullRequestBodyIsBounded(t *testing.T) {
	srv, _ := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, wirePull{
			Number: 12, Title: "t", State: "open",
			Body: strings.Repeat("b", domain.MaxPullRequestBody*3),
		})
	})
	got, err := New(testLogger()).GetPullRequest(context.Background(), creds(srv), testRepo(), 12)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if len(got.Body) > domain.MaxPullRequestBody+4 {
		t.Fatalf("body survived at %d bytes", len(got.Body))
	}
}

func TestPullRequestNumberMustBePositive(t *testing.T) {
	srv, cap := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a nonsense pull request number reached GitHub")
	})
	for _, n := range []int{0, -1} {
		if _, err := New(testLogger()).GetPullRequest(context.Background(), creds(srv), testRepo(), n); err == nil {
			t.Fatalf("number %d was accepted", n)
		}
	}
	if cap.calls != 0 {
		t.Fatal("a request was made")
	}
}
