// Package tools exposes Meta Threads as capabilities an agent can be
// authorized to use.
//
// ══════════════════════════════════════════════════════════════════════
//
//	meta_threads.*  is NOT  threads.thread.*
//
// ══════════════════════════════════════════════════════════════════════
//
//	meta_threads.*    EXTERNAL EVIDENCE. Posts the operator already
//	                  published on Meta's social network, and the metrics
//	                  Meta reports about them. Read-only, always fresh,
//	                  never stored.
//
//	threads.thread.*  INTERNAL STATE. The operator's own pipeline of
//	                  ideas and drafts, in our Postgres. Owned by
//	                  internal/threads, and the only thing a write ever
//	                  touches.
//
// An agent granted both can compare them. Nothing here writes to either
// one, and nothing here may ever create a Thread: finding an interesting
// post is not the user asking for it to be saved. That rule is enforced
// structurally — this package cannot reach internal/threads, has no import
// of it, and returns data rather than performing anything.
//
// ── Why this package imports the Agents module's ports ─────────────────
// It implements `chat/ports.Tool`, an interface Agents DECLARES and this
// package SATISFIES — the same dependency inversion GitHub, Job Radar and
// Threads use. Nothing here can observe which agent is calling, and nothing
// in Agents can name Meta Threads.
//
// ── Read-only, structurally ────────────────────────────────────────────
// Every Definition below declares EffectRead, and that is not a promise
// this file keeps by discipline: the application service it calls exposes
// no write, the port declares no write verb, and the OAuth scopes this
// integration can even request contain no write permission. Four layers,
// none of which has a publish in it.
package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/integrations/metathreads/app"
	"github.com/corsi/backend/internal/integrations/metathreads/domain"
	"github.com/corsi/backend/internal/integrations/metathreads/ports"
	"github.com/corsi/backend/internal/platform/workspace"
)

// The six capabilities, named once.
const (
	ProfileGetTool      chatdomain.ToolName = "meta_threads.profile.get"
	ProfileInsightsTool chatdomain.ToolName = "meta_threads.profile.insights"
	PostListTool        chatdomain.ToolName = "meta_threads.post.list"
	PostGetTool         chatdomain.ToolName = "meta_threads.post.get"
	PostInsightsTool    chatdomain.ToolName = "meta_threads.post.insights"
	PostSearchTool      chatdomain.ToolName = "meta_threads.post.search"
)

// New builds every Meta Threads tool, ready for chat.Deps.Tools.
func New(svc *app.Service) []chatports.Tool {
	b := base{svc: svc}
	return []chatports.Tool{
		profileGet{b}, profileInsights{b},
		postList{b}, postGet{b}, postInsights{b}, postSearch{b},
	}
}

type base struct{ svc *app.Service }

/* ── the workspace ───────────────────────────────────────────────────── */

func workspaceOf(ctx context.Context) (uuid.UUID, error) {
	ws, ok := workspace.FromContext(ctx)
	if !ok || ws == uuid.Nil {
		return uuid.Nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"this call arrived without a workspace, so no Meta Threads connection could be resolved")
	}
	return ws, nil
}

/* ── error translation ───────────────────────────────────────────────── */

// toolError turns a domain error into one the model can act on.
//
// ── Why an upstream failure is an EXECUTION failure ────────────────────
// The recoverable codes tell a model "your request was wrong, fix it and
// retry". A Meta outage, an expired token or a missing scope are not that:
// nothing the model can put in an argument will help, and reporting them as
// invalid arguments would send it round a loop of rewordings.
//
// More importantly, none of them may ever surface as an empty result. A
// model told "no posts matched" when the truth is "the token expired" will
// tell the user they have never written about a topic they have written
// about ten times. Every failure below is a failure, loudly.
func toolError(err error) error {
	var de *domain.Error
	if !errAs(err, &de) {
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, err.Error())
	}
	switch de.Kind {
	case domain.KindInvalid, domain.KindNotFound:
		return chatdomain.ToolError(chatdomain.ToolErrInvalidArguments, de.Message)
	case domain.KindNotConnected:
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"this workspace has no Meta Threads account connected, so there is no published "+
				"history to read. Tell the user to connect one in Integrations; do not guess "+
				"at what they have posted.")
	case domain.KindScopeMissing, domain.KindTokenExpired:
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, de.Message+
			". Report this to the user rather than answering from memory.")
	default:
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"Meta Threads could not be read: "+de.Message+
				". This is an upstream failure, NOT an empty result — do not report it as "+
				"the user having no posts.")
	}
}

func errAs(err error, target **domain.Error) bool {
	for err != nil {
		if de, ok := err.(*domain.Error); ok {
			*target = de
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

/* ── shaping ─────────────────────────────────────────────────────────── */

const timeFormat = "2006-01-02T15:04:05Z"

// postSummary is one post as a listing entry.
//
// ── Why the full text is here and not an excerpt ───────────────────────
// Because a Threads post is capped at 500 characters, so the "full" text IS
// roughly an excerpt, and the questions this tool exists for — what themes
// repeat, which hooks work — cannot be answered from a truncated opening.
// The listing is bounded by COUNT instead, which is the honest lever.
func postSummary(p ports.Post) map[string]any {
	m := map[string]any{
		"id":   p.ID,
		"text": p.Text,
	}
	if !p.At.IsZero() {
		m["published_at"] = p.At.UTC().Format(timeFormat)
	}
	if p.Type != "" {
		m["media_type"] = p.Type
	}
	if p.Link != "" {
		m["permalink"] = p.Link
	}
	if p.Username != "" {
		m["author"] = p.Username
	}
	if p.TopicTag != "" {
		m["topic_tag"] = p.TopicTag
	}
	// Only when true: four false flags on every row would be noise in the
	// model's context, and the absence reads correctly.
	if p.IsReply {
		m["is_reply"] = true
	}
	if p.IsQuote {
		m["is_quote"] = true
	}
	if p.HasReplies {
		m["has_replies"] = true
	}
	return m
}

// metrics renders an insight set.
//
// ── Why there is no engagement score ───────────────────────────────────
// Adding likes to replies is an INTERPRETATION, and interpretation is the
// agent's job. A score computed here would travel as though Meta reported
// it, and every later answer would inherit a weighting nobody chose and
// nobody could see. The agent is free to say "counting likes and replies
// together, this one led" — and when it does, the reader can tell that is
// the agent talking.
//
// An unreported metric is omitted and NAMED in `unavailable`, never
// defaulted to zero: a post that was never measured and a post nobody
// looked at are different facts.
func metrics(in ports.PostInsights) map[string]any {
	m := map[string]any{}
	for name, v := range map[string]*int64{
		"views": in.Views, "likes": in.Likes, "replies": in.Replies,
		"reposts": in.Reposts, "quotes": in.Quotes, "shares": in.Shares,
	} {
		if v != nil {
			m[name] = *v
		}
	}
	if len(in.Unavailable) > 0 {
		m["unavailable"] = in.Unavailable
		m["unavailable_note"] = "Meta did not report these metrics for this post. " +
			"That is not the same as zero — do not treat them as zero."
	}
	return m
}

// maxToolResultBytes bounds what these tools hand back, below the Agents
// ceiling so an oversized result is a smaller honest answer rather than an
// execution failure the model reads as "the tool is broken".
const maxToolResultBytes = 24 << 10

func fit(out map[string]any, listKey string) (chatdomain.ToolOutput, error) {
	if size(out) <= maxToolResultBytes {
		return out, nil
	}
	items, _ := out[listKey].([]map[string]any)
	if listKey == "" || items == nil {
		return nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"the result was larger than one tool call may carry; ask for fewer posts")
	}
	kept := len(items)
	for kept > 0 {
		kept /= 2
		out[listKey] = items[:kept]
		out["truncated"] = true
		out["truncated_note"] = "the full result did not fit in one tool call; only the " +
			"first entries are shown. Ask for a smaller limit."
		if size(out) <= maxToolResultBytes {
			return out, nil
		}
	}
	return nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
		"even a single result was larger than one tool call may carry")
}

func size(v any) int {
	raw, err := json.Marshal(v)
	if err != nil {
		return maxToolResultBytes + 1
	}
	return len(raw)
}

/* ── argument readers ────────────────────────────────────────────────── */

func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func argInt(args map[string]any, key string, fallback int) int {
	switch n := args[key].(type) {
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	default:
		return fallback
	}
}

// argDate reads an optional YYYY-MM-DD bound.
//
// Date rather than timestamp because that is what a person says out loud
// ("desde junho"), and a model asked for an RFC3339 instant will invent a
// time of day. A value that will not parse is a refusal, not a silent
// ignore: dropping a window the user asked for would answer a different
// question and look like an answer to theirs.
func argDate(args map[string]any, key string) (time.Time, error) {
	raw := argString(args, key)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"argument "+key+" must be a date as YYYY-MM-DD")
	}
	return t.UTC(), nil
}

var postIDProperty = chatdomain.ToolProperty{
	Type: chatdomain.TypeString,
	Description: "The post's id, exactly as returned by meta_threads.post.list or " +
		"meta_threads.post.search. Never invent one: if you do not have an id, list first.",
	MaxLength: 100,
}

const limitDescription = "How many posts to return, 1 to 100. Defaults to 10. " +
	"Ask for a small number first and page with `after` if you need more: every post " +
	"returned costs context, and you rarely need fifty to answer a question."

/* ── meta_threads.profile.get ────────────────────────────────────────── */

type profileGet struct{ base }

func (profileGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   ProfileGetTool,
		Title:  "Meta Threads · Ver perfil",
		Effect: chatdomain.EffectRead,
		// Every answer here describes Meta's systems, not ours. See
		// chatdomain.ToolDefinition.External.
		External: true,
		Description: "Reads the connected Meta Threads account: username, display name, bio and " +
			"whether it is verified. Meta Threads is the EXTERNAL social network where the user " +
			"publishes — it is not the user's internal content pipeline, which is threads.thread.*. " +
			"Use this to confirm whose published history you are looking at.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				// The port's schema requires at least one property, and this
				// call genuinely takes none: the account is decided by the
				// workspace's connection, never by an argument. A no-op flag
				// is the honest way to say that — it changes nothing, and the
				// description says so.
				"confirm": {
					Type:        chatdomain.TypeBoolean,
					Description: "Ignored. This call takes no arguments: the account is the one connected to the workspace.",
				},
			},
		},
	}
}

func (t profileGet) Execute(ctx context.Context, _ map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	p, err := t.svc.Profile(ctx, ws)
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{"username": p.Username, "account_id": p.ID}
	if p.Name != "" {
		out["name"] = p.Name
	}
	if p.Biography != "" {
		out["biography"] = p.Biography
	}
	if p.IsVerified {
		out["is_verified"] = true
	}
	return out, nil
}

/* ── meta_threads.profile.insights ───────────────────────────────────── */

type profileInsights struct{ base }

func (profileInsights) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   ProfileInsightsTool,
		Title:  "Meta Threads · Métricas da conta",
		Effect: chatdomain.EffectRead,
		// Every answer here describes Meta's systems, not ours. See
		// chatdomain.ToolDefinition.External.
		External: true,
		Description: "Reads account-level metrics from Meta Threads: total views, likes, replies, " +
			"reposts, quotes, link clicks and follower count. Use it for questions about the " +
			"account as a whole (\"como está minha conta\", \"quantos seguidores\"), and " +
			"meta_threads.post.insights for one specific post. " +
			"Metrics Meta did not report are listed under `unavailable` and are NOT zero.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"since": {
					Type: chatdomain.TypeString,
					Description: "Optional. Start of the window, as YYYY-MM-DD. Meta has no data " +
						"before 2024-04-13 and earlier dates are clamped to it.",
					MaxLength: 10,
				},
				"until": {
					Type:        chatdomain.TypeString,
					Description: "Optional. End of the window, as YYYY-MM-DD.",
					MaxLength:   10,
				},
			},
		},
	}
}

func (t profileInsights) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	since, err := argDate(args, "since")
	if err != nil {
		return nil, err
	}
	until, err := argDate(args, "until")
	if err != nil {
		return nil, err
	}

	in, err := t.svc.AccountInsights(ctx, ws, ports.AccountInsightsQuery{Since: since, Until: until})
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{}
	for name, v := range map[string]*int64{
		"views": in.Views, "likes": in.Likes, "replies": in.Replies,
		"reposts": in.Reposts, "quotes": in.Quotes, "clicks": in.Clicks,
		"followers_count": in.FollowersCount,
	} {
		if v != nil {
			out[name] = *v
		}
	}
	if len(in.Unavailable) > 0 {
		out["unavailable"] = in.Unavailable
		out["unavailable_note"] = "Meta did not report these. That is not the same as zero."
	}
	return out, nil
}

/* ── meta_threads.post.list ──────────────────────────────────────────── */

type postList struct{ base }

func (postList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   PostListTool,
		Title:  "Meta Threads · Listar meus posts",
		Effect: chatdomain.EffectRead,
		// Every answer here describes Meta's systems, not ours. See
		// chatdomain.ToolDefinition.External.
		External: true,
		Description: "Lists the posts the user has PUBLISHED on Meta Threads, newest first. This " +
			"is their real published history on the external social network — evidence about " +
			"what they have already said in public. It is NOT their internal pipeline of ideas " +
			"and drafts, which is threads.thread.list. " +
			"Use it for questions about past output: \"meus últimos posts\", \"o que eu venho " +
			"publicando\", \"quais temas eu repito\". " +
			"Read a page, decide which posts matter, then call meta_threads.post.insights on " +
			"just those — pulling metrics for everything is slow and mostly wasted. " +
			"Finding an interesting post here is NEVER a reason to create an internal Thread; " +
			"only do that when the user asks for something to be saved.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"limit": {Type: chatdomain.TypeInteger, Description: limitDescription},
				"after": {
					Type: chatdomain.TypeString,
					Description: "Optional. The `next_cursor` from a previous call, to read the " +
						"following page. Opaque — pass it back exactly as received.",
					MaxLength: 500,
				},
				"since": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Only posts published on or after this date, YYYY-MM-DD.",
					MaxLength:   10,
				},
				"until": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Only posts published on or before this date, YYYY-MM-DD.",
					MaxLength:   10,
				},
			},
		},
	}
}

func (t postList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	since, err := argDate(args, "since")
	if err != nil {
		return nil, err
	}
	until, err := argDate(args, "until")
	if err != nil {
		return nil, err
	}

	page, err := t.svc.ListPosts(ctx, ws, ports.PostQuery{
		Limit: argInt(args, "limit", 0), After: argString(args, "after"),
		Since: since, Until: until,
	})
	if err != nil {
		return nil, toolError(err)
	}

	rows := make([]map[string]any, 0, len(page.Posts))
	for _, p := range page.Posts {
		rows = append(rows, postSummary(p))
	}
	out := map[string]any{"posts": rows}
	if page.NextCursor != "" {
		out["next_cursor"] = page.NextCursor
		// Said explicitly, because "ten posts came back" and "ten posts
		// exist" are different facts and a model will otherwise conclude the
		// second from the first.
		out["has_more"] = true
	} else {
		out["has_more"] = false
	}
	if len(rows) == 0 {
		out["note"] = "no posts came back for this request. If a date window was set, try a " +
			"wider one before concluding the user has not posted."
	}
	return fit(out, "posts")
}

/* ── meta_threads.post.get ───────────────────────────────────────────── */

type postGet struct{ base }

func (postGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   PostGetTool,
		Title:  "Meta Threads · Ver post",
		Effect: chatdomain.EffectRead,
		// Every answer here describes Meta's systems, not ours. See
		// chatdomain.ToolDefinition.External.
		External: true,
		Description: "Reads one published Meta Threads post in full. Use meta_threads.post.list " +
			"or meta_threads.post.search to find the id. Reading a post does not change it — " +
			"this capability cannot edit, delete or repost anything.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{"post_id": postIDProperty},
			Required:   []string{"post_id"},
		},
	}
}

func (t postGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id := argString(args, "post_id")
	if id == "" {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments, "post_id is required")
	}
	p, err := t.svc.GetPost(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{"post": postSummary(p)}, "")
}

/* ── meta_threads.post.insights ──────────────────────────────────────── */

type postInsights struct{ base }

func (postInsights) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   PostInsightsTool,
		Title:  "Meta Threads · Métricas do post",
		Effect: chatdomain.EffectRead,
		// Every answer here describes Meta's systems, not ours. See
		// chatdomain.ToolDefinition.External.
		External: true,
		Description: "Reads the metrics Meta reports for ONE published post: views, likes, " +
			"replies, reposts, quotes and shares. This is how \"qual post performou melhor\" " +
			"gets answered — list first, then read insights for the handful that matter. " +
			"These are Meta's raw counts. There is no engagement score and none is implied: " +
			"if you want to rank posts by likes plus replies, do that yourself and SAY that " +
			"is what you did. " +
			"Metrics under `unavailable` were not reported by Meta and are NOT zero — some " +
			"post types carry no insights at all.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{"post_id": postIDProperty},
			Required:   []string{"post_id"},
		},
	}
}

func (t postInsights) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id := argString(args, "post_id")
	if id == "" {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments, "post_id is required")
	}
	in, err := t.svc.PostInsights(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}
	return map[string]any{"post_id": id, "metrics": metrics(in)}, nil
}

/* ── meta_threads.post.search ────────────────────────────────────────── */

type postSearch struct{ base }

// searchCoverageNote is attached to EVERY search result.
//
// ── Why the tool cannot simply say which corpus answered ───────────────
// Meta's keyword search endpoint serves two different corpora through one
// URL. Without app-review approval for `threads_keyword_search` it searches
// only the authenticated user's own posts; with approval it searches public
// posts. The response is byte-identical either way — no field, no header,
// no count distinguishes them.
//
// So this build cannot know, and pretending otherwise would be the worst
// available option: a model told "these are the top public posts about X"
// when it is actually looking at the user's own archive would report
// strangers' opinions as market signal, or the user's own words back to
// them as a trend.
//
// The note says exactly what is known and exactly what is not.
const searchCoverageNote = "COVERAGE: this searched the user's own published posts, " +
	"and — only if this deployment's Meta app holds app-review approval for " +
	"threads_keyword_search — public posts by other people. Meta returns the same " +
	"response shape either way and this tool cannot tell which happened. Check the " +
	"`author` on each result before describing anything as someone else's post, and " +
	"never present these results as a complete or ranked view of what is trending on " +
	"Meta Threads."

func (postSearch) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   PostSearchTool,
		Title:  "Meta Threads · Buscar posts",
		Effect: chatdomain.EffectRead,
		// Every answer here describes Meta's systems, not ours. See
		// chatdomain.ToolDefinition.External.
		External: true,
		Description: "Searches Meta Threads posts by keyword or topic tag. " +
			"Its main use is answering \"eu já falei sobre X?\" against the user's real " +
			"published history — far cheaper than paging through everything. " +
			"IMPORTANT: what this can reach depends on the deployment's Meta app approval, " +
			"and the response does not say which. It always covers the user's own posts; it " +
			"covers other people's public posts ONLY if the app was approved for that. " +
			"So do not use it as a trend or discovery feed, and do not describe its results " +
			"as \"what is popular right now\" — check the `author` field on each result " +
			"before attributing anything.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"query": {
					Type:        chatdomain.TypeString,
					Description: "The keyword or topic tag to search for. Required.",
					MaxLength:   200,
				},
				"mode": {
					Type: chatdomain.TypeString,
					Description: "Optional. 'keyword' (default) searches post text; 'tag' searches " +
						"Meta's topic tags.",
					MaxLength: 10,
				},
				"rank": {
					Type: chatdomain.TypeString,
					Description: "Optional. 'top' (default) uses Meta's own relevance ordering; " +
						"'recent' returns newest first. The ordering is Meta's and is not a score " +
						"this system computed.",
					MaxLength: 10,
				},
				"author": {
					Type: chatdomain.TypeString,
					Description: "Optional. Restrict to one Meta Threads username, exact match. " +
						"Use the connected account's own username to search only their history.",
					MaxLength: 100,
				},
				"limit": {Type: chatdomain.TypeInteger, Description: limitDescription},
				"after": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The `next_cursor` from a previous call. Opaque.",
					MaxLength:   500,
				},
				"since": {
					Type: chatdomain.TypeString,
					Description: "Optional. Only posts on or after this date, YYYY-MM-DD. Meta " +
						"holds nothing before 2023-07-05 and earlier dates are clamped.",
					MaxLength: 10,
				},
				"until": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Only posts on or before this date, YYYY-MM-DD.",
					MaxLength:   10,
				},
			},
			Required: []string{"query"},
		},
	}
}

// searchModes and searchRanks map the words the model is told to the words
// Meta accepts. The model is never asked to spell Meta's uppercase enum.
var searchModes = map[string]string{"keyword": "KEYWORD", "tag": "TAG"}
var searchRanks = map[string]string{"top": "TOP", "recent": "RECENT"}

func (t postSearch) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	query := argString(args, "query")
	if query == "" {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments, "query is required")
	}
	since, err := argDate(args, "since")
	if err != nil {
		return nil, err
	}
	until, err := argDate(args, "until")
	if err != nil {
		return nil, err
	}

	q := ports.SearchQuery{
		Query: query, AuthorUsername: argString(args, "author"),
		Limit: argInt(args, "limit", 0), After: argString(args, "after"),
		Since: since, Until: until,
	}
	if raw := strings.ToLower(argString(args, "mode")); raw != "" {
		mode, ok := searchModes[raw]
		if !ok {
			return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
				"mode must be 'keyword' or 'tag'")
		}
		q.Mode = mode
	}
	if raw := strings.ToLower(argString(args, "rank")); raw != "" {
		rank, ok := searchRanks[raw]
		if !ok {
			return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
				"rank must be 'top' or 'recent'")
		}
		q.Kind = rank
	}

	page, err := t.svc.SearchPosts(ctx, ws, q)
	if err != nil {
		return nil, toolError(err)
	}

	rows := make([]map[string]any, 0, len(page.Posts))
	for _, p := range page.Posts {
		rows = append(rows, postSummary(p))
	}
	out := map[string]any{"posts": rows, "coverage": searchCoverageNote}
	if page.NextCursor != "" {
		out["next_cursor"] = page.NextCursor
		out["has_more"] = true
	} else {
		out["has_more"] = false
	}
	if len(rows) == 0 {
		// The single most dangerous result this integration can produce, so
		// it is spelled out. Meta also returns an empty array for terms it
		// considers sensitive, which is indistinguishable from "never wrote
		// about this".
		out["note"] = "nothing matched. This means the search found nothing — it does NOT " +
			"prove the user has never written about this. Meta returns an empty result for " +
			"terms it treats as sensitive, and coverage depends on app approval. If it " +
			"matters, page through meta_threads.post.list instead of concluding from this."
	}
	return fit(out, "posts")
}
