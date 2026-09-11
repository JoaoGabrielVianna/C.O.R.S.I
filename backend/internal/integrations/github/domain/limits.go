package domain

// Every ceiling this integration enforces, in one file.
//
// ── Why they are collected here ────────────────────────────────────────
// Because a limit that lives next to the code that applies it is a limit
// nobody can audit as a set. The question an operator asks is "how much of
// a turn can GitHub consume?", and answering it should not require reading
// seven files. It is also the only way to see that the per-tool budget sits
// safely below the one Agents enforces, which is the constraint that
// actually matters.
//
// ── The one that governs the rest ──────────────────────────────────────
// Agents refuses a tool result above 32 KiB (chat/domain.MaxToolResultBytes)
// and reports it as an execution failure — a failure the model reads as
// "the tool broke" rather than "ask for less". So these tools must never
// reach that ceiling: they bound themselves lower, and when they do, they
// SAY SO in the payload. A truncation the model cannot see is a truncation
// it will answer as though it were the whole story.

const (
	// MaxToolResultBytes is the budget one GitHub tool result may occupy.
	//
	// Twenty-four kibibytes, against the thirty-two Agents allows. The gap
	// is deliberate: the tools measure their own payload before the
	// envelope, the field names, and the truncation notes are added, and a
	// budget equal to the hard ceiling would turn a rounding difference into
	// a failed call.
	MaxToolResultBytes = 24 << 10

	// MaxRepositoriesListed bounds `github.repository.list`.
	//
	// The authorized set is chosen by a human through a UI; fifty is far
	// past the number anyone curates by hand, and the tool declares the
	// total either way.
	MaxRepositoriesListed = 50

	// MaxAccessibleRepositories bounds what the settings page fetches from
	// GitHub to offer for selection. Higher than the authorized ceiling
	// because this is the set being chosen FROM, and an account with two
	// hundred repositories is ordinary.
	MaxAccessibleRepositories = 300

	// MaxCommitsPerCall bounds a history listing, and DefaultCommits is what
	// a call that does not ask gets. Twenty is a screenful of history and
	// roughly two kilobytes; fifty is the ceiling for "show me more".
	DefaultCommits     = 20
	MaxCommitsPerCall  = 50
	MaxCommitMessage   = 400
	MaxCommitFiles     = 50
	MaxCommitPatchByte = 12 << 10

	// MaxSearchResults bounds a code search.
	//
	// Ten, and it is the smallest ceiling here on purpose. Search results
	// carry code fragments, which are the most expensive thing per result
	// this integration returns, and the point of a search is to pick a file
	// to open rather than to read everything at once. GitHub's own code
	// search limit is ten requests per minute, so a wider net would also be
	// the fastest way to spend the rate budget.
	MaxSearchResults = 10
	// MaxSearchFragments and MaxSearchFragmentBytes bound what one hit shows
	// of the matching lines.
	MaxSearchFragments     = 3
	MaxSearchFragmentBytes = 400
	// MaxSearchQueryLength bounds the query string handed to GitHub.
	MaxSearchQueryLength = 256

	// MaxFileBytes bounds one file read.
	//
	// Sixty-four kibibytes of source is around fifteen hundred lines, more
	// than a model reads usefully in one call and comfortably inside the
	// tool budget once base64 and JSON escaping are paid for. A file above
	// it is returned truncated and says so.
	MaxFileBytes = 64 << 10
	// MaxFilePathLength bounds the path a caller may ask for.
	MaxFilePathLength = 400

	// MaxPullRequestsPerCall and DefaultPullRequests bound a PR listing.
	DefaultPullRequests = 20
	MaxPullRequests     = 50
	// MaxPullRequestBody bounds the description a detail read returns. PR
	// bodies contain templates, checklists and screenshots-as-links; four
	// kilobytes is the useful part of almost all of them.
	MaxPullRequestBody = 4 << 10

	// MaxRefLength bounds a branch, tag or sha a caller may name. GitHub's
	// own ref limit is not documented as a number; 255 matches the
	// default_branch column and is far past any real branch name.
	MaxRefLength = 255
)

// PageSize is what the adapter asks GitHub for per page.
//
// One hundred is GitHub's documented maximum. Asking for the maximum means
// every ceiling above is reached in the fewest possible round trips, which
// matters because the rate limit is counted in requests and not in bytes.
const PageSize = 100
