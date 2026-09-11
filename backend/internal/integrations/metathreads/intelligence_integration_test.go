//go:build integration

package metathreads

import (
	"net/http"
	"strings"
	"testing"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	mttools "github.com/corsi/backend/internal/integrations/metathreads/tools"
	threadstools "github.com/corsi/backend/internal/threads/tools"
)

/* ── 5. own-history intelligence ─────────────────────────────────────── */

// "Veja meus últimos posts" — the read this whole sprint exists for.
func TestListReturnsTheRealPublishedHistory(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	out := e.execute(t, e.wsA, mttools.PostListTool, map[string]any{"limit": 3})
	rows, _ := out["posts"].([]any)
	if len(rows) != 3 {
		t.Fatalf("%d posts, want 3: %+v", len(rows), out)
	}
	first := rows[0].(map[string]any)
	if !strings.Contains(first["text"].(string), "AI agents") {
		t.Errorf("newest post = %+v", first)
	}
	if first["published_at"] == nil {
		t.Error("a post came back with no date; a history without dates answers no question")
	}
	if first["permalink"] == nil {
		t.Error("a post came back with no permalink")
	}
	// More exist, and the result says so rather than letting a model infer
	// completeness from a length.
	if out["has_more"] != true {
		t.Errorf("has_more = %v; three of six came back", out["has_more"])
	}
	if out["next_cursor"] == nil {
		t.Error("no cursor came back, so the history cannot be paged")
	}
}

// Paging works, and — the trap — the LAST page reports has_more false even
// though Meta still sends a cursor. A client that read the cursor alone
// would page forever.
func TestPagingReachesTheEndAndStops(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	seen := map[string]bool{}
	args := map[string]any{"limit": 2}
	for page := 0; page < 10; page++ {
		out := e.execute(t, e.wsA, mttools.PostListTool, args)
		for _, raw := range out["posts"].([]any) {
			seen[raw.(map[string]any)["id"].(string)] = true
		}
		if out["has_more"] != true {
			if page == 0 {
				t.Fatal("the first page of a six-post history reported no more")
			}
			break
		}
		cursor, _ := out["next_cursor"].(string)
		if cursor == "" {
			t.Fatal("has_more was true and no cursor came back")
		}
		args = map[string]any{"limit": 2, "after": cursor}
		if page == 9 {
			t.Fatal("paging never terminated")
		}
	}
	if len(seen) != 6 {
		t.Fatalf("paged %d distinct posts, want the whole history of 6", len(seen))
	}
}

// A small limit is the default. Every post carried back is prompt tokens,
// and a tool that dumped the archive by default would spend a turn's budget
// before the model decided what it was looking for.
func TestTheDefaultPageIsSmall(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.execute(t, e.wsA, mttools.PostListTool, map[string]any{})

	for _, r := range e.meta.seen() {
		if strings.Contains(r.Path, "/me/threads") {
			if got := r.Query.Get("limit"); got != "10" {
				t.Errorf("default limit sent to Meta = %q, want 10", got)
			}
			return
		}
	}
	t.Fatal("no listing call reached Meta")
}

// "Qual post performou melhor" — raw counts, no invented score.
func TestInsightsReportMetasCountsAndNoComputedScore(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	out := e.execute(t, e.wsA, mttools.PostInsightsTool, map[string]any{"post_id": "p_005"})
	m, _ := out["metrics"].(map[string]any)
	if m["views"] != float64(12000) || m["likes"] != float64(830) || m["replies"] != float64(41) {
		t.Fatalf("metrics = %+v", m)
	}
	// No engagement score, under any spelling. Interpretation is the agent's
	// job, and a score computed here would travel as though Meta reported it.
	for _, invented := range []string{"engagement", "score", "rate", "rank", "performance"} {
		for key := range m {
			if strings.Contains(strings.ToLower(key), invented) {
				t.Errorf("the tool invented a %q metric: %s", invented, key)
			}
		}
	}
}

// A metric Meta did not report is ABSENT and NAMED, never zero. A post that
// was never measured and a post nobody looked at are different facts.
func TestAnUnreportedMetricIsNamedAndNotZero(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	// p_000 is the repost facade: Meta returns an empty data array.
	out := e.execute(t, e.wsA, mttools.PostInsightsTool, map[string]any{"post_id": "p_000"})
	m, _ := out["metrics"].(map[string]any)

	if _, present := m["views"]; present {
		t.Errorf("a metric Meta never reported came back as a value: %+v", m)
	}
	unavailable, _ := m["unavailable"].([]any)
	if len(unavailable) == 0 {
		t.Fatalf("the absence was not reported: %+v", m)
	}
	note, _ := m["unavailable_note"].(string)
	if !strings.Contains(note, "not the same as zero") {
		t.Errorf("the note does not warn against reading absence as zero: %q", note)
	}
	// The seeded posts DO report metrics, so this is not just "nothing works".
	got := e.execute(t, e.wsA, mttools.PostInsightsTool, map[string]any{"post_id": "p_004"})
	if got["metrics"].(map[string]any)["likes"] != float64(210) {
		t.Fatalf("a measured post lost its metrics: %+v", got)
	}
}

// "Eu já falei sobre AI agents?" — the question search exists for.
func TestSearchAnswersWhetherATopicWasAlreadyCovered(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	out := e.execute(t, e.wsA, mttools.PostSearchTool, map[string]any{"query": "AI agents"})
	rows, _ := out["posts"].([]any)
	if len(rows) == 0 {
		t.Fatalf("search found nothing for a topic the archive covers: %+v", out)
	}
	if !strings.Contains(rows[0].(map[string]any)["text"].(string), "AI agents") {
		t.Errorf("search returned an unrelated post: %+v", rows[0])
	}
}

// EVERY search result carries the coverage caveat, because the endpoint
// serves two different corpora through one URL and the response cannot tell
// them apart. A model that read these as "what is trending" would report
// the user's own words back to them as a market signal.
func TestEverySearchResultDeclaresWhatItCouldNotSee(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	for _, args := range []map[string]any{
		{"query": "AI agents"},
		{"query": "nothing will match this"},
	} {
		out := e.execute(t, e.wsA, mttools.PostSearchTool, args)
		coverage, _ := out["coverage"].(string)
		if !strings.Contains(coverage, "app-review approval") {
			t.Errorf("a search result did not declare its coverage: %+v", out)
		}
		if !strings.Contains(coverage, "trending") {
			t.Errorf("the coverage note does not warn against reading this as trending: %q", coverage)
		}
	}
}

// An empty search must never read as proof of absence. Meta returns an
// empty array for terms it deems sensitive, which is indistinguishable from
// "never wrote about this".
func TestAnEmptySearchDoesNotClaimTheTopicWasNeverCovered(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	out := e.execute(t, e.wsA, mttools.PostSearchTool, map[string]any{"query": "quantum knitting"})
	note, _ := out["note"].(string)
	if !strings.Contains(note, "does NOT prove") {
		t.Fatalf("an empty search did not warn against concluding absence: %+v", out)
	}
}

// The author filter is passed through, which is how a search is narrowed to
// the operator's own history once the app can see more than that.
func TestSearchCanBeRestrictedToOneAuthor(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.execute(t, e.wsA, mttools.PostSearchTool, map[string]any{
		"query": "agents", "author": "joaocorsi", "rank": "recent", "mode": "keyword",
	})
	for _, r := range e.meta.seen() {
		if !strings.Contains(r.Path, "keyword_search") {
			continue
		}
		if r.Query.Get("author_username") != "joaocorsi" {
			t.Errorf("author_username = %q", r.Query.Get("author_username"))
		}
		// The model says "recent"; Meta is told "RECENT". The model is never
		// asked to spell an upstream enum.
		if r.Query.Get("search_type") != "RECENT" {
			t.Errorf("search_type = %q, want RECENT", r.Query.Get("search_type"))
		}
		if r.Query.Get("search_mode") != "KEYWORD" {
			t.Errorf("search_mode = %q, want KEYWORD", r.Query.Get("search_mode"))
		}
		return
	}
	t.Fatal("no search call reached Meta")
}

// A rank or mode the model invented is refused rather than passed upstream.
func TestAnInventedRankIsRefused(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	if _, err := e.executeErr(e.wsA, mttools.PostSearchTool,
		map[string]any{"query": "x", "rank": "viral"}); err == nil {
		t.Fatal("an invented rank was accepted")
	}
}

// Account insights answer "how is the account doing", and followers_count
// arrives in its own call because Meta refuses a window for it.
func TestAccountInsightsSplitTheWindowedCallFromFollowers(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)

	out := e.execute(t, e.wsA, mttools.ProfileInsightsTool, map[string]any{
		"since": "2026-08-01", "until": "2026-08-23",
	})
	if out["views"] != float64(48000) {
		t.Errorf("views = %v", out["views"])
	}
	if out["followers_count"] != float64(1240) {
		t.Errorf("followers_count = %v", out["followers_count"])
	}

	var windowed, followers int
	for _, r := range e.meta.seen() {
		if !strings.Contains(r.Path, "threads_insights") {
			continue
		}
		if r.Query.Get("metric") == "followers_count" {
			followers++
			if r.Query.Get("since") != "" || r.Query.Get("until") != "" {
				t.Error("followers_count was sent with a window Meta refuses")
			}
			continue
		}
		windowed++
		if r.Query.Get("since") == "" {
			t.Error("the windowed call lost its since")
		}
	}
	if windowed != 1 || followers != 1 {
		t.Errorf("windowed=%d followers=%d, want one of each", windowed, followers)
	}
}

// A `since` before Meta's floor is clamped rather than failed: a caller
// asking for "everything" gets everything Meta has.
func TestAnImpossibleSinceIsClampedNotRefused(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.execute(t, e.wsA, mttools.ProfileInsightsTool, map[string]any{"since": "2020-01-01"})
	for _, r := range e.meta.seen() {
		if strings.Contains(r.Path, "threads_insights") && r.Query.Get("metric") != "followers_count" {
			if r.Query.Get("since") != "1712991600" {
				t.Errorf("since = %q, want it clamped to Meta's floor", r.Query.Get("since"))
			}
			return
		}
	}
	t.Fatal("no windowed insights call reached Meta")
}

// A malformed date is refused rather than silently dropped: answering a
// different question and calling it an answer is worse than failing.
func TestAMalformedDateIsRefused(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	if _, err := e.executeErr(e.wsA, mttools.PostListTool,
		map[string]any{"since": "last tuesday"}); err == nil {
		t.Fatal("a date the tool could not parse was accepted and ignored")
	}
}

/* ── 6. failures are never empty results ─────────────────────────────── */

// THE most damaging bug this integration could have. A model told "no
// posts" when the truth is "the token expired" will tell the user they have
// never written about a topic they have covered ten times.
func TestAnUpstreamFailureIsNeverReportedAsAnEmptyHistory(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.meta.failOn("/me/threads", http.StatusBadGateway,
		`{"error":{"message":"Service temporarily unavailable","code":2}}`)

	out, err := e.executeErr(e.wsA, mttools.PostListTool, map[string]any{})
	if err == nil {
		t.Fatalf("an upstream outage produced a result instead of a failure: %+v", out)
	}
	var fail *chatdomain.ToolFailure
	if !errorsAs(err, &fail) {
		t.Fatalf("error = %v", err)
	}
	if fail.Code != chatdomain.ToolErrExecutionFailed {
		t.Errorf("code = %q, want an execution failure", fail.Code)
	}
	if !strings.Contains(fail.Message, "NOT an empty result") {
		t.Errorf("the failure does not warn against reading it as no posts: %q", fail.Message)
	}
}

// An expired token is reported as itself, so the answer is "reconnect" and
// not "you have no posts".
func TestAnExpiredTokenIsReportedAsExpired(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	e.meta.failOn("/me/threads", http.StatusUnauthorized,
		`{"error":{"message":"Error validating access token: Session has expired","code":190}}`)

	_, err := e.executeErr(e.wsA, mttools.PostListTool, map[string]any{})
	if err == nil {
		t.Fatal("an expired token produced a result")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error = %v, want it to name the expiry", err)
	}
	if !strings.Contains(err.Error(), "rather than answering from memory") {
		t.Errorf("the failure does not tell the model to report it: %v", err)
	}
}

// A workspace with no connection is told exactly that, with the next
// action, rather than being handed an empty archive to reason about.
func TestAnUnconnectedWorkspaceIsToldToConnect(t *testing.T) {
	e := newEnv(t)
	_, err := e.executeErr(e.wsA, mttools.PostListTool, map[string]any{})
	if err == nil {
		t.Fatal("an unconnected workspace produced a result")
	}
	if !strings.Contains(err.Error(), "no Meta Threads account connected") {
		t.Errorf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "do not guess") {
		t.Errorf("the failure does not warn the model against inventing a history: %v", err)
	}
}

// A capability the credential never received refuses before spending a
// round trip, and names the scope so the reconnect can fix it.
func TestAMissingScopeRefusesBeforeCallingMeta(t *testing.T) {
	e := newEnv(t)
	conn := e.connect(e.wsA)
	// Rewrite the stored grant to a basic-only one, which is what a person
	// who declined the optional permissions would have.
	if _, err := e.pool.Exec(ctxFor(e.wsA),
		`UPDATE meta_threads.connections SET scopes = ARRAY['threads_basic'] WHERE id = $1`,
		conn.ID); err != nil {
		t.Fatalf("narrow the scopes: %v", err)
	}

	before := len(e.meta.seen())
	_, err := e.executeErr(e.wsA, mttools.PostInsightsTool, map[string]any{"post_id": "p_005"})
	if err == nil {
		t.Fatal("insights were read without the scope")
	}
	if !strings.Contains(err.Error(), "threads_manage_insights") {
		t.Errorf("the refusal does not name the scope: %v", err)
	}
	if len(e.meta.seen()) != before {
		t.Error("a call that could not succeed was still sent to Meta")
	}
	// And a capability the credential DOES hold still works.
	if _, err := e.executeErr(e.wsA, mttools.PostListTool, map[string]any{"limit": 1}); err != nil {
		t.Fatalf("a basic read failed: %v", err)
	}
}

/* ── 7. reading changes nothing ──────────────────────────────────────── */

// Reading Meta must never write to the INTERNAL pipeline. Finding an
// interesting post is not the user asking for it to be saved.
func TestReadingMetaNeverCreatesAnInternalThread(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allMetaTools...)
	e.authorize(e.wsA, agentID, allThreadTools...)

	// Two calls, not three: the turn loop caps a turn at three provider
	// rounds, and a scripted fixture that ignored the ceiling would fail on
	// the runtime's own limit rather than on the property under test.
	e.llm.scriptCalls(
		chatdomain.ToolCall{Name: mttools.PostListTool, Arguments: `{"limit":5}`},
		chatdomain.ToolCall{Name: mttools.PostSearchTool, Arguments: `{"query":"AI agents"}`},
	)
	e.turn(e.wsA, conv, "analise meu histórico e encontre padrões")

	if n := len(e.liveThreads(e.wsA)); n != 0 {
		t.Fatalf("%d internal threads were created by a read-only analysis", n)
	}
}

// And analysing a post does not change the post: every call to Meta is a
// GET, except the one documented POST that redeems an authorization code.
func TestNothingButTheTokenExchangeEverUsesAWriteMethod(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	for _, tool := range allMetaTools {
		args := map[string]any{}
		switch tool {
		case mttools.PostGetTool, mttools.PostInsightsTool:
			args["post_id"] = "p_005"
		case mttools.PostSearchTool:
			args["query"] = "agents"
		}
		_, _ = e.executeErr(e.wsA, tool, args)
	}
	// The app secret may appear ONLY on the two token endpoints, which is
	// where Meta's own documentation puts it. No read may carry it, and no
	// read may carry the app id either: a credential on a data path is a
	// credential in somebody's proxy log.
	tokenEndpoints := map[string]bool{"/oauth/access_token": true, "/access_token": true}
	sawRead := false
	for _, r := range e.meta.seen() {
		if tokenEndpoints[r.Path] {
			continue
		}
		sawRead = true
		if r.Query.Get("client_secret") != "" {
			t.Errorf("%s carried the app secret; only the token endpoints may", r.Path)
		}
		if r.Query.Get("client_id") != "" {
			t.Errorf("%s carried the app id", r.Path)
		}
	}
	if !sawRead {
		t.Fatal("no read reached Meta, so this proved nothing")
	}
}

/* ── 8. audit ────────────────────────────────────────────────────────── */

// Every meta_threads call lands in the EXISTING audit trail, with what a
// later question needs — and with no secret in it.
func TestMetaThreadsCallsAreAuditedWithoutSecrets(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allMetaTools...)

	e.llm.scriptCalls(
		chatdomain.ToolCall{Name: mttools.PostListTool, Arguments: `{"limit":3}`},
		chatdomain.ToolCall{Name: mttools.PostInsightsTool, Arguments: `{"post_id":"p_005"}`},
	)
	e.turn(e.wsA, conv, "quais dos meus posts performaram melhor?")

	records, err := e.chatSvc.ConversationToolCalls(ctxFor(e.wsA), e.wsA, conv)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	byName := map[chatdomain.ToolName]*chatdomain.ToolCallRecord{}
	for i := range records {
		byName[records[i].ToolName] = &records[i]
	}
	for _, name := range []chatdomain.ToolName{mttools.PostListTool, mttools.PostInsightsTool} {
		rec, ok := byName[name]
		if !ok {
			t.Fatalf("%s was not recorded", name)
		}
		if rec.Status != chatdomain.ToolCallOK {
			t.Errorf("%s: status = %q", name, rec.Status)
		}
		for _, field := range []*string{rec.Arguments, rec.Result} {
			if field == nil || strings.TrimSpace(*field) == "" {
				t.Errorf("%s: the record is missing arguments or result", name)
			}
		}
		if rec.ConversationID != conv {
			t.Errorf("%s: conversation = %v", name, rec.ConversationID)
		}
		if rec.Round < 1 {
			t.Errorf("%s: round = %d", name, rec.Round)
		}
		if rec.DurationMS < 0 {
			t.Errorf("%s: duration = %d", name, rec.DurationMS)
		}
	}

	// THE secret check, over the whole audit trail: no access token, no
	// refresh token, no app secret, in any argument or any result.
	var everything strings.Builder
	for _, r := range records {
		if r.Arguments != nil {
			everything.WriteString(*r.Arguments)
		}
		if r.Result != nil {
			everything.WriteString(*r.Result)
		}
	}
	for _, secret := range []string{
		"LONG_LIVED_TOKEN", "SHORT_LIVED_TOKEN", "REFRESHED_TOKEN", testAppSecret,
	} {
		if strings.Contains(everything.String(), secret) {
			t.Errorf("a credential reached the audit trail: %s", secret)
		}
	}
}

/* ── 9. the whole story ──────────────────────────────────────────────── */

// Meta evidence → agent reasoning → ONE internal Thread, and only because
// the user asked for it.
//
// This is the shape the sprint is for, and the two halves stay apart the
// whole way: everything read is external and unchanged, and the single
// write lands in our own pipeline.
func TestExternalEvidenceBecomesAnInternalIdeaOnlyWhenAsked(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allMetaTools...)
	e.authorize(e.wsA, agentID, allThreadTools...)

	// Turn 1: read the history and the metrics. Nothing is written.
	e.llm.scriptCalls(
		chatdomain.ToolCall{Name: mttools.PostListTool, Arguments: `{"limit":5}`},
		chatdomain.ToolCall{Name: mttools.PostInsightsTool, Arguments: `{"post_id":"p_005"}`},
	)
	sink := e.turn(e.wsA, conv, "veja meus posts e qual performou melhor")
	for _, name := range []chatdomain.ToolName{
		mttools.PostListTool, mttools.PostInsightsTool,
	} {
		if ev, ok := sink.finished(name); !ok || ev.ErrorCode != "" {
			t.Fatalf("%s failed: %+v", name, ev)
		}
	}
	if n := len(e.liveThreads(e.wsA)); n != 0 {
		t.Fatalf("%d threads were created before the user asked for anything", n)
	}

	// Turn 2: the "já falei sobre isso?" half, in its own turn.
	e.llm.scriptCalls(chatdomain.ToolCall{Name: mttools.PostSearchTool, Arguments: `{"query":"AI agents"}`})
	if ev, _ := e.turn(e.wsA, conv, "eu já falei de AI agents?").
		finished(mttools.PostSearchTool); ev.ErrorCode != "" {
		t.Fatalf("search failed: %+v", ev)
	}
	if n := len(e.liveThreads(e.wsA)); n != 0 {
		t.Fatalf("%d threads were created by a search", n)
	}

	// Turn 3: three opportunities, in prose. Still nothing written.
	e.llm.scriptReply("Três oportunidades: 1) revogar acesso de agente; 2) audit trail como " +
		"feature de produto; 3) o custo escondido de microservices cedo.")
	e.turn(e.wsA, conv, "com base nisso, me dê 3 oportunidades de conteúdo")
	if n := len(e.liveThreads(e.wsA)); n != 0 {
		t.Fatalf("%d threads were created by a suggestion", n)
	}

	// Turn 4: the user picks one. NOW a Thread is born — in the internal
	// pipeline, through the internal tool.
	e.llm.scriptCalls(chatdomain.ToolCall{
		Name: threadstools.ThreadCreateTool,
		Arguments: `{"title":"Audit trail como feature de produto",` +
			`"content":"Todo agente que escreve em produção precisa de um audit trail. Não é compliance, é usabilidade."}`,
	})
	if ev, _ := e.turn(e.wsA, conv, "gostei da segunda, salva como ideia").
		finished(threadstools.ThreadCreateTool); ev.ErrorCode != "" {
		t.Fatalf("the create failed: %+v", ev)
	}

	live := e.liveThreads(e.wsA)
	if len(live) != 1 {
		t.Fatalf("%d internal threads, want exactly 1", len(live))
	}
	// And it is OURS: a row in threads.threads, with our lifecycle, not a
	// copy of anything Meta returned.
	var title, status string
	if err := e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT title, status FROM threads.threads WHERE deleted_at IS NULL`).Scan(&title, &status); err != nil {
		t.Fatalf("read the internal thread: %v", err)
	}
	if status != "idea" {
		t.Errorf("status = %q, want idea", status)
	}
	if !strings.Contains(title, "Audit trail") {
		t.Errorf("title = %q", title)
	}
	// Nothing from Meta was copied in: the stored text is what the agent
	// wrote for the user, not a published post.
	var content string
	_ = e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT content FROM threads.threads WHERE deleted_at IS NULL`).Scan(&content)
	for _, published := range []string{"p_005", "https://www.threads.net"} {
		if strings.Contains(content, published) {
			t.Errorf("a Meta post was copied into the internal pipeline: %q", content)
		}
	}
}

func errorsAs(err error, target **chatdomain.ToolFailure) bool {
	for err != nil {
		if f, ok := err.(*chatdomain.ToolFailure); ok {
			*target = f
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
