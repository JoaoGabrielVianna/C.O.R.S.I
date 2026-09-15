-- Which ENTITY a write touched, recorded beside the fact that it ran.
--
-- ── The incident ──────────────────────────────────────────────────────
-- A turn created a Room and an Artifact and then hit the round ceiling
-- before adding the items it had been asked for. Asked to try again, the
-- next turn created a SECOND Room and a SECOND Artifact.
--
-- The receipt was correct and insufficient. It said:
--
--   EXECUTED     palace.room.create x1
--   EXECUTED     palace.artifact.create x1
--   NOT_EXECUTED palace.artifact.item.add x2 tool_round_limit
--
-- The capabilities are Confidential, so `arguments` and `result` are not
-- kept and are never replayed; the interrupted turn's own prose was cut off
-- before it named anything. The receipt proved the VERB and not the OBJECT,
-- and a retry with no way to identify what already exists duplicates it.
--
-- ── Why two columns and not one text field ────────────────────────────
-- Because the id is typed and the type is not. `effect_id` is a uuid, so
-- the database itself refuses anything that is not an identifier — which is
-- the structural half of the guarantee that this column can never become a
-- channel for content. See domain/effect_ref.go.
--
-- ── Why stored rather than derived later ──────────────────────────────
-- The same reason `effect` and `external` are stored: a receipt is a
-- statement about a turn that already happened, and the registry describes
-- the build running now. A capability that changes what it reports, or
-- leaves the build, must not be able to rewrite what a past turn did.
--
-- ── Nullable, and null is the common case ─────────────────────────────
-- Most calls touch nothing nameable, and the 47 capabilities that do not
-- opt into reporting leave both columns null. Null means "no identity was
-- reported", never "identity unknown, please guess": a resume that finds no
-- ref must read, which is the behaviour that was always correct.

ALTER TABLE chat.tool_calls
  ADD COLUMN effect_type text,
  ADD COLUMN effect_id   uuid;

-- Either both or neither. A type with no id names nothing, and an id with
-- no type is an identifier the reader cannot resolve; both halves are
-- required for the ref to mean anything, so a half-written one is refused
-- rather than stored and puzzled over later.
ALTER TABLE chat.tool_calls
  ADD CONSTRAINT tool_calls_effect_ref_complete
  CHECK ((effect_type IS NULL) = (effect_id IS NULL));

-- The type follows the same vocabulary rule the domain enforces: short,
-- lowercase, no spaces. Stated here too because a CHECK is the one place
-- that keeps holding after somebody bypasses the application.
ALTER TABLE chat.tool_calls
  ADD CONSTRAINT tool_calls_effect_type_shape
  CHECK (effect_type IS NULL OR effect_type ~ '^[a-z0-9_]{1,40}$');

-- Resume reads a single turn's effects. The index is partial because the
-- overwhelming majority of rows have no ref at all.
CREATE INDEX tool_calls_effect_idx
  ON chat.tool_calls (message_id)
  WHERE effect_id IS NOT NULL;
