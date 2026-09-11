// Package llm is the driven adapter that talks to an OpenAI-compatible
// completions endpoint. LiteLLM is the intended target, but nothing here is
// LiteLLM-specific: the contract is `POST /chat/completions` with SSE
// streaming and `GET /models`, which every OpenAI-shaped gateway speaks.
//
// Written against net/http on purpose. The wire format is a JSON body and a
// line-oriented event stream; pulling in a vendor SDK would add a
// dependency, a release cadence, and an opinion about which provider we are
// allowed to point at, in exchange for roughly two hundred lines.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Timeouts. The overall http.Client timeout is deliberately zero: it would
// cap the whole response body, and a streamed answer is a body that stays
// open for as long as the model keeps talking. Deadlines that *should*
// apply are enforced instead by ResponseHeaderTimeout (the provider must
// start answering promptly) and by the caller's context (which bounds the
// stream as a whole).
const (
	responseHeaderTimeout = 60 * time.Second
	modelsTimeout         = 15 * time.Second
	// upstreamErrBodyLimit bounds how much of a provider error body we read
	// before giving up; these are meant to be short JSON documents, and an
	// endpoint that returns an HTML error page should not fill a log line.
	upstreamErrBodyLimit = 4 << 10
)

type Client struct {
	http *http.Client
	log  *slog.Logger
}

func New(log *slog.Logger) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	// Streamed responses are read incrementally; compression would buffer.
	transport.DisableCompression = true
	return &Client{
		http: &http.Client{Transport: transport},
		log:  log,
	}
}

// --- wire types -----------------------------------------------------------

type completionRequest struct {
	Model         string         `json:"model"`
	Messages      []wireMessage  `json:"messages"`
	Temperature   float32        `json:"temperature"`
	MaxTokens     int            `json:"max_tokens"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	// user + metadata are spend-attribution only; the gateway strips
	// metadata before forwarding upstream, and both are omitted when empty.
	User     string            `json:"user,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	// Tools is omitted entirely when the agent has none, which is what makes
	// the body byte-identical to the one X1 verified against a real gateway.
	// `tool_choice` is deliberately absent: the default is "auto", which is
	// what we want, and sending a field to restate a default is a field that
	// can be wrong.
	Tools []wireTool `json:"tools,omitempty"`
}

// wireMessage is ports.ChatMessage in the shape the protocol wants.
//
// The translation exists for two fields now. A tool call on the wire is
// `{"id":…,"type":"function","function":{"name":…,"arguments":…}}`, which is
// not the flat value the rest of the system reasons about; and `content` is
// either a bare string or an array of blocks, depending on whether the
// message carries a cache breakpoint. Both are shapes of an outbound
// request, which is this adapter's business and not the domain's.
//
// `content` keeps no omitempty. An assistant message that only asks for a
// tool has empty content and must still carry the key, and the field was
// unconditional in the body X1 verified.
type wireMessage struct {
	Role       string         `json:"role"`
	Content    wireContent    `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// wireContent is a message body that can be either shape the protocol
// allows.
//
// ── Why a custom marshaller and not `any` ──────────────────────────────
// `cache_control` is a property of a content BLOCK. There is nowhere to put
// it on a bare JSON string, which is why the field used to be `string` and
// why prompt caching was not merely unconfigured but inexpressible.
//
// The obvious fix — make the field `any` and assign either a string or a
// slice — spreads the decision across every construction site and makes the
// zero value marshal as `null`. This type keeps the decision in one place
// and, more importantly, makes the DEFAULT byte-identical to what the
// adapter has always sent:
//
//	CacheBreakpoint false  →  "content": "texto"        ← unchanged
//	CacheBreakpoint true   →  "content": [{"type":"text",…,"cache_control":…}]
//
// That equality is not a nicety. It is what lets the semantic-equivalence
// proof be a statement about bytes rather than an argument about intent:
// with caching off, or on any message that is not the breakpoint, the
// request body is the one the external boundary was verified against.
type wireContent struct {
	Text string
	// CacheBreakpoint marks this message as the END of a cacheable prefix.
	// Everything up to and including it may be served from cache on a later
	// request whose prefix matches byte for byte.
	CacheBreakpoint bool
}

func (c wireContent) MarshalJSON() ([]byte, error) {
	// ── An empty body is never blocked ──────────────────────────────
	//
	// An assistant turn that only asked for a tool has empty content, and
	// it must still carry the key — that is why the field has no omitempty
	// and why TestAssistantToolCallKeepsAnEmptyContentKey exists. Wrapping
	// that empty string in a text block would send `{"type":"text",
	// "text":""}`, which several gateways reject outright.
	//
	// The case should not arise: a breakpoint marks the end of a STABLE
	// prefix, and a tool call is the least stable thing in a turn. It is
	// handled anyway, and handled by dropping the MARKER rather than the
	// content, because losing a cache entry costs money and sending a
	// malformed block costs the turn.
	if !c.CacheBreakpoint || c.Text == "" {
		return json.Marshal(c.Text)
	}
	return json.Marshal([]wireContentBlock{{
		Type:         "text",
		Text:         c.Text,
		CacheControl: &wireCacheControl{Type: cacheControlEphemeral},
	}})
}

// wireContentBlock is one element of the array form of `content`.
type wireContentBlock struct {
	Type         string            `json:"type"`
	Text         string            `json:"text"`
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}

// wireCacheControl is the breakpoint marker itself.
//
// `ephemeral` is the only type the API defines, and the default TTL is five
// minutes. The TTL is deliberately NOT sent: the one-hour variant costs
// twice as much to write instead of 1.25x, and choosing it is a decision
// that has to be made against measured traffic rather than assumed here.
// Adding a field later is additive; unwinding a doubled write price that
// nobody measured is not.
type wireCacheControl struct {
	Type string `json:"type"`
}

const cacheControlEphemeral = "ephemeral"

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireCallFunction `json:"function"`
}

type wireCallFunction struct {
	Name string `json:"name"`
	// Arguments is a JSON *string* containing JSON, which is the protocol's
	// own choice and not ours. Passed through exactly as the model produced
	// it when we echo a call back.
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  wireParameters `json:"parameters"`
}

// wireParameters is the JSON Schema the model is handed.
//
// It is assembled here rather than marshalled from domain.ToolSchema
// because the two are different documents: the domain type is the contract
// this system validates against, and this one is JSON Schema draft
// vocabulary, with the `type: object` and `additionalProperties: false`
// that make the model's own constrained decoding behave.
type wireParameters struct {
	Type                 string                  `json:"type"`
	Properties           map[string]wireProperty `json:"properties"`
	Required             []string                `json:"required,omitempty"`
	AdditionalProperties bool                    `json:"additionalProperties"`
}

type wireProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	MaxLength   int    `json:"maxLength,omitempty"`
}

// toWireMessages translates the request's message list.
func toWireMessages(messages []ports.ChatMessage) []wireMessage {
	out := make([]wireMessage, len(messages))
	for i, m := range messages {
		out[i] = wireMessage{
			Role: m.Role,
			// The breakpoint is decided by the Context Builder, which is the
			// only layer that knows which part of a prompt is stable. This
			// adapter only knows how to write it down.
			Content:    wireContent{Text: m.Content, CacheBreakpoint: m.CacheBreakpoint},
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) == 0 {
			continue
		}
		calls := make([]wireToolCall, len(m.ToolCalls))
		for j, c := range m.ToolCalls {
			calls[j] = wireToolCall{
				ID:   c.ID,
				Type: "function",
				Function: wireCallFunction{
					Name:      toWireName(c.Name),
					Arguments: c.Arguments,
				},
			}
		}
		out[i].ToolCalls = calls
	}
	return out
}

// toWireTools translates the declaration list.
func toWireTools(defs []domain.ToolDefinition) []wireTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]wireTool, len(defs))
	for i, d := range defs {
		props := make(map[string]wireProperty, len(d.Schema.Properties))
		for name, p := range d.Schema.Properties {
			props[name] = wireProperty{
				Type:        string(p.Type),
				Description: p.Description,
				MaxLength:   p.MaxLength,
			}
		}
		out[i] = wireTool{
			Type: "function",
			Function: wireFunction{
				Name:        toWireName(d.Name),
				Description: d.DeclaredDescription(),
				Parameters: wireParameters{
					Type:                 "object",
					Properties:           props,
					Required:             d.Schema.Required,
					AdditionalProperties: false,
				},
			},
		}
	}
	return out
}

// ── the name encoding ──────────────────────────────────────────────────
//
// Canonical tool names are dotted (`system.echo`). The OpenAI-compatible
// function-calling spec documents the allowed character set for a function
// name as letters, digits, underscores and dashes — a dot is not in it.
// Many gateways accept one anyway; this one does NOT, and that is now
// observed rather than assumed. Against a real LiteLLM, the same request
// differing only in the function name gives:
//
//	system__echo → HTTP 200
//	system.echo  → HTTP 400 "Invalid 'tools[0].function.name': string does
//	               not match pattern. Expected a string that matches the
//	               pattern '^[a-zA-Z0-9_-]+$'"
//
// So this encoding is load bearing: remove it and every tool turn fails at
// the gateway. TestLiveToolNameEncoding records the real gateway's answer
// instead of assuming it.
//
// So the wire name is the canonical name with `.` written as `__`, and the
// mapping is reversed the moment a call comes back. It is confined to this
// file: nothing above the adapter — not the store, not the audit trail, not
// the interface — ever sees the encoded form.
//
// It is unambiguous because domain.ValidToolName rejects `__` inside a
// segment, which is the only way the reverse mapping could be wrong.
func toWireName(n domain.ToolName) string {
	return strings.ReplaceAll(string(n), ".", "__")
}

func fromWireName(s string) domain.ToolName {
	return domain.ToolName(strings.ReplaceAll(s, "__", "."))
}

// streamOptions.include_usage asks the provider to emit a final frame
// carrying token counts. Without it the stream ends with no usage at all
// and every turn records zero tokens.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// Reasoning arrives under three different field names depending on who is
// answering, and LiteLLM passes each through as it finds it:
//
//	reasoning_content — DeepSeek's native field, and what LiteLLM
//	                    normalizes most providers onto
//	reasoning         — the shorter alias some gateways emit instead
//	thinking_blocks   — Anthropic's structured form, forwarded verbatim
//
// All three are read, in that order of preference, so a chunk is never
// counted twice when a gateway helpfully sends both.
type completionChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ThinkingBlocks   []struct {
				Type     string `json:"type"`
				Thinking string `json:"thinking"`
			} `json:"thinking_blocks"`
			// A streamed tool call arrives in pieces. The first fragment for
			// a given index carries the id and the function name; the ones
			// after it carry successive slices of the argument JSON, and only
			// the concatenation of all of them is parseable. `index` is what
			// ties the pieces together when the model asks for more than one
			// tool at once — it is the only field guaranteed on every
			// fragment, which is why the accumulator keys on it and not on
			// the id.
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
}

// wireUsage is the usage frame, in full.
//
// ── Why every optional field is a pointer ──────────────────────────────
// Because `encoding/json` leaves an absent number at zero, and a zero is a
// measurement. A gateway that does not report cache counts and a call that
// read nothing from cache would produce identical structs, and every
// cache-effectiveness figure computed from them would quietly average in
// calls that were never measured. Pointers keep ABSENT and ZERO apart from
// the wire all the way to the database column. See ports.Usage.
//
// ── Why the same fact is read from two places ──────────────────────────
// A real LiteLLM reports cache counts in two vocabularies at once — see the
// frame captured in litellm_real_test.go. `cache_read_input_tokens` is
// Anthropic's own field forwarded verbatim; `prompt_tokens_details.
// cached_tokens` is the OpenAI-shaped restatement of it. Both are read, both
// are kept, and neither is derived from the other: whether this deployment
// makes them agree is a question for the X-test to answer with evidence, not
// one for this struct to settle by assumption.
//
// PromptTokens and CompletionTokens stay plain ints. A frame that omitted
// them is not a usage frame, and the caller already treats a nil Usage as
// "the provider said nothing".
type wireUsage struct {
	PromptTokens     int  `json:"prompt_tokens"`
	CompletionTokens int  `json:"completion_tokens"`
	TotalTokens      *int `json:"total_tokens"`

	// Anthropic's fields, forwarded by the gateway at the top level.
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`

	PromptTokensDetails *struct {
		CachedTokens *int `json:"cached_tokens"`
		// The gateway has been observed emitting both spellings for the
		// write side. Read both; prefer neither over the top-level field.
		CacheCreationTokens *int `json:"cache_creation_tokens"`
		CacheWriteTokens    *int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`

	CompletionTokensDetails *struct {
		ReasoningTokens *int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// toPortsUsage translates one usage frame, preserving absence.
//
// ── The precedence, stated once ────────────────────────────────────────
// For a fact reported in more than one place, the Anthropic-named top-level
// field wins. It is the provider's own number passed through, while the
// `*_details` restatement is the gateway's translation of it — and when a
// translation and its original disagree, the original is the one that
// matches the invoice.
//
// A field absent from every source stays nil. Nothing here invents a zero.
func (u *wireUsage) toPortsUsage() *ports.Usage {
	if u == nil {
		return nil
	}
	out := &ports.Usage{
		PromptTokens:        u.PromptTokens,
		CompletionTokens:    u.CompletionTokens,
		TotalTokens:         u.TotalTokens,
		CacheReadTokens:     u.CacheReadInputTokens,
		CacheCreationTokens: u.CacheCreationInputTokens,
	}
	if d := u.PromptTokensDetails; d != nil {
		out.CachedTokens = d.CachedTokens
		if out.CacheReadTokens == nil {
			out.CacheReadTokens = d.CachedTokens
		}
		if out.CacheCreationTokens == nil {
			out.CacheCreationTokens = firstNonNil(d.CacheCreationTokens, d.CacheWriteTokens)
		}
	}
	if d := u.CompletionTokensDetails; d != nil {
		out.ReasoningTokens = d.ReasoningTokens
	}
	return out
}

func firstNonNil(vals ...*int) *int {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

// reasoningText picks whichever reasoning field this chunk actually used.
func (c *completionChunk) reasoningText() string {
	if len(c.Choices) == 0 {
		return ""
	}
	d := &c.Choices[0].Delta
	if d.ReasoningContent != "" {
		return d.ReasoningContent
	}
	if d.Reasoning != "" {
		return d.Reasoning
	}
	var sb strings.Builder
	for _, b := range d.ThinkingBlocks {
		sb.WriteString(b.Thinking)
	}
	return sb.String()
}

type modelsResponse struct {
	Data []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

// --- Stream ---------------------------------------------------------------

func (c *Client) Stream(ctx context.Context, req ports.CompletionRequest) (ports.Stream, error) {
	body, err := json.Marshal(completionRequest{
		Model:         req.Model,
		Messages:      toWireMessages(req.Messages),
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		User:          req.User,
		Metadata:      req.Metadata,
		Tools:         toWireTools(req.Tools),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal completion request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, http.MethodPost, req.Creds, "/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	res, err := c.http.Do(httpReq)
	if err != nil {
		return nil, domain.Upstream("could not reach the LLM provider: " + cleanTransportErr(err))
	}
	if res.StatusCode != http.StatusOK {
		defer func() { _ = res.Body.Close() }()
		return nil, upstreamStatusErr(res)
	}
	return newSSEStream(res.Body), nil
}

// --- Models ---------------------------------------------------------------

func (c *Client) Models(ctx context.Context, creds ports.Credentials) ([]ports.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()

	httpReq, err := c.newRequest(ctx, http.MethodGet, creds, "/models", nil)
	if err != nil {
		return nil, err
	}

	res, err := c.http.Do(httpReq)
	if err != nil {
		return nil, domain.Upstream("could not reach the LLM provider: " + cleanTransportErr(err))
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return nil, upstreamStatusErr(res)
	}

	var parsed modelsResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&parsed); err != nil {
		return nil, domain.Upstream("the provider returned a /models response we could not parse")
	}

	models := make([]ports.Model, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, ports.Model{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	return models, nil
}

// --- Prices ---------------------------------------------------------------

type modelInfoResponse struct {
	Data []struct {
		ModelName string `json:"model_name"`
		ModelInfo struct {
			InputCostPerToken  float64 `json:"input_cost_per_token"`
			OutputCostPerToken float64 `json:"output_cost_per_token"`
			// The two cache rates, as the gateway publishes them. Pointers
			// because a model without prompt caching omits them, and an
			// absent rate must not be read as free — see ports.Price.
			//
			// Observed on this deployment for claude-opus-4-7:
			//	input               5,0e-06
			//	cache creation      6,25e-06   (1,25x input)
			//	cache read          5,0e-07    (0,10x input)
			//
			// Read from the card rather than derived from those multipliers.
			// The multipliers are a published convention; the card is what
			// this gateway will actually bill.
			CacheCreationInputTokenCost *float64 `json:"cache_creation_input_token_cost"`
			CacheReadInputTokenCost     *float64 `json:"cache_read_input_token_cost"`
		} `json:"model_info"`
	} `json:"data"`
}

// ModelPrices reads /model/info and returns per-token costs keyed by model
// name. A scoped virtual key sees only the models it may use, which is
// exactly the set we need to price. Models the gateway reports at zero cost
// are dropped so the caller can tell "free" from "unknown".
func (c *Client) ModelPrices(ctx context.Context, creds ports.Credentials) (map[string]ports.Price, error) {
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()

	httpReq, err := c.newRequest(ctx, http.MethodGet, creds, "/model/info", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(httpReq)
	if err != nil {
		return nil, domain.Upstream("could not reach the LLM provider: " + cleanTransportErr(err))
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, upstreamStatusErr(res)
	}

	var parsed modelInfoResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&parsed); err != nil {
		return nil, domain.Upstream("the provider returned a /model/info response we could not parse")
	}
	prices := make(map[string]ports.Price, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ModelName == "" {
			continue
		}
		if m.ModelInfo.InputCostPerToken == 0 && m.ModelInfo.OutputCostPerToken == 0 {
			continue
		}
		prices[m.ModelName] = ports.Price{
			InputCostPerToken:  m.ModelInfo.InputCostPerToken,
			OutputCostPerToken: m.ModelInfo.OutputCostPerToken,
			// Carried through as the card states them, including their
			// absence. A model with no cache pricing prices a cached turn
			// as unknown rather than as free.
			CacheCreationCostPerToken: m.ModelInfo.CacheCreationInputTokenCost,
			CacheReadCostPerToken:     m.ModelInfo.CacheReadInputTokenCost,
		}
	}
	return prices, nil
}

// --- Key spend ------------------------------------------------------------

type keyInfoResponse struct {
	Info struct {
		KeyAlias  string   `json:"key_alias"`
		Spend     float64  `json:"spend"`
		MaxBudget *float64 `json:"max_budget"`
		Models    []string `json:"models"`
	} `json:"info"`
}

// KeyInfo reads /key/info for the key in creds. LiteLLM lets a key read its
// own record even though the aggregate spend logs are admin-only, so this
// is the real billed spend of exactly this token — no master key needed.
func (c *Client) KeyInfo(ctx context.Context, creds ports.Credentials) (ports.KeySpend, error) {
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()

	httpReq, err := c.newRequest(ctx, http.MethodGet, creds, "/key/info", nil)
	if err != nil {
		return ports.KeySpend{}, err
	}
	res, err := c.http.Do(httpReq)
	if err != nil {
		return ports.KeySpend{}, domain.Upstream("could not reach the LLM provider: " + cleanTransportErr(err))
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return ports.KeySpend{}, upstreamStatusErr(res)
	}

	var parsed keyInfoResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&parsed); err != nil {
		return ports.KeySpend{}, domain.Upstream("the provider returned a /key/info response we could not parse")
	}
	return ports.KeySpend{
		KeyAlias:  parsed.Info.KeyAlias,
		Spend:     parsed.Info.Spend,
		MaxBudget: parsed.Info.MaxBudget,
		Models:    parsed.Info.Models,
	}, nil
}

// --- helpers --------------------------------------------------------------

func (c *Client) newRequest(ctx context.Context, method string, creds ports.Credentials, path string, body io.Reader) (*http.Request, error) {
	url := domain.NormalizeBaseURL(creds.BaseURL) + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, domain.Invalid("provider base_url is not usable: " + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+creds.APIKey)
	return req, nil
}

// upstreamStatusErr turns a non-200 into a domain error carrying enough of
// the provider's own message to be actionable ("invalid api key", "model
// not found") without echoing an arbitrarily large body.
func upstreamStatusErr(res *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(res.Body, upstreamErrBodyLimit))
	detail := extractErrorMessage(raw)

	msg := fmt.Sprintf("LLM provider returned %d", res.StatusCode)
	if detail != "" {
		msg += ": " + detail
	}
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg += " (check the API key stored for this provider)"
	case http.StatusNotFound:
		msg += " (check the base URL and the model id)"
	}
	return domain.Upstream(msg)
}

// extractErrorMessage digs the human-readable string out of the several
// error shapes gateways use: {"error":{"message":...}}, {"error":"..."},
// {"detail":"..."}. Falls back to the raw text, trimmed.
func extractErrorMessage(raw []byte) string {
	var probe struct {
		Error  json.RawMessage `json:"error"`
		Detail string          `json:"detail"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil {
		if probe.Detail != "" {
			return truncate(probe.Detail, 300)
		}
		if len(probe.Error) > 0 {
			var nested struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(probe.Error, &nested) == nil && nested.Message != "" {
				return truncate(nested.Message, 300)
			}
			var flat string
			if json.Unmarshal(probe.Error, &flat) == nil && flat != "" {
				return truncate(flat, 300)
			}
		}
	}
	return truncate(strings.TrimSpace(string(raw)), 300)
}

// cleanTransportErr strips the url.Error wrapper so the message we surface
// does not embed the full endpoint URL — which, on a misconfigured
// provider, can carry a key in a query string.
func cleanTransportErr(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && i+2 < len(msg) {
		return msg[i+2:]
	}
	return msg
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
