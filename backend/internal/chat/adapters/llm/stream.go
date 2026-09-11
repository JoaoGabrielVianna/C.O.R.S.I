package llm

import (
	"bufio"
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Server-Sent Events, as the completions API uses them: one `data:` field
// per frame, frames separated by blank lines, terminated by the literal
// sentinel `data: [DONE]`. Lines beginning with `:` are keep-alive comments
// and other field names (`event:`, `id:`) are unused by this protocol.
const (
	sseDataPrefix = "data:"
	sseDoneToken  = "[DONE]"

	// A single delta is normally a few dozen bytes, but a provider that
	// batches under load can emit a much larger frame. 1 MiB is far above
	// anything legitimate and still bounds a hostile or broken endpoint.
	maxFrameBytes = 1 << 20
)

type sseStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	done    bool
	// pending accumulates streamed tool-call fragments by their `index`.
	//
	// ── Why assembly happens here ──────────────────────────────────────
	// Fragmentation is a property of the OpenAI streaming protocol, and this
	// file is the only place allowed to know about that protocol. If the
	// port emitted fragments, every caller would have to reimplement this
	// loop — and the application layer would be parsing a wire format
	// through an interface designed to hide it.
	//
	// The stream is read by exactly one goroutine (see SendMessage), so this
	// state needs no synchronisation.
	pending map[int]*partialToolCall
	// emitted guards against sending the assembled calls twice, which would
	// otherwise happen on a gateway that puts finish_reason and the usage
	// frame in separate events.
	emitted bool
}

// partialToolCall is one tool call under construction.
type partialToolCall struct {
	id   string
	name string
	args strings.Builder
}

func newSSEStream(body io.ReadCloser) *sseStream {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	return &sseStream{body: body, scanner: sc, pending: map[int]*partialToolCall{}}
}

// absorbToolCalls folds one chunk's fragments into the accumulator.
//
// Fields are appended rather than overwritten only where the protocol
// fragments them. The id and the name arrive once, on the opening fragment;
// the arguments arrive in slices and are concatenated. Overwriting the name
// on a later fragment would blank it, because later fragments carry `""`.
func (s *sseStream) absorbToolCalls(chunk *completionChunk) {
	if len(chunk.Choices) == 0 {
		return
	}
	for _, frag := range chunk.Choices[0].Delta.ToolCalls {
		p, ok := s.pending[frag.Index]
		if !ok {
			p = &partialToolCall{}
			s.pending[frag.Index] = p
		}
		if frag.ID != "" {
			p.id = frag.ID
		}
		if frag.Function.Name != "" {
			p.name = frag.Function.Name
		}
		p.args.WriteString(frag.Function.Arguments)
	}
}

// assembled returns the completed calls, in the index order the model asked
// for them, and empties the accumulator.
//
// A fragment group with no name is dropped: it cannot be resolved to a tool
// and would reach the application layer as a call for the empty tool, which
// is a worse failure than a call that never arrived. The id may legitimately
// be absent on a gateway that omits it; that is the application layer's
// problem to report, not this one's to invent a value for.
func (s *sseStream) assembled() []domain.ToolCall {
	if len(s.pending) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(s.pending))
	for i := range s.pending {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)

	out := make([]domain.ToolCall, 0, len(indexes))
	for _, i := range indexes {
		p := s.pending[i]
		if p.name == "" {
			continue
		}
		out = append(out, domain.ToolCall{
			ID:        p.id,
			Name:      fromWireName(p.name),
			Arguments: p.args.String(),
		})
	}
	s.pending = map[int]*partialToolCall{}
	return out
}

// Recv returns the next meaningful event, skipping keep-alives, blank
// separators, and frames that carry nothing we act on. It returns io.EOF
// once the provider sends [DONE] or closes the connection.
func (s *sseStream) Recv() (ports.StreamEvent, error) {
	if s.done {
		return ports.StreamEvent{}, io.EOF
	}

	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue // separator or keep-alive comment
		}
		if !strings.HasPrefix(line, sseDataPrefix) {
			continue // `event:` / `id:` — not part of this protocol
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, sseDataPrefix))
		if payload == sseDoneToken {
			s.done = true
			return s.finalEvent()
		}

		var chunk completionChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// One malformed frame should not discard an answer that is
			// otherwise streaming fine. Skip it and keep reading.
			continue
		}

		ev := ports.StreamEvent{}
		// Fold the fragments in before anything else looks at the chunk: a
		// gateway is free to put the last argument slice and the terminal
		// reason in the same frame, and the assembled call has to include it.
		s.absorbToolCalls(&chunk)

		if len(chunk.Choices) > 0 {
			ev.Delta = chunk.Choices[0].Delta.Content
			ev.Reasoning = chunk.reasoningText()
			if fr := chunk.Choices[0].FinishReason; fr != nil {
				ev.FinishReason = *fr
			}
		}
		// The terminal reason is where a turn's tool calls become complete.
		// Emitted once, with that event, so the caller receives whole calls
		// and never a partial one.
		if ev.FinishReason != "" && !s.emitted {
			if calls := s.assembled(); len(calls) > 0 {
				ev.ToolCalls = calls
				s.emitted = true
			}
		}
		// The usage frame arrives after the last content frame and is a
		// separate event, not an annotation on the final delta.
		//
		// This used to say the frame "has an empty choices array". X1
		// showed that is not what a real LiteLLM sends: the usage frame
		// still carries `choices:[{"index":0,"delta":{}}]`. The code was
		// already right — it keys off `usage` being present, not off
		// choices being absent — but the comment would have led the next
		// reader to a change that silently zeroes every token count. See
		// TestRealLiteLLMUsageFrameSurvivesItsChoice.
		if chunk.Usage != nil {
			// Translated whole, including the cache and reasoning counts.
			// This used to keep two fields and drop the rest — see
			// wireUsage for why absence had to survive the translation.
			ev.Usage = chunk.Usage.toPortsUsage()
		}
		if ev.Delta == "" && ev.Reasoning == "" && ev.FinishReason == "" &&
			ev.Usage == nil && len(ev.ToolCalls) == 0 {
			continue // role-only opening frame, or an empty heartbeat
		}
		return ev, nil
	}

	s.done = true
	if err := s.scanner.Err(); err != nil {
		return ports.StreamEvent{}, domain.Upstream("the LLM stream broke: " + cleanTransportErr(err))
	}
	// Clean close without [DONE]. Treated as a normal end of stream: the
	// text received so far is a real answer and the caller will record it.
	return s.finalEvent()
}

// finalEvent closes the stream, delivering any tool calls that were fully
// fragmented but never sealed by a terminal reason.
//
// A gateway that streams tool calls and then ends without `finish_reason`
// is out of spec, and dropping the calls would turn that into a turn that
// silently did nothing. Delivering them with an explicit `tool_calls`
// reason is the honest reading of what arrived. When there is nothing
// pending — the ordinary case, and every case a compliant gateway
// produces — this is io.EOF exactly as before.
func (s *sseStream) finalEvent() (ports.StreamEvent, error) {
	if s.emitted {
		return ports.StreamEvent{}, io.EOF
	}
	calls := s.assembled()
	if len(calls) == 0 {
		return ports.StreamEvent{}, io.EOF
	}
	s.emitted = true
	return ports.StreamEvent{FinishReason: string(domain.FinishToolCalls), ToolCalls: calls}, nil
}

func (s *sseStream) Close() error {
	s.done = true
	return s.body.Close()
}
