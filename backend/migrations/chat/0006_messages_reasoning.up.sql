-- Reasoning models emit their chain of thought on a channel separate from
-- the answer. Storing it alongside the reply is what lets a thread be
-- re-read later showing how the model got there, not just where it landed.
--
-- Kept out of `content` on purpose: reasoning is never replayed back to the
-- provider on the next turn (see buildWireMessages), so mixing the two
-- would silently change what the model sees.
ALTER TABLE chat.messages
    ADD COLUMN reasoning    TEXT NOT NULL DEFAULT '',
    -- Wall-clock milliseconds spent reasoning before the first answer
    -- token. Recorded rather than derived so the history can say "thought
    -- for 4.2s" long after the stream is gone.
    ADD COLUMN reasoning_ms INTEGER NOT NULL DEFAULT 0 CHECK (reasoning_ms >= 0);
