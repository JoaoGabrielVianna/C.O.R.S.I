import type { ApiMemory } from "@/modules/agents/api/memories";

/**
 * The four things that can be done to a memory, wherever it is shown.
 *
 * Both surfaces — the list and the Brain — offer the same set, because they
 * are two views of one collection and an action available in one but not the
 * other would make the choice of view consequential. The dialogs they open
 * live once, in the Memory layout above both, so switching tabs mid-edit
 * does not lose the draft.
 *
 * `startEdit` and `startDelete` open a dialog rather than acting: one
 * rewrites text, the other cannot be undone. The two toggles act
 * immediately, because both are reversible in a click.
 */
export interface MemoryActions {
  startEdit: (m: ApiMemory) => void;
  startDelete: (m: ApiMemory) => void;
  toggleEnabled: (m: ApiMemory) => void;
  togglePinned: (m: ApiMemory) => void;
}
