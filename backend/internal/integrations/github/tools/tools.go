// Package tools exposes the GitHub integration as capabilities an agent can
// be authorized to use.
//
// ── Why this package imports the Agents module's ports ─────────────────
// It implements `chat/ports.Tool`, an interface Agents DECLARES and this
// package SATISFIES. That is dependency inversion, not the forbidden
// `Integration → Module` direction, and it is the arrangement the Agents
// module documents for itself:
//
//	ports.Tool  — "A tool backed by GitHub is implemented in Integrations …
//	               An interface owned by Agents and satisfied elsewhere,
//	               wired at the composition root, is exactly how a module
//	               receives a capability without learning who provides it."
//
// What would be a violation is this package calling an Agents service,
// reading a chat table, or knowing what an agent is. It does none of those.
// The only chat symbols it touches are the vocabulary of the port itself —
// a name, a schema, an output map, an error code. Nothing here can observe
// which agent is calling, and nothing in Agents can name GitHub.
//
// ── Every tool here is a read ──────────────────────────────────────────
// EffectRead, all of them, and the package contains no code that could
// perform a write: the API port it calls has no write verb to reach. That
// is the honest form of "read-only" — not a flag, an absence.
package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/integrations/github/app"
	"github.com/corsi/backend/internal/integrations/github/domain"
	"github.com/corsi/backend/internal/integrations/github/ports"
	"github.com/corsi/backend/internal/platform/workspace"
)

// New builds every GitHub tool, ready for chat.Deps.Tools.
//
// Returned as a slice of the port type rather than as concrete values,
// because the composition root's only job is to hand them over — and a
// caller that could reach a concrete method could reach past the contract.
func New(svc *app.Service) []chatports.Tool {
	base := base{svc: svc}
	return []chatports.Tool{
		codeSearch{base},
		commitGet{base},
		commitList{base},
		fileGet{base},
		pullRequestGet{base},
		pullRequestList{base},
		repositoryList{base},
	}
}

type base struct{ svc *app.Service }

/* ── the workspace ───────────────────────────────────────────────────── */

// workspaceOf reads the workspace this call belongs to.
//
// ── Why it comes from the context ──────────────────────────────────────
// `ports.Tool.Execute` takes a context and a validated argument map, and
// nothing else. Widening that signature to carry a workspace would change a
// contract Agents owns, and would make every existing tool and every test
// that constructs one a breaking change — for a value the platform already
// puts in the context on every request.
//
// The chain is real and unbroken: the workspace middleware stamps the id,
// the SSE handler passes `r.Context()` into SendMessage, the turn loop
// passes it to executeToolCall, and the executor derives the tool's
// deadline FROM it rather than replacing it. A test in the Agents module
// asserts that a value placed on the request context reaches an executor,
// so a future refactor that detaches the context fails there rather than
// here.
//
// ── Why a missing workspace is a refusal and not a default ─────────────
// There is no sensible fallback. Guessing would mean spending one
// workspace's credential on another's request, which is the single worst
// thing this integration could do. It fails closed.
func workspaceOf(ctx context.Context) (uuid.UUID, error) {
	ws, ok := workspace.FromContext(ctx)
	if !ok || ws == uuid.Nil {
		return uuid.Nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"this call arrived without a workspace, so no GitHub credential could be resolved")
	}
	return ws, nil
}

/* ── error translation ───────────────────────────────────────────────── */

// toolError turns an integration error into one the model can act on.
//
// ── Why every one of these is recoverable ──────────────────────────────
// Because they are all facts about the request rather than faults of the
// system, and the model is the party that can respond to them: narrow the
// query, name an authorized repository, tell the user to reconnect. A tool
// failure goes back as the content of a `tool` message, so an actionable
// sentence here becomes an actionable sentence on screen.
//
// ── Why the credential is never in one ─────────────────────────────────
// Nothing in the integration formats a token into an error — see
// domain.ValidateToken and the note on api.Client.do. This function only
// forwards messages those layers already produced, so there is no path by
// which one could appear here. The audit trail stores this string verbatim,
// which is exactly why that has to be true upstream rather than filtered
// here: a redaction at the last layer is a redaction somebody removes.
func toolError(err error) error {
	var de *domain.Error
	if !errAs(err, &de) {
		// Not one of ours: a context cancellation, or a bug. Reported as an
		// execution failure with its own text, which for a cancellation is
		// what the executor is about to overwrite with a timeout anyway.
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, err.Error())
	}
	switch de.Kind {
	case domain.KindInvalid:
		// The model can fix this one by itself, and telling it so is the
		// whole reason the codes are distinguishable.
		return chatdomain.ToolError(chatdomain.ToolErrInvalidArguments, de.Message)
	default:
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, de.Message)
	}
}

// errAs is errors.As without the import cycle-free import; kept as a named
// function so the intent reads at the call site.
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

/* ── shared schema pieces ────────────────────────────────────────────── */

// repositoryProperty is the argument every repository-scoped tool takes.
//
// ── Why the model names a repository as a string at all ────────────────
// Because it has to name one somehow, and every alternative is worse: a
// uuid it has never seen, an index into a list it must first fetch, or an
// implicit "current repository" that would make two consecutive calls mean
// different things. What makes the string safe is not its format — it is
// that it is only ever a LOOKUP KEY. It is matched against rows the
// operator authorized, and the request that follows is built from the
// stored columns. A string that matches nothing is refused before any
// network call; a string that matches is never used again.
var repositoryProperty = chatdomain.ToolProperty{
	Type: chatdomain.TypeString,
	Description: "The repository, as owner/name (for example acme/website). " +
		"It must be one of the repositories authorized in C.O.R.S.I.; " +
		"use github.repository.list to see them.",
	MaxLength: 140,
}

/* ── github.repository.list ──────────────────────────────────────────── */

type repositoryList struct{ base }

func (repositoryList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   "github.repository.list",
		Title:  "GitHub · Repositórios",
		Effect: chatdomain.EffectRead,
		// Reads GitHub, not us. The invariant is ours-versus-theirs and
		// holds for every integration, not only the newest one.
		External: true,
		Description: "Lists the GitHub repositories this workspace has authorized " +
			"C.O.R.S.I. to read, with each one's language, size, last push, " +
			"default branch, visibility and description. Call it first when " +
			"you do not know which repositories are available: no other GitHub " +
			"tool can reach a repository that is not in this list. What it " +
			"returns identifies and situates a repository; it does not say " +
			"what the code does or how good it is. Read a file or the history " +
			"for that.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"filter": {
					Type: chatdomain.TypeString,
					Description: "Optional. Only return repositories whose owner, name or " +
						"description contains this text.",
					MaxLength: 100,
				},
			},
		},
	}
}

func (t repositoryList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	repos, meta, err := t.svc.ToolListRepositories(ctx, ws)
	if err != nil {
		return nil, toolError(err)
	}
	filter := strings.ToLower(strings.TrimSpace(argString(args, "filter")))

	items := make([]string, 0, len(repos))
	for i := range repos {
		r := &repos[i]
		if filter != "" && !strings.Contains(strings.ToLower(r.FullName+" "+r.Description), filter) {
			continue
		}
		items = append(items, repositoryLine(r, meta[r.GitHubID]))
		if len(items) == domain.MaxRepositoriesListed {
			break
		}
	}

	out := map[string]any{
		"format":           repositoryLineFormat,
		"repositories":     items,
		"authorized_total": len(repos),
	}
	if meta == nil {
		// Said rather than shown. Without this the caller sees "-" in three
		// columns and has no way to tell "GitHub says this repository has no
		// language" from "we could not ask GitHub", which are opposite facts.
		out["note"] = "GitHub could not be reached for the per-repository detail, " +
			"so language, size and last push are shown as '-'. The list of " +
			"repositories itself is complete and authoritative."
	}
	return fit(out, "repositories")
}

// repositoryLineFormat is the legend, sent once instead of repeating six
// field names on every row.
//
// ── Why lines and not one JSON object per repository ───────────────────
// Because the field names would be the majority of the payload. Thirty-two
// repositories as objects with these six fields is around 5.500 characters;
// as lines it is around 3.400 — which is what the four-field version cost
// before this change. The listing therefore carries half again as much
// evidence for the same tokens, and that trade is the whole point: the tool
// existed to answer "which repositories are there" and was being asked
// "which of them are any good", with nothing in the payload to answer it.
//
// The name stays first and unabbreviated on every line, because it is the
// one value that has to survive the round trip: the caller reads it here
// and sends it back as the `repository` argument of the next call.
const repositoryLineFormat = "owner/name | language | size | last push | default branch | " +
	"visibility | description. A '-' means GitHub reports nothing for that field."

// repositoryLine renders one repository as a single line.
//
// Unknown is written as "-" and never as a plausible default. A repository
// with no detected language is not "Go", and an absent push date is not
// today; both would be the tool asserting something GitHub did not say.
func repositoryLine(r *domain.Repository, m app.RepositoryMetadata) string {
	var b strings.Builder
	b.Grow(120)
	b.WriteString(r.FullName)

	b.WriteString(" | ")
	b.WriteString(dash(m.Language))

	b.WriteString(" | ")
	b.WriteString(sizeLabel(m.SizeKB))

	b.WriteString(" | ")
	if m.PushedAt.IsZero() {
		b.WriteString("-")
	} else {
		// The date alone. An hour and a minute would be four more characters
		// per row for a precision nobody chooses a repository by.
		b.WriteString(m.PushedAt.UTC().Format("2006-01-02"))
	}

	b.WriteString(" | ")
	b.WriteString(dash(r.DefaultBranch))

	b.WriteString(" | ")
	if r.Private {
		b.WriteString("private")
	} else {
		b.WriteString("public")
	}
	// Flags only when true. A line reading "not archived, not a fork" would
	// pay characters on every row to say the ordinary case.
	if m.Fork {
		b.WriteString(", fork")
	}
	if m.Archived {
		b.WriteString(", archived")
	}

	b.WriteString(" | ")
	b.WriteString(dash(strings.TrimSpace(flattenToOneLine(r.Description))))
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// sizeLabel keeps the number readable without keeping it precise: the
// question a size answers here is "stub or system", and 2.1 MB answers it
// exactly as well as 2148 KB while being shorter.
func sizeLabel(kb int) string {
	switch {
	case kb <= 0:
		return "-"
	case kb < 1024:
		return itoa(kb) + " KB"
	default:
		mb := float64(kb) / 1024
		return strconv.FormatFloat(mb, 'f', 1, 64) + " MB"
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// flattenToOneLine keeps one repository on one line. A description with a
// newline in it would otherwise break the format the legend just promised.
var descriptionFlattener = strings.NewReplacer("\n", " ", "\r", " ", "|", "/")

func flattenToOneLine(s string) string { return descriptionFlattener.Replace(s) }

/* ── github.commit.list ──────────────────────────────────────────────── */

type commitList struct{ base }

func (commitList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   "github.commit.list",
		Title:  "GitHub · Commits",
		Effect: chatdomain.EffectRead,
		// Reads GitHub, not us. The invariant is ours-versus-theirs and
		// holds for every integration, not only the newest one.
		External: true,
		Description: "Lists recent commits of an authorized repository, newest first. " +
			"Returns the sha, the first lines of the message, the author and the " +
			"date — not the diff. Use github.commit.get for what a commit changed.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"repository": repositoryProperty,
				"ref": {
					Type:        chatdomain.TypeString,
					Description: "Optional branch, tag or sha. Defaults to the default branch.",
					MaxLength:   domain.MaxRefLength,
				},
				"path": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Only commits that touched this file or directory.",
					MaxLength:   domain.MaxFilePathLength,
				},
				"limit": {
					Type:        chatdomain.TypeInteger,
					Description: "How many commits to return, 1 to 50. Defaults to 20.",
				},
			},
			Required: []string{"repository"},
		},
	}
}

func (t commitList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	repo, commits, err := t.svc.ToolListCommits(ctx, ws, argString(args, "repository"),
		ports.CommitQuery{
			Ref:   argString(args, "ref"),
			Path:  argString(args, "path"),
			Limit: argInt(args, "limit", domain.DefaultCommits),
		})
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{
		// The STORED name, not the string the model sent. A transcript that
		// echoed the input would say the repository was read even when a
		// different capitalisation resolved to a different row.
		"repository": repo.FullName,
		"commits":    commits,
	}, "commits")
}

/* ── github.commit.get ───────────────────────────────────────────────── */

type commitGet struct{ base }

func (commitGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   "github.commit.get",
		Title:  "GitHub · Ler commit",
		Effect: chatdomain.EffectRead,
		// Reads GitHub, not us. The invariant is ours-versus-theirs and
		// holds for every integration, not only the newest one.
		External: true,
		Description: "Reads one commit of an authorized repository, with the list of " +
			"files it changed and the diff of each. Large commits are returned " +
			"partially and say so.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"repository": repositoryProperty,
				"sha": {
					Type:        chatdomain.TypeString,
					Description: "The commit sha, full or abbreviated.",
					MaxLength:   domain.MaxRefLength,
				},
			},
			Required: []string{"repository", "sha"},
		},
	}
}

func (t commitGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	sha := strings.TrimSpace(argString(args, "sha"))
	if sha == "" {
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments, "sha required")
	}
	repo, detail, err := t.svc.ToolGetCommit(ctx, ws, argString(args, "repository"), sha)
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{
		"repository": repo.FullName,
		"commit":     detail,
	}
	// Stated, not implied. The `files` array being shorter than
	// `files_total` is a fact the model would otherwise have to infer by
	// comparing two numbers it has no reason to compare.
	if detail.FilesTotal > len(detail.Files) {
		out["note"] = "this commit changed more files than are shown; " +
			"ask for a specific file with github.file.get"
	}
	return fit(out, "")
}

/* ── github.code.search ──────────────────────────────────────────────── */

type codeSearch struct{ base }

func (codeSearch) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   "github.code.search",
		Title:  "GitHub · Buscar código",
		Effect: chatdomain.EffectRead,
		// Reads GitHub, not us. The invariant is ours-versus-theirs and
		// holds for every integration, not only the newest one.
		External: true,
		Description: "Searches the code of the authorized repositories and returns the " +
			"files that matched, with the matching lines. Use it to find where " +
			"something is implemented, then github.file.get to read the file. " +
			"It searches the default branch only, and GitHub allows a small " +
			"number of searches per minute — prefer one precise query over " +
			"several broad ones.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"query": {
					Type: chatdomain.TypeString,
					Description: "What to search for. Plain terms work best " +
						"(budgetPreflight, \"max tokens\"). GitHub qualifiers such as " +
						"language:go, path:internal or extension:ts may be added.",
					MaxLength: domain.MaxSearchQueryLength,
				},
				"repository": {
					Type: chatdomain.TypeString,
					Description: "Optional. Search only this authorized repository, as " +
						"owner/name. Omit to search all of them.",
					MaxLength: 140,
				},
			},
			Required: []string{"query"},
		},
	}
}

func (t codeSearch) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	// The scope is either one authorized repository or all of them, and the
	// service decides which from the argument. Neither branch lets the query
	// string influence it: the `repo:` qualifiers are built from stored
	// rows. See app.ToolSearchCode.
	scope, result, err := t.svc.ToolSearchCode(ctx, ws,
		argString(args, "repository"), argString(args, "query"))
	if err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{
		"hits":        result.Hits,
		"total_count": result.TotalCount,
		"searched":    fullNames(scope),
	}
	if result.TotalCount > len(result.Hits) {
		out["note"] = "GitHub matched more files than are shown; " +
			"narrow the query or search one repository at a time"
	}
	if result.Incomplete {
		out["incomplete"] = true
	}
	if len(scope) == 0 {
		out["note"] = "no repositories are authorized in this workspace, so there was " +
			"nothing to search; the operator authorizes them in Integrations"
	}
	return fit(out, "hits")
}

func fullNames(repos []domain.Repository) []string {
	out := make([]string, 0, len(repos))
	for i := range repos {
		out = append(out, repos[i].FullName)
	}
	return out
}

/* ── github.file.get ─────────────────────────────────────────────────── */

type fileGet struct{ base }

func (fileGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   "github.file.get",
		Title:  "GitHub · Ler arquivo",
		Effect: chatdomain.EffectRead,
		// Reads GitHub, not us. The invariant is ours-versus-theirs and
		// holds for every integration, not only the newest one.
		External: true,
		Description: "Reads one text file from an authorized repository. Returns the " +
			"file's contents; a large file is returned truncated and says so. " +
			"Binary files and directories are refused.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"repository": repositoryProperty,
				"path": {
					Type: chatdomain.TypeString,
					Description: "The path inside the repository, for example " +
						"internal/chat/app/send.go.",
					MaxLength: domain.MaxFilePathLength,
				},
				"ref": {
					Type:        chatdomain.TypeString,
					Description: "Optional branch, tag or sha. Defaults to the default branch.",
					MaxLength:   domain.MaxRefLength,
				},
			},
			Required: []string{"repository", "path"},
		},
	}
}

func (t fileGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	repo, file, err := t.svc.ToolGetFile(ctx, ws, argString(args, "repository"),
		argString(args, "path"), argString(args, "ref"))
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{
		"repository": repo.FullName,
		"file":       file,
	}
	// ── Why the text is trimmed again here ──────────────────────────
	//
	// The client already bounded the read, but it bounded it in BYTES OF
	// FILE, and what has to fit is bytes of JSON — where a newline costs
	// two, a quote costs two, and a tab costs two. A file at the client's
	// ceiling can therefore still be over the tool budget, and `fit` cannot
	// help: there is no list to shrink, so it would refuse the call
	// outright and the model would be told the tool is broken.
	//
	// So the text is measured against the real payload and cut until it
	// fits. That is the difference between "here is the first half of the
	// file" and "I could not read the file".
	fitText(out, file)
	if file.Truncated {
		out["note"] = "this file was longer than one tool result may carry; " +
			"what is shown is the beginning of the file"
	}
	return fit(out, "")
}

/* ── github.pull_request.list ────────────────────────────────────────── */

type pullRequestList struct{ base }

func (pullRequestList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   "github.pull_request.list",
		Title:  "GitHub · Pull requests",
		Effect: chatdomain.EffectRead,
		// Reads GitHub, not us. The invariant is ours-versus-theirs and
		// holds for every integration, not only the newest one.
		External: true,
		Description: "Lists pull requests of an authorized repository, most recently " +
			"updated first. Returns number, title, state and author — not the " +
			"diff. Use github.pull_request.get for one in detail.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"repository": repositoryProperty,
				"state": {
					Type:        chatdomain.TypeString,
					Description: "open, closed or all. Defaults to open.",
					MaxLength:   10,
				},
				"limit": {
					Type:        chatdomain.TypeInteger,
					Description: "How many to return, 1 to 50. Defaults to 20.",
				},
			},
			Required: []string{"repository"},
		},
	}
}

func (t pullRequestList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	repo, pulls, err := t.svc.ToolListPullRequests(ctx, ws, argString(args, "repository"),
		ports.PullRequestQuery{
			State: strings.TrimSpace(argString(args, "state")),
			Limit: argInt(args, "limit", domain.DefaultPullRequests),
		})
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{
		"repository":    repo.FullName,
		"pull_requests": pulls,
	}, "pull_requests")
}

/* ── github.pull_request.get ─────────────────────────────────────────── */

type pullRequestGet struct{ base }

func (pullRequestGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:   "github.pull_request.get",
		Title:  "GitHub · Ler pull request",
		Effect: chatdomain.EffectRead,
		// Reads GitHub, not us. The invariant is ours-versus-theirs and
		// holds for every integration, not only the newest one.
		External: true,
		Description: "Reads one pull request of an authorized repository: its " +
			"description, state, branches and how much it changes. It does not " +
			"return the diff — use github.commit.get or github.file.get for code.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"repository": repositoryProperty,
				"number": {
					Type:        chatdomain.TypeInteger,
					Description: "The pull request number.",
				},
			},
			Required: []string{"repository", "number"},
		},
	}
}

func (t pullRequestGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	repo, pr, err := t.svc.ToolGetPullRequest(ctx, ws, argString(args, "repository"),
		argInt(args, "number", 0))
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{
		"repository":   repo.FullName,
		"pull_request": pr,
	}, "")
}

/* ── result shaping ──────────────────────────────────────────────────── */

// fit bounds a tool result and, when it has to cut, says so IN the result.
//
// ── Why the ceiling is enforced here and not left to Agents ────────────
// Agents already refuses a result above 32 KiB — but it refuses it as an
// EXECUTION FAILURE, which the model reads as "the tool is broken" and
// responds to by apologising rather than by asking for less. That is the
// wrong lesson from the right fact. So the tools bound themselves lower and
// return a smaller, honest answer instead of a failure.
//
// ── Why truncation is declared and never silent ────────────────────────
// A list that was cut and does not say so is indistinguishable from a
// complete one. The model would answer "there are 4 open pull requests"
// when there are forty, with no way for anyone to notice. So a cut result
// carries `truncated: true` and a note that tells the model what to do
// about it — which is the only form of truncation that leaves the
// conversation able to recover.
//
// `listKey` names the field to shrink; empty means the payload has no list
// to shrink and can only be reported as over budget.
func fit(out map[string]any, listKey string) (chatdomain.ToolOutput, error) {
	if size(out) <= domain.MaxToolResultBytes {
		return out, nil
	}
	items, ok := out[listKey].([]any)
	if !ok {
		items = toAnySlice(out[listKey])
	}
	if listKey == "" || items == nil {
		// Nothing to shrink. Refusing is better than returning a payload
		// Agents will reject with a less useful message.
		return nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"the result was larger than one tool call may carry; ask for something narrower")
	}

	// Halve until it fits. Binary rather than one-at-a-time because a
	// hundred-element list of large objects would otherwise cost a hundred
	// serialisations to discover it needs ten.
	kept := len(items)
	for kept > 0 {
		kept /= 2
		out[listKey] = items[:kept]
		out["truncated"] = true
		out["truncated_note"] = "the full result did not fit in one tool call; " +
			"only the first results are shown. Ask for fewer, or narrow the request."
		if size(out) <= domain.MaxToolResultBytes {
			return out, nil
		}
	}
	return nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
		"even a single result was larger than one tool call may carry; "+
			"ask for something narrower")
}

// fitText cuts a file's text until the whole payload fits the budget.
//
// It mutates the FileContent in place, which is safe because the value was
// built for this one call and nothing else holds it. `Size` is deliberately
// left alone: it is the file's real size, and it is what lets the model say
// how much it did not see.
//
// The cut is on a rune boundary, so the result is still valid UTF-8 and
// still encodes — a half rune would make json.Marshal emit a replacement
// character, which is a silent corruption of source code.
func fitText(out map[string]any, file *ports.FileContent) {
	for size(out) > domain.MaxToolResultBytes && len(file.Text) > 0 {
		cut := len(file.Text) / 2
		for cut > 0 && !utf8.RuneStart(file.Text[cut]) {
			cut--
		}
		file.Text = file.Text[:cut]
		file.Truncated = true
	}
}

func size(v any) int {
	raw, err := json.Marshal(v)
	if err != nil {
		// An unmarshalable payload is over any budget: reporting it as
		// oversized routes it into the same honest refusal.
		return domain.MaxToolResultBytes + 1
	}
	return len(raw)
}

// toAnySlice re-boxes a typed slice so fit can shrink it without knowing
// its element type. Reflection would be shorter and less obvious; a
// round trip through JSON is neither cheap nor correct for time values, so
// the handful of concrete types the tools actually pass are named.
func toAnySlice(v any) []any {
	switch s := v.(type) {
	case []any:
		return s
	case []string:
		out := make([]any, len(s))
		for i := range s {
			out[i] = s[i]
		}
		return out
	case []map[string]any:
		out := make([]any, len(s))
		for i := range s {
			out[i] = s[i]
		}
		return out
	case []ports.Commit:
		out := make([]any, len(s))
		for i := range s {
			out[i] = s[i]
		}
		return out
	case []ports.PullRequest:
		out := make([]any, len(s))
		for i := range s {
			out[i] = s[i]
		}
		return out
	case []ports.CodeSearchHit:
		out := make([]any, len(s))
		for i := range s {
			out[i] = s[i]
		}
		return out
	default:
		return nil
	}
}

/* ── argument readers ────────────────────────────────────────────────── */

// argString and argInt read values the schema already validated.
//
// The comma-ok form is kept even though a declared string property either
// decoded into a string or the call never got here — for the same reason
// echoTool keeps it: "safe because somewhere else checked" is exactly the
// assumption that stops being true.
func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
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
