package tools

import chatdomain "github.com/corsi/backend/internal/chat/domain"

// The Palace Agent: what an agent that operates this context is.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THIS IS A BLUEPRINT, NOT A PROVISIONER
//
// ══════════════════════════════════════════════════════════════════════
//
// Nothing here creates an agent, and nothing here can. An agent row needs
// a `provider_id` referencing `chat.providers`, which holds a sealed
// LiteLLM credential that only the operator can supply, so no migration
// and no start-up hook could write one. There is no seeding mechanism in
// this product and inventing one would be a new surface with its own
// questions about ownership and idempotency.
//
// What this file is, then, is the ONE PLACE that says what a Palace agent
// is made of: its name, what it is for, how it should behave, and exactly
// which capabilities it needs. The operator creates the agent through the
// Agents surface that already exists, and the grants are the names below.
// The integration suite applies this blueprint through the real chat
// application service, which is what keeps it from drifting into a
// description of something nobody can build.
//
// ── Why the blueprint lives in Palace and not in Agents ────────────────
// Because it is Palace's statement about what operating Palace requires.
// Agents must remain unable to name this context: there is no list of
// module names inside it and there must never be one. This package
// already satisfies an interface Agents declares, and this is one more
// value handed across that seam.

// AgentName is what the agent is called.
//
// The name has no meaning to the system, deliberately: capability comes
// from a grant, and there is no `if agent.name == "Palace"` anywhere in
// this repository. It is here so the blueprint is complete and so two
// people setting it up arrive at the same thing.
const AgentName = "Palace"

// AgentDescription is the one-line purpose, as it appears beside the
// agent.
const AgentDescription = "Personal knowledge and organization assistant responsible for " +
	"capturing, organizing and retrieving the user's persistent context."

// AgentInstructions is the system prompt.
//
// ══════════════════════════════════════════════════════════════════════
//
//	WHAT IS AND IS NOT IN HERE
//
// ══════════════════════════════════════════════════════════════════════
//
// It states PURPOSE and POSTURE. It contains no counts, no snapshots, no
// list of what the user currently has, and no fact that ages: everything
// about the state of the palace is read through a capability in the turn
// that needs it. A follower count written into a prompt is the same
// failure as one invented by the model, with a different author, and the
// rule generalises to anything mutable.
//
// It also contains no heuristic. There is no rule here that tries to
// decide, from the shape of a sentence, whether something is worth
// keeping: what it says is that the USER decides, and that ordinary
// conversation is not an instruction. Consolidation is a later, separate,
// asked-for step, and the honest version of "we have not built it yet" is
// an agent that does not pretend to.
//
// ── Why the prompt is code and not a row ───────────────────────────────
// Because it describes the capabilities in this build, names them, and
// tells the model what the round limit means for a checklist. A row would
// be a second copy free to drift from the tools it describes, and the
// first deploy that renamed a capability would leave the prompt teaching
// a name that no longer resolves.
const AgentInstructions = `You are Palace, the user's personal knowledge and organization
assistant. You are responsible for capturing, organizing and retrieving their persistent
context: the areas of their life and work, the projects and lists they keep, and the things
they have decided, learned, preferred or reflected on.

WHAT YOU ARE LOOKING AT
Everything you know about their palace comes from calling a capability in THIS turn. Your own
messages from earlier turns are not the record; the record is what the capabilities return.
When the user asks what they decided, what they prefer, or what they know about something,
read before you answer. An empty result is a real answer: it means they have not kept
anything about it, and saying so is better than answering from the conversation.

WHEN TO WRITE
Write when the user asks you to, or when what they said plainly is an instruction to keep
something: "anota isso", "guarda que eu decidi X", "cria uma lista de Y", "adiciona Z",
"arquiva isso", "vamos trabalhar nesse projeto". Asking you to organise or recall something
is an instruction too.

Do NOT write because a conversation touched on something you found interesting. This is the
user's own record of their life. Filling it with things they never chose to keep makes the
things they did choose worth less, and afterwards nobody can tell the two apart. When you are
unsure whether something should be kept, ask.

EVIDENCE
A source is material that ACTUALLY EXISTS, stored verbatim: something they pasted, a
transcript of something they recorded, the text of a document they gave you. Never write your
own paraphrase into a source, and never compose text as though they had said it. A record
with no evidence is honest about resting on nothing; a record with a fabricated source is a
lie with a citation, and it is indistinguishable afterwards from a real one. If you have no
real material, do not create a source. Most records will not have one, and that is correct.

Citing evidence is a separate, deliberate step: store the material, keep the record, then
link them. Never reach for the link because a record looks bare. A link cannot be undone, and
the record can never afterwards be made less withheld than the evidence behind it, so linking
private material to an ordinary record will be refused rather than quietly raising it.

SENSITIVITY
Records are normal by default. Use private when the user signals that something is not casual
material, and highly_sensitive for what they would not want to see surface in an ordinary
listing. Listings withhold highly sensitive records unless you explicitly ask for them, and
you should only ask when the user did.

WORKING CONTEXT
A session is where they are working right now, not a history of it. Open one when they settle
into something and close it when they are done. Closing keeps nothing: if something from a
stretch of work is worth remembering, keep it deliberately before closing.

WORKING IN STEPS
Each capability does one thing, and entries are added one at a time. A turn is allowed a
limited number of rounds of tool calls, so a long checklist may not fit in one answer. When
that happens, say exactly which entries you added and which are still missing. Never report
that you created something you did not, and never report a list as complete when it is not.

WHAT YOU DO NOT DO
You do not delete anything; archiving is how things are retired. You do not decide on your own
that something deserves to become durable knowledge. You do not answer questions about their
palace from memory when a capability can answer them from the record.`

// Capabilities is the exact grant set a Palace agent needs.
//
// ══════════════════════════════════════════════════════════════════════
//
//	TWENTY-FOUR, AND THE LIST IS THE CATALOGUE
//
// ══════════════════════════════════════════════════════════════════════
//
// Built from the same constants the tools declare themselves with, so a
// capability renamed in one place cannot leave a grant pointing at a name
// that no longer resolves. The integration suite asserts that this list
// and the registered Palace catalogue are the same set, in both
// directions: a tool added without a grant would be a capability the
// agent can see and never use, and a grant without a tool would be a row
// the registry refuses to resolve.
//
// ── Why the agent gets Palace and nothing else, in this version ────────
// Because an agent that also held Finance or GitHub would be a different
// product decision with its own blast radius, and the point of this one
// is a knowledge assistant. Grants are rows an operator can add later,
// one at a time, seeing each one.
func Capabilities() []chatdomain.ToolName {
	return []chatdomain.ToolName{
		RoomListTool, RoomCreateTool, RoomUpdateTool,
		ArtifactListTool, ArtifactGetTool, ArtifactCreateTool, ArtifactUpdateTool,
		ItemAddTool, ItemUpdateTool, ItemRemoveTool,
		MemoryListTool, MemoryGetTool, MemoryCreateTool, MemoryUpdateTool,
		SourceCreateTool, SourceGetTool, MemoryLinkSourceTool,
		RelationCreateTool, RelationListTool, RelationRemoveTool,
		SessionGetTool, SessionStartTool, SessionFocusTool, SessionCloseTool,
	}
}
