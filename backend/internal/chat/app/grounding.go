package app

// The grounding policy: the platform's own instruction about when to look
// something up instead of guessing it.
//
// ── The failure it exists to answer ────────────────────────────────────
// An agent with seven read-only capabilities listed the user's 32
// repositories correctly, and then, asked which of them were impressive,
// answered with "probably involves automation", "suggests simulation",
// "usually means…". It could have looked. It did not.
//
// Function calling was working. What was missing was any statement, from
// anywhere, about WHEN a conclusion needs evidence — the agent in question
// had an empty system prompt, so the model received no instructions at all
// beyond the tool declarations themselves.
//
// ── Why it lives here and not in a prompt field ────────────────────────
// Because it is a property of the platform, not of an agent. Every agent
// with capabilities needs it, no agent should have to remember to write it,
// and an agent that never wrote one would silently be the agent that
// guesses. Putting it in each system prompt would make the rule optional,
// unversioned and impossible to change in one place — and would leave the
// default agent, the one with no prompt at all, with nothing.
//
// It is composed into the instructions block by BuildContext, which is the
// one place that decides what a turn is told. There is no second prompt
// system, and no policy in a handler.
//
// ── Why it names nothing ───────────────────────────────────────────────
// No provider, no integration, no tool. It is a rule about the relationship
// between a claim and its evidence, and it has to hold identically for the
// capabilities that exist today and the ones that arrive later. A policy
// that said "GitHub" would be a policy that stops working the moment the
// second integration lands — and would be teaching the model about a vendor
// rather than about honesty.
//
// ── Why the wording avoids demanding labels ────────────────────────────
// The three categories are a discipline for the model's own reasoning, not
// a format for the user's screen. A model told to emit "OBSERVED:" prefixes
// produces a worse answer wearing a costume; a model told to keep the
// categories apart produces a normal answer that happens to be true. The
// requirement is one-directional: what is unknown must not be presented as
// observed. The reverse — evidence stated plainly — needs no ceremony.
//
// ── Why it is only sent when there are capabilities ────────────────────
// Because a turn with no tools cannot verify anything, and instructing it
// to verify would be asking for something impossible while charging for the
// request. Most agents in this system have no capabilities at all; their
// prompt is byte-identical to what it was before this file existed.

// ── Why a paragraph about permission was added ─────────────────────────
// Because the live experiment showed the policy was RIGHT and INCOMPLETE.
// Given real capabilities and real evidence, claude-sonnet-4-6 answered:
//
//	"preciso ser honesto: até agora só listei os repositórios … qualquer
//	 opinião que eu desse agora seria baseada em aparências … Quer que eu
//	 mergulhe no código desses?"
//
// That is full compliance with everything the policy said. It asserted
// nothing it had not observed, it named the gap, it separated observation
// from inference — and then it stopped and asked. Nothing in the text
// distinguished "verify before asserting" from "offer to verify", so the
// model chose the polite reading, and the user got no answer.
//
// The paragraph closes that specific gap and nothing else. It is not more
// emphasis on the same rule; it is the rule that was missing.
//
// Its last sentence is the safety half, and it exists because the registry
// now contains a capability that writes. "Act without asking" must never
// generalise from a read to a change, and the model can tell the two apart
// because the declaration says so — see domain.ToolDefinition.DeclaredDescription.

// ── Why a paragraph about external systems was added ───────────────────
// Because the policy was, again, right and incomplete. A content agent
// with real capabilities and a real connection answered "1.535 seguidores,
// 40.055 views" in a turn with ZERO tool calls; the true figures were 163
// and 224. Nothing in the previous text was violated in a way the model
// could notice: it was not "presenting a guess as an observation" from its
// own point of view — it had read that account in an earlier turn of
// another conversation and treated the figures as known.
//
// That is the gap. The policy spoke about evidence in general and said
// nothing about evidence GOING STALE, and for a system we do not own,
// last week's reading is not weak evidence — it is a different number.
//
// The paragraph closes that and nothing else. It names no vendor and no
// tool: it points at the DECLARATION, which the capability writes about
// itself. And it is defence in depth, not the guarantee — the guarantee is
// the read receipt, which holds whether or not the model reads this.

// ── Why a paragraph about the execution record was added ───────────────
// Because the policy was right and incomplete in the one remaining
// direction. It had a great deal to say about not asserting what was never
// observed, and nothing at all about the opposite error: DENYING something
// that was.
//
// A live agent executed a write, the receipt recorded EXECUTED, and on the
// next turn it said "na verdade eu não cheguei a criar — só respondi como
// se tivesse". It was not being careless; it was applying this very policy.
// The capability withheld its payload, so there was no result to re-read;
// its own earlier sentence is not the record, and it knows that. Told
// nothing either way, the honest-seeming move was to disclaim — and having
// disclaimed, it wrote the same thing again.
//
// So the policy learns the symmetric rule, and it is generic: a system that
// records what ran is authority for whether it ran, in both directions. The
// paragraph names no module, no capability and no vendor, exactly like the
// three before it, and it is placed inside the cached prefix so it costs
// approximately one prompt per agent rather than one per turn.
//
// Its last sentence is the boundary, and it is the half that must not be
// lost: RECEIPT PROVES EXECUTION, READ PROVES STATE. Knowing that a write
// ran is not knowing what it wrote, and a model that read the first as the
// second would start reconstructing the payload redaction removed.

// groundingPolicy is the whole of it.
//
// Every sentence is load-bearing, and the order is the order a model reads
// it in: what to do, what a guess is for, how to keep the categories apart,
// how much is enough, and who decides when to go and get it. It is
// deliberately short — see the token measurement in the batch record —
// because an instruction block that grows without limit is one that gets
// ignored in the middle.
const groundingPolicy = `You have capabilities that look things up. What you assert should rest on what they return.

A guess may decide what to check; it must not stand in for the answer. When a question turns on facts an available capability could verify, verify them instead of filling the gap with what sounds plausible.

Keep apart what you observed — in this conversation or returned by a capability — what you inferred, and what you do not know. Never present the last as the first; no labels needed, just do not confuse them.

Judge how much to check: if the context already answers the question, answer from it; if a conclusion rests on a few candidates, examine enough of them to support it.

When a lookup would settle the question, do it and then answer: offering to look, and waiting to be told to, is not an answer, because the user already asked. Check the few things most likely to settle it, not everything in reach. A capability that CHANGES something is the exception, used only when the user asked for that change.

Some capabilities read systems outside this product and say so in their declaration. What such a system holds NOW — counts, metrics, what was published — must come from calling one this turn; an earlier reading is stale, not weak. If the call fails, say it failed.

An execution log records what actually ran and may be shown to you for earlier turns. Never deny doing what it records as executed, and never treat a withheld payload as proof that a call did not run. It proves nothing beyond that: for what was written, or what anything holds now, read it with a capability this turn.`

// GroundingPolicyCharacters is what the policy costs, in runes.
//
// Exported so a test can assert the overhead rather than describe it, and
// so the number in the batch record cannot drift from the string.
func GroundingPolicyCharacters() int {
	return len([]rune(groundingPolicy))
}
