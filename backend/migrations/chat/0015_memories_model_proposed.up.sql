-- Who wrote the words of a memory.
--
-- ── Why this is a different fact from `origin` ─────────────────────────
-- `origin` says WHERE a memory was captured: the Memory page, or inside a
-- conversation. This says WHO composed the text: the user, or a model whose
-- proposal the user approved. The two are independent — a proposal is
-- captured in a conversation, but so is a sentence the user highlighted and
-- saved themselves.
--
-- ── Why a boolean and not a third origin ───────────────────────────────
-- A new value in the origin enum would travel: the Brain groups by origin,
-- the frontend's Provenance union switches on it, and every reader would
-- have to be taught a third case to keep rendering the two it already
-- knows. A column that starts false answers the new question without
-- changing the answer to the old one.
--
-- ── Why it exists before anything sets it ──────────────────────────────
-- The next version's consolidation flow proposes memories the user then
-- confirms. Approving text a model wrote is not the same act as writing it,
-- and a system that cannot tell them apart cannot answer "did I say this,
-- or did it?" later. Adding the column with the write path is what keeps
-- the rows created before that path from having to be guessed about: every
-- memory that exists today was typed by a person, and false records exactly
-- that.
ALTER TABLE chat.agent_memories
    ADD COLUMN model_proposed BOOLEAN NOT NULL DEFAULT false;

-- No index. It is read with the row and never filtered on: the Memory page
-- reads one agent's memories and the turn reads the enabled ones, neither
-- of which narrows by who composed the text.
