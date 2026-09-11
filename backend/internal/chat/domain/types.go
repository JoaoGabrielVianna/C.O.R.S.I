// Package domain holds entities, value objects, and domain errors for the
// chat bounded context. It depends only on the stdlib and uuid; nothing here
// imports HTTP, SQL, or any platform/adapter package.
//
// This context owns three things: the credentials for an OpenAI-compatible
// LLM endpoint (LiteLLM in practice), a registry of configured agents, and
// the conversation log. It knows nothing about any other bounded context.
package domain

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

func (r Role) Valid() bool {
	return r == RoleUser || r == RoleAssistant
}

// FinishReason records why a streamed completion stopped. `stop` and
// `length` come from the provider; `aborted` and `error` are ours, for a
// stream that died before the provider said anything conclusive.
type FinishReason string

const (
	FinishStop    FinishReason = "stop"
	FinishLength  FinishReason = "length"
	FinishAborted FinishReason = "aborted"
	FinishError   FinishReason = "error"
	// FinishToolCalls is the provider's own reason for stopping to ask for a
	// tool. It is a mid-turn state, never a stored one: the loop reads it,
	// runs the tools and calls the provider again, so a turn that ends this
	// way ended for one of the reasons below instead.
	FinishToolCalls FinishReason = "tool_calls"
	// FinishToolRoundLimit is ours: the turn asked for tools more times than
	// one turn is allowed to. See app/send.go maxToolRounds.
	FinishToolRoundLimit FinishReason = "tool_round_limit"
)

// UsageSource says how much a turn's token counts can be trusted.
//
// It exists because a token count of zero is ambiguous on its own, and the
// ambiguity is dangerous: read as "this turn was free" it under-reports a
// statement and, later, lets a budget through that should have held. This
// type is the authority on what the numbers mean; the numbers are not.
type UsageSource string

const (
	// UsageUnknown: nothing is known about what this turn consumed. The
	// counts stored beside it are zero only because the column cannot be
	// null. Never read them as a measurement.
	UsageUnknown UsageSource = "unknown"
	// UsageProvider: the gateway reported usage. Exact.
	UsageProvider UsageSource = "provider"
	// UsageEstimated: the gateway reported none, but the request did reach
	// the model, so the counts are EstimateTokens over what was sent and
	// what came back. Approximate, and labelled everywhere it is shown.
	UsageEstimated UsageSource = "estimated"
)

func (s UsageSource) Valid() bool {
	return s == UsageUnknown || s == UsageProvider || s == UsageEstimated
}

// OrUnknown normalises the zero value on the way to storage. A row written
// without an explicit source — a user turn, which is recorded before any
// model runs — carries no measurement, and "unknown" is the honest name for
// that. It keeps the column free of a fourth, undocumented state.
func (s UsageSource) OrUnknown() UsageSource {
	if s == "" {
		return UsageUnknown
	}
	return s
}
