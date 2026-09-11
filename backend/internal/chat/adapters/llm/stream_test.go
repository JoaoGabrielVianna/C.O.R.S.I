package llm

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// drain reads a stream to EOF, returning the concatenated text plus the
// last finish reason and usage seen.
func drain(t *testing.T, raw string) (text, finish string, usage *struct{ P, C int }) {
	t.Helper()
	s := newSSEStream(io.NopCloser(strings.NewReader(raw)))
	defer func() { _ = s.Close() }()

	var sb strings.Builder
	for {
		ev, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		sb.WriteString(ev.Delta)
		if ev.FinishReason != "" {
			finish = ev.FinishReason
		}
		if ev.Usage != nil {
			usage = &struct{ P, C int }{ev.Usage.PromptTokens, ev.Usage.CompletionTokens}
		}
	}
	return sb.String(), finish, usage
}

func TestStreamHappyPath(t *testing.T) {
	raw := `data: {"choices":[{"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"choices":[{"delta":{"content":"Oi"},"finish_reason":null}]}

data: {"choices":[{"delta":{"content":", João"},"finish_reason":null}]}

data: {"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5}}

data: [DONE]

`
	text, finish, usage := drain(t, raw)
	if text != "Oi, João" {
		t.Fatalf("text = %q", text)
	}
	if finish != "stop" {
		t.Fatalf("finish = %q", finish)
	}
	if usage == nil || usage.P != 12 || usage.C != 5 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestStreamSkipsKeepAlivesAndGarbage(t *testing.T) {
	raw := `: ping

data: {"choices":[{"delta":{"content":"a"}}]}

data: {not json

event: something

data: {"choices":[{"delta":{"content":"b"}}]}

data: [DONE]
`
	text, _, _ := drain(t, raw)
	if text != "ab" {
		t.Fatalf("text = %q, want %q", text, "ab")
	}
}

func TestStreamEndsWithoutDoneToken(t *testing.T) {
	// A provider that just closes the socket after the last content frame.
	// The text received is still a valid answer.
	raw := `data: {"choices":[{"delta":{"content":"parcial"}}]}
`
	text, _, _ := drain(t, raw)
	if text != "parcial" {
		t.Fatalf("text = %q", text)
	}
}

func TestRecvAfterDoneKeepsReturningEOF(t *testing.T) {
	s := newSSEStream(io.NopCloser(strings.NewReader("data: [DONE]\n")))
	for i := 0; i < 3; i++ {
		if _, err := s.Recv(); !errors.Is(err, io.EOF) {
			t.Fatalf("recv #%d = %v, want EOF", i, err)
		}
	}
}

// Reasoning reaches us under three different field names. Each is read,
// and a gateway that sends more than one for the same chunk must not have
// its text counted twice.
func TestStreamReasoningFieldVariants(t *testing.T) {
	collect := func(t *testing.T, raw string) (reasoning, answer string) {
		t.Helper()
		s := newSSEStream(io.NopCloser(strings.NewReader(raw)))
		defer func() { _ = s.Close() }()
		var r, a strings.Builder
		for {
			ev, err := s.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("recv: %v", err)
			}
			r.WriteString(ev.Reasoning)
			a.WriteString(ev.Delta)
		}
		return r.String(), a.String()
	}

	cases := []struct {
		name string
		raw  string
	}{
		{
			"deepseek reasoning_content",
			`data: {"choices":[{"delta":{"reasoning_content":"deixa eu pensar"}}]}

data: {"choices":[{"delta":{"content":"resposta"}}]}

data: [DONE]
`,
		},
		{
			"short reasoning alias",
			`data: {"choices":[{"delta":{"reasoning":"deixa eu pensar"}}]}

data: {"choices":[{"delta":{"content":"resposta"}}]}

data: [DONE]
`,
		},
		{
			"anthropic thinking_blocks",
			`data: {"choices":[{"delta":{"thinking_blocks":[{"type":"thinking","thinking":"deixa eu "},{"type":"thinking","thinking":"pensar"}]}}]}

data: {"choices":[{"delta":{"content":"resposta"}}]}

data: [DONE]
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reasoning, answer := collect(t, tc.raw)
			if reasoning != "deixa eu pensar" {
				t.Errorf("reasoning = %q", reasoning)
			}
			if answer != "resposta" {
				t.Errorf("answer = %q", answer)
			}
		})
	}

	t.Run("both fields on one chunk are not double counted", func(t *testing.T) {
		raw := `data: {"choices":[{"delta":{"reasoning_content":"pensando","reasoning":"pensando"}}]}

data: [DONE]
`
		reasoning, _ := collect(t, raw)
		if reasoning != "pensando" {
			t.Fatalf("reasoning = %q, want it counted once", reasoning)
		}
	})

	t.Run("reasoning-only chunk is not skipped as empty", func(t *testing.T) {
		raw := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"só pensamento\"}}]}\n\ndata: [DONE]\n"
		reasoning, answer := collect(t, raw)
		if reasoning != "só pensamento" || answer != "" {
			t.Fatalf("reasoning = %q, answer = %q", reasoning, answer)
		}
	})
}

func TestExtractErrorMessage(t *testing.T) {
	cases := map[string]string{
		`{"error":{"message":"invalid api key","type":"auth"}}`: "invalid api key",
		`{"error":"model not found"}`:                           "model not found",
		`{"detail":"rate limited"}`:                             "rate limited",
		`plain text failure`:                                    "plain text failure",
	}
	for raw, want := range cases {
		if got := extractErrorMessage([]byte(raw)); got != want {
			t.Errorf("extractErrorMessage(%s) = %q, want %q", raw, got, want)
		}
	}
}
