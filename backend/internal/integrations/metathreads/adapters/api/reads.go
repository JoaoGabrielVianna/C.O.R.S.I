package api

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/corsi/backend/internal/integrations/metathreads/domain"
	"github.com/corsi/backend/internal/integrations/metathreads/ports"
)

/* ── field selections ────────────────────────────────────────────────── */

// postFields is the `fields` parameter for reading the operator's OWN
// posts — the `/threads` edge and a single media object.
//
// Requested explicitly rather than taking Meta's default, for two reasons.
// The default is a bare id, so a listing without this would be a page of
// identifiers and nothing else. And naming the set here means the shape
// this integration depends on is written down in one place, next to the
// struct it fills.
//
// ── Why `is_reply` and `has_replies` are NOT here ──────────────────────
// They are documented on the KEYWORD SEARCH response and nowhere else:
// Meta's own example for the `/threads` edge omits both, and asking for
// them there fails the whole request with the generic "An unknown error
// occurred" — no field name, no hint. That cost a real debugging session,
// so the two field lists are separate and this comment is the reason.
var postFields = strings.Join([]string{
	"id", "text", "media_type", "permalink", "timestamp",
	"username", "is_quote_post", "topic_tag",
}, ",")

// searchFields is the `fields` parameter for keyword search, which is the
// one endpoint Meta documents `is_reply` and `has_replies` on.
var searchFields = strings.Join([]string{
	"id", "text", "media_type", "permalink", "timestamp",
	"username", "is_quote_post", "is_reply", "has_replies",
}, ",")

// profileFields is the `fields` parameter for GET /me.
var profileFields = strings.Join([]string{
	"id", "username", "name", "threads_profile_picture_url",
	"threads_biography", "is_verified",
}, ",")

/* ── limits ──────────────────────────────────────────────────────────── */

const (
	// defaultPostLimit is what a caller that says nothing gets.
	//
	// Ten, and small on purpose. Every post carried back becomes prompt
	// tokens on the next provider call of the same turn, and a post's text
	// runs to 500 characters. A model that needs more asks for more; a model
	// handed fifty by default spends a turn's budget before it has decided
	// what it is looking for.
	defaultPostLimit = 10
	// maxPostLimit is Meta's own ceiling for keyword search, applied to both
	// reads so one number describes the boundary.
	maxPostLimit = 100
)

func clampLimit(n int) int {
	if n <= 0 {
		return defaultPostLimit
	}
	if n > maxPostLimit {
		return maxPostLimit
	}
	return n
}

/* ── the envelopes ───────────────────────────────────────────────────── */

type postEnvelope struct {
	Data   []wirePost `json:"data"`
	Paging struct {
		Cursors struct {
			After string `json:"after"`
		} `json:"cursors"`
		Next string `json:"next"`
	} `json:"paging"`
}

type wirePost struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	MediaType   string `json:"media_type"`
	Permalink   string `json:"permalink"`
	Timestamp   string `json:"timestamp"`
	Username    string `json:"username"`
	IsQuotePost bool   `json:"is_quote_post"`
	IsReply     bool   `json:"is_reply"`
	HasReplies  bool   `json:"has_replies"`
	TopicTag    string `json:"topic_tag"`
}

func (w wirePost) normalized() ports.Post {
	p := ports.Post{
		ID: w.ID, Text: w.Text, Type: w.MediaType, Link: w.Permalink,
		Username: w.Username, IsReply: w.IsReply, IsQuote: w.IsQuotePost,
		HasReplies: w.HasReplies, TopicTag: w.TopicTag,
	}
	if t, ok := parseMetaTime(w.Timestamp); ok {
		p.At = t
	}
	return p
}

// metaTimeLayouts are the shapes Meta actually sends.
//
// ── Why RFC3339 alone is not enough, and how that was found ────────────
// Meta stamps `2026-08-20T12:00:00+0000` — a numeric zone with NO colon.
// time.RFC3339 requires `+00:00` or `Z` and REFUSES that string, so a
// parser that only knew RFC3339 would drop the timestamp off every post
// Meta ever returned, silently: the field would simply be absent, and a
// model would answer "what have I posted recently" from an archive with no
// dates in it.
//
// It is silent because a failed parse has no natural failure mode here —
// there is nothing to report to and nothing that looks broken. The
// integration suite catches it only because its fake speaks Meta's real
// format rather than Go's preferred one, which is the entire argument for
// building the fake at the wire instead of at the port.
var metaTimeLayouts = []string{
	"2006-01-02T15:04:05-0700", // what Meta actually sends
	time.RFC3339,               // the colon form, in case it ever changes
}

// parseMetaTime reads a Meta timestamp.
//
// A value that will not parse is reported as absent rather than defaulted
// to the epoch: a post apparently published in 1970 is worse than a post
// with no date, because a model will reason about it.
func parseMetaTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range metaTimeLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func (e postEnvelope) page() ports.PostPage {
	out := ports.PostPage{Posts: make([]ports.Post, 0, len(e.Data))}
	for _, w := range e.Data {
		out.Posts = append(out.Posts, w.normalized())
	}
	// A cursor is only a continuation when Meta also offered a next link.
	// Meta returns an `after` cursor on the final page too, and treating it
	// as "there is more" would make a caller page forever over an empty
	// result.
	if e.Paging.Next != "" {
		out.NextCursor = e.Paging.Cursors.After
	}
	return out
}

/* ── profile ─────────────────────────────────────────────────────────── */

func (c *Client) Profile(ctx context.Context, cr ports.Credentials) (ports.Profile, error) {
	q := url.Values{}
	q.Set("fields", profileFields)
	q.Set("access_token", cr.Token)

	var wire struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		Name       string `json:"name"`
		Picture    string `json:"threads_profile_picture_url"`
		Biography  string `json:"threads_biography"`
		IsVerified bool   `json:"is_verified"`
	}
	if err := c.do(ctx, endpoint(cr.BaseURL, "me", q), &wire); err != nil {
		return ports.Profile{}, err
	}
	return ports.Profile{
		ID: wire.ID, Username: wire.Username, Name: wire.Name,
		Biography: wire.Biography, ProfilePictureURL: wire.Picture,
		IsVerified: wire.IsVerified,
	}, nil
}

/* ── own posts ───────────────────────────────────────────────────────── */

func (c *Client) ListPosts(ctx context.Context, cr ports.Credentials, pq ports.PostQuery) (ports.PostPage, error) {
	q := url.Values{}
	q.Set("fields", postFields)
	q.Set("limit", strconv.Itoa(clampLimit(pq.Limit)))
	q.Set("access_token", cr.Token)
	if pq.After != "" {
		q.Set("after", pq.After)
	}
	if !pq.Since.IsZero() {
		q.Set("since", strconv.FormatInt(pq.Since.Unix(), 10))
	}
	if !pq.Until.IsZero() {
		q.Set("until", strconv.FormatInt(pq.Until.Unix(), 10))
	}

	var env postEnvelope
	if err := c.do(ctx, endpoint(cr.BaseURL, "me/threads", q), &env); err != nil {
		return ports.PostPage{}, err
	}
	return env.page(), nil
}

func (c *Client) GetPost(ctx context.Context, cr ports.Credentials, id string) (ports.Post, error) {
	q := url.Values{}
	q.Set("fields", postFields)
	q.Set("access_token", cr.Token)

	var wire wirePost
	if err := c.do(ctx, endpoint(cr.BaseURL, url.PathEscape(id), q), &wire); err != nil {
		return ports.Post{}, err
	}
	if wire.ID == "" {
		return ports.Post{}, domain.NotFound("post %s was not found", id)
	}
	return wire.normalized(), nil
}

/* ── keyword search ──────────────────────────────────────────────────── */

// earliestSearchTimestamp is Meta's floor for the `since` parameter of
// keyword search: 1688540400. Sending anything earlier is refused outright,
// so a caller asking for "everything" is clamped rather than failed.
const earliestSearchTimestamp = 1688540400

func (c *Client) SearchPosts(ctx context.Context, cr ports.Credentials, sq ports.SearchQuery) (ports.PostPage, error) {
	q := url.Values{}
	q.Set("q", sq.Query)
	q.Set("fields", searchFields)
	q.Set("limit", strconv.Itoa(clampLimit(sq.Limit)))
	q.Set("access_token", cr.Token)
	if sq.Kind != "" {
		q.Set("search_type", sq.Kind)
	}
	if sq.Mode != "" {
		q.Set("search_mode", sq.Mode)
	}
	if sq.AuthorUsername != "" {
		q.Set("author_username", sq.AuthorUsername)
	}
	if !sq.Since.IsZero() {
		since := sq.Since.Unix()
		if since < earliestSearchTimestamp {
			since = earliestSearchTimestamp
		}
		q.Set("since", strconv.FormatInt(since, 10))
	}
	if !sq.Until.IsZero() {
		q.Set("until", strconv.FormatInt(sq.Until.Unix(), 10))
	}
	if sq.After != "" {
		q.Set("after", sq.After)
	}

	var env postEnvelope
	if err := c.do(ctx, endpoint(cr.BaseURL, "keyword_search", q), &env); err != nil {
		return ports.PostPage{}, err
	}
	return env.page(), nil
}

/* ── insights ────────────────────────────────────────────────────────── */

// metricEnvelope is Meta's insights shape: a list of named series, each
// carrying values. Post-level metrics have exactly one value; account-level
// ones may have several, and `total_value` appears instead for some.
type metricEnvelope struct {
	Data []struct {
		Name   string `json:"name"`
		Period string `json:"period"`
		Values []struct {
			Value int64 `json:"value"`
		} `json:"values"`
		TotalValue *struct {
			Value int64 `json:"value"`
		} `json:"total_value"`
	} `json:"data"`
}

// collect reduces the envelope to name → summed value.
//
// Summed because an account metric over a window arrives as one entry per
// bucket, and the question being asked ("how many views in this period") is
// the total. A post metric has one value, so the sum is that value.
func (e metricEnvelope) collect() map[string]int64 {
	out := make(map[string]int64, len(e.Data))
	for _, series := range e.Data {
		if series.TotalValue != nil {
			out[series.Name] = series.TotalValue.Value
			continue
		}
		var sum int64
		for _, v := range series.Values {
			sum += v.Value
		}
		out[series.Name] = sum
	}
	return out
}

// pick returns a pointer to the metric when Meta reported it, and records
// the name as unavailable when it did not. The distinction is the whole
// reason the fields are pointers — see ports.PostInsights.
func pick(got map[string]int64, name string, unavailable *[]string) *int64 {
	v, ok := got[name]
	if !ok {
		*unavailable = append(*unavailable, name)
		return nil
	}
	return &v
}

func (c *Client) PostInsights(ctx context.Context, cr ports.Credentials, id string) (ports.PostInsights, error) {
	q := url.Values{}
	q.Set("metric", "views,likes,replies,reposts,quotes,shares")
	q.Set("access_token", cr.Token)

	var env metricEnvelope
	if err := c.do(ctx, endpoint(cr.BaseURL, url.PathEscape(id)+"/insights", q), &env); err != nil {
		return ports.PostInsights{}, err
	}
	got := env.collect()
	var missing []string
	return ports.PostInsights{
		Views:       pick(got, "views", &missing),
		Likes:       pick(got, "likes", &missing),
		Replies:     pick(got, "replies", &missing),
		Reposts:     pick(got, "reposts", &missing),
		Quotes:      pick(got, "quotes", &missing),
		Shares:      pick(got, "shares", &missing),
		Unavailable: missing,
	}, nil
}

// earliestAccountInsight is Meta's floor for user insights: 2024-04-13,
// Unix 1712991600. Anything earlier is refused, so it is clamped.
const earliestAccountInsight = 1712991600

func (c *Client) AccountInsights(ctx context.Context, cr ports.Credentials, userID string, aq ports.AccountInsightsQuery) (ports.AccountInsights, error) {
	// followers_count is requested in a SEPARATE call from the windowed
	// metrics, because Meta refuses `since`/`until` for it and would fail
	// the whole request rather than ignore the parameter for one series.
	windowed := url.Values{}
	windowed.Set("metric", "views,likes,replies,reposts,quotes,clicks")
	windowed.Set("access_token", cr.Token)
	if !aq.Since.IsZero() {
		since := aq.Since.Unix()
		if since < earliestAccountInsight {
			since = earliestAccountInsight
		}
		windowed.Set("since", strconv.FormatInt(since, 10))
	}
	if !aq.Until.IsZero() {
		windowed.Set("until", strconv.FormatInt(aq.Until.Unix(), 10))
	}

	path := url.PathEscape(userID) + "/threads_insights"
	var env metricEnvelope
	if err := c.do(ctx, endpoint(cr.BaseURL, path, windowed), &env); err != nil {
		return ports.AccountInsights{}, err
	}
	got := env.collect()
	var missing []string
	out := ports.AccountInsights{
		Views:   pick(got, "views", &missing),
		Likes:   pick(got, "likes", &missing),
		Replies: pick(got, "replies", &missing),
		Reposts: pick(got, "reposts", &missing),
		Quotes:  pick(got, "quotes", &missing),
		Clicks:  pick(got, "clicks", &missing),
	}

	followers := url.Values{}
	followers.Set("metric", "followers_count")
	followers.Set("access_token", cr.Token)
	var fenv metricEnvelope
	if err := c.do(ctx, endpoint(cr.BaseURL, path, followers), &fenv); err != nil {
		// A follower count this account cannot report is not a reason to
		// lose the metrics that DID come back. Recorded as unavailable, and
		// the caller says so.
		missing = append(missing, "followers_count")
		out.Unavailable = missing
		return out, nil
	}
	out.FollowersCount = pick(fenv.collect(), "followers_count", &missing)
	out.Unavailable = missing
	return out, nil
}
