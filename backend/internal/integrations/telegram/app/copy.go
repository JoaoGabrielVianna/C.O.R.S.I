package app

import "fmt"

/* ── everything a Telegram user can be told ──────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	ONE FILE, BECAUSE THIS IS THE UNTRANSLATED SURFACE
//
// ══════════════════════════════════════════════════════════════════════
//
// The platform's i18n Definition of Done is a FRONTEND capability:
// `frontend/src/lib/i18n/`, `useT()`, `useFormat()`, and a build that
// fails on a missing key. None of it exists in Go, and there is no backend
// i18n capability to reach for.
//
// So this surface ships in one language, and that is a gap rather than a
// decision to be proud of. It is confined to this file on purpose: the day
// the platform grows a backend dictionary, the whole of what a Telegram
// user reads is in one place, already separated from the logic that
// chooses which sentence to send. Nothing below is built by string
// concatenation at a call site.
//
// ── The other rule these sentences follow ──────────────────────────────
// A message from here can land on a phone belonging to somebody who is not
// the operator — an unpaired stranger who found the bot. So none of them
// names a workspace, an agent that the reader may not select, an internal
// id, or anything about what this deployment contains. What the operator
// is told and what the log records are produced separately, and the log is
// the one allowed to be specific.

const (
	// The unpaired refusal. It is the same sentence for "never paired",
	// "revoked" and "the binding is for a different Telegram account":
	// distinguishing them would answer questions to whoever asked.
	copyNotPaired = "Este chat não está vinculado a um workspace do C.O.R.S.I.\n\n" +
		"Envie /start para gerar um código de pareamento."

	copyPairingIssued = "Código de pareamento:\n\n" +
		"%s\n\n" +
		"Ele vale por %d minutos e só pode ser usado uma vez.\n\n" +
		"Confirme o pareamento no C.O.R.S.I. Depois é só enviar /agents aqui."

	copyAlreadyPaired = "Este chat já está vinculado.\n\n" +
		"Use /agents para escolher um agente, ou /status para ver a situação."

	copyHelp = "C.O.R.S.I. no Telegram.\n\n" +
		"/agents — escolher o agente com quem falar\n" +
		"/status — situação deste chat\n" +
		"/start — gerar um código de pareamento\n" +
		"/help — esta mensagem\n\n" +
		"Qualquer outra mensagem vai para o agente selecionado, " +
		"na conversa dele. Cada agente tem a sua própria conversa: " +
		"trocar de agente não mistura contexto."

	copyUnknownCommand = "Não conheço esse comando. Use /help."

	// Sent when the chat has no agent selected yet. It is the reason a
	// freshly paired chat does not silently pick one.
	copyNoAgentSelected = "Nenhum agente selecionado neste chat.\n\n" +
		"Use /agents para escolher."

	copyNoAgents = "Este workspace não tem nenhum agente configurado."

	copyPickAgent = "Escolha o agente:"

	copyAgentSelected = "Agora você está falando com %s."

	// The agent that was selected has since disappeared. Stated rather
	// than silently re-selecting something: the operator deleted it, and
	// the product should say so instead of quietly talking to somebody
	// else.
	copyAgentGone = "O agente selecionado não existe mais neste workspace.\n\n" +
		"Use /agents para escolher outro."

	// The concurrency refusal. See app/inflight.go.
	copyBusy = "Ainda estou processando a mensagem anterior deste chat. " +
		"Aguarde a resposta antes de enviar outra."

	// A turn that produced no words at all. The alternative — sending
	// nothing — would leave the operator staring at a chat that swallowed
	// their message.
	copyEmptyAnswer = "O agente terminou o turno sem produzir texto."

	// The generic failure. Concise and specific about nothing: stack
	// traces, database errors, tool payloads and gateway text never reach
	// a phone. The log has the detail.
	copyTurnFailed = "Não consegui completar esse turno. Tente novamente."

	copyResumeButton = "Continuar"

	// Offered under a turn that stopped in a way the runtime can continue.
	copyResumable = "O turno parou antes de terminar."

	// The resume was refused by the runtime — the conversation has moved
	// on, or the turn was already continued. Idempotent refusal is the
	// runtime's behaviour and this is how it reads.
	copyResumeRefused = "Esse turno não pode mais ser continuado."

	copyOnlyPrivate = "Este bot só funciona em conversa privada."
)

// formatPairingIssued renders the code the operator will retype.
func formatPairingIssued(code string, ttlMinutes int) string {
	return fmt.Sprintf(copyPairingIssued, code, ttlMinutes)
}

func formatAgentSelected(name string) string {
	return fmt.Sprintf(copyAgentSelected, name)
}

/* ── the status line, and the two receipt footers ────────────────────── */

// formatStatus describes this chat.
//
// It names the agent and nothing else. No workspace id, no conversation
// id, no model, no token count: a status message is for orienting the
// operator, and every identifier in it is one more thing on a phone screen
// that should not be there.
func formatStatus(paired bool, agentName string, running bool) string {
	if !paired {
		return copyNotPaired
	}
	s := "Vinculado.\n"
	if agentName == "" {
		s += "Agente: nenhum selecionado (use /agents)\n"
	} else {
		s += "Agente: " + agentName + "\n"
	}
	if running {
		s += "Turno em andamento: sim"
	} else {
		s += "Turno em andamento: não"
	}
	return s
}

// formatWriteReceipt states what this turn CHANGED, from execution
// records.
//
// ══════════════════════════════════════════════════════════════════════
//
//	NO TOOL RECEIPT, NO EXECUTION CLAIM
//
// ══════════════════════════════════════════════════════════════════════
//
// It never reads the answer. It is derived entirely from counts the
// runtime produced at the moment each capability ran, which is what makes
// it able to contradict a model that wrote "8 transações importadas" after
// calling nothing.
//
// Empty string when the turn requested no writes at all: a line saying
// "0 escritas" under every ordinary conversational reply would train the
// reader to stop seeing it, and the write-side danger is a FALSE POSITIVE
// claim, which requires a write to have been requested.
func formatWriteReceipt(executed, failed, refused int) string {
	if executed == 0 && failed == 0 && refused == 0 {
		return ""
	}
	s := fmt.Sprintf("escritas: %d executada(s)", executed)
	if failed > 0 {
		s += fmt.Sprintf(", %d falhou(ram)", failed)
	}
	if refused > 0 {
		s += fmt.Sprintf(", %d recusada(s)", refused)
	}
	return "[" + s + "]"
}

// formatReadReceipt states whether this turn read anything OUTSIDE
// C.O.R.S.I.
//
// ══════════════════════════════════════════════════════════════════════
//
//	READ PROVES STATE — AND ABSENCE IS THE DANGEROUS STATE
//
// ══════════════════════════════════════════════════════════════════════
//
// This is the inverted twin of the write footer and the inversion is
// deliberate, exactly as it is in the web client's ExternalReadStrip. For
// a WRITE the dangerous state is a claim with nothing behind it, so
// silence is safe and only activity is labelled. For an external READ the
// dangerous state is the SILENCE: a follower count invented in a turn that
// called nothing looks identical to one that was fetched.
//
// So NO_EXTERNAL_READ gets a label, and it is the whole reason this
// function exists.
//
// `available` is the domain's own presentation gate, passed through
// unchanged: an agent with no external capability produces
// NO_EXTERNAL_READ on every turn of its life, correctly and uselessly, and
// printing that under every reply would be noise that teaches the reader
// to ignore the one place it matters.
func formatReadReceipt(status string, available bool) string {
	if !available {
		return ""
	}
	switch status {
	case "VERIFIED_EXTERNAL_READ":
		return "[leitura externa: verificada neste turno]"
	case "FAILED_EXTERNAL_READ":
		return "[leitura externa: falhou — não há evidência atual]"
	case "NO_EXTERNAL_READ":
		return "[leitura externa: nenhuma neste turno]"
	default:
		// An unknown status is not silently dropped: a vocabulary that
		// grew is something the reader should see, and rendering nothing
		// would be this surface quietly opting out of the guarantee.
		return "[leitura externa: " + status + "]"
	}
}
