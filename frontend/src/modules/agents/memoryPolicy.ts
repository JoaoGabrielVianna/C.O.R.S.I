/**
 * The one thing the client is allowed to know about the memory policy.
 *
 * The policy itself is the backend's: which modes exist, what the default
 * is, and above all what deserves to be remembered. None of that is
 * duplicated here, because a second copy of a rule is a rule that will
 * disagree with itself on the first change — the same reason there is no
 * tool catalogue in this frontend.
 *
 * What the interface does need is the ceiling, so a counter can turn red
 * before a save is refused rather than after. It mirrors
 * `domain.MaxMemoryPolicyNotes`, exactly as MAX_MEMORY_CHARS mirrors the
 * memory CHECK constraint, and it is a courtesy: the server refuses 1001
 * characters whatever this file says.
 */
export const MAX_MEMORY_POLICY_NOTES = 1000;
