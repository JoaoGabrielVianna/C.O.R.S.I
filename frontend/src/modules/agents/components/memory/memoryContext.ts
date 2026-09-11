import { useOutletContext } from "react-router-dom";

import type { ApiAgent } from "@/modules/agents/api/agents";
import type { MemoryPage } from "@/modules/agents/api/memories";
import type { MemoryActions } from "./memoryActions";

/**
 * What the Memory layout hands to both of its views.
 *
 * The list and the Brain read one query and share one set of actions, held
 * by the layout above them. Switching tabs therefore costs nothing: no
 * refetch, no second copy of the collection, and no chance of the two
 * disagreeing about which memory is being edited.
 *
 * It lives in its own module rather than beside the layout component so the
 * layout file exports only components and keeps fast refresh.
 */
export interface MemoryContext {
  agent: ApiAgent;
  page: MemoryPage | undefined;
  isLoading: boolean;
  /** Non-null when the read failed, with a message worth showing. */
  error: string | null;
  refetch: () => void;
  actions: MemoryActions;
  /** True while a write is in flight, so both views disable at once. */
  busy: boolean;
  onCreate: () => void;
}

export function useMemoryContext(): MemoryContext {
  return useOutletContext<MemoryContext>();
}
