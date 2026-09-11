/**
 * TanStack Query hooks for conversations and their transcripts.
 *
 * The transcript query is the source of truth for a thread's history; the
 * in-flight reply is separate state owned by `useChatStream`, which
 * invalidates this query once the turn is persisted. Keeping the two apart
 * is what lets a streaming answer render token by token without the cache
 * being rewritten on every frame.
 *
 * ── One key root, several views ────────────────────────────────────────
 * Every list below hangs off `conversationsRootKey`, so a single
 * invalidation after a write refreshes the agent's list, the workspace's
 * recent list and the per-agent counts at once. Adding a view must not mean
 * remembering to invalidate it in five mutations.
 */

import {
  useInfiniteQuery,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";

import { getApiWorkspaceId } from "@/lib/api/workspace";
import {
  createConversation,
  deleteConversation,
  getConversation,
  listConversations,
  listMessages,
  renameConversation,
  type ApiConversation,
  type ConversationPage,
  type Transcript,
} from "@/modules/agents/api/conversations";

export const conversationsRootKey = (workspaceId: string) =>
  ["chat", "conversations", workspaceId] as const;

export const messagesKey = (workspaceId: string, conversationId: string) =>
  ["chat", "messages", workspaceId, conversationId] as const;

/** How many threads one page of an agent's list carries. */
export const CONVERSATIONS_PAGE_SIZE = 30;

/** How many cross-agent threads the Home offers as a way back in. */
export const RECENT_CONVERSATIONS = 6;

export interface ConversationList {
  conversations: ApiConversation[];
  /** How many exist in total, whether or not they have been fetched. */
  total: number;
  hasMore: boolean;
  loadMore: () => void;
  loadingMore: boolean;
  isLoading: boolean;
  isError: boolean;
}

/**
 * An agent's threads, paged.
 *
 * Paged rather than capped: the server defaults to fifty and returns no
 * marker when it truncates, so a plain read of the list would go quiet
 * exactly when it started hiding things. Here the page size is explicit,
 * `total` says how many exist, and the caller can ask for the rest.
 *
 * Offset paging over an activity-ordered list can shuffle a thread between
 * pages if a turn lands mid-scroll. Accepted: the alternative is a cursor
 * the server does not expose, and the visible cost is one row appearing
 * twice in a session, against a silent cut that never announces itself.
 */
export function useAgentConversations(agentId: string | undefined): ConversationList {
  const workspaceId = getApiWorkspaceId();
  const query = useInfiniteQuery({
    queryKey: [...conversationsRootKey(workspaceId), "agent", agentId ?? "none"],
    queryFn: ({ pageParam }) =>
      listConversations({
        agentId: agentId as string,
        limit: CONVERSATIONS_PAGE_SIZE,
        offset: pageParam,
      }),
    enabled: Boolean(agentId),
    initialPageParam: 0,
    getNextPageParam: (last: ConversationPage) => {
      const loaded = last.offset + last.items.length;
      return loaded < last.total ? loaded : undefined;
    },
    staleTime: 30_000,
  });

  const pages = query.data?.pages ?? [];
  return {
    conversations: pages.flatMap((p) => p.items),
    total: pages.at(-1)?.total ?? 0,
    hasMore: query.hasNextPage,
    loadMore: () => void query.fetchNextPage(),
    loadingMore: query.isFetchingNextPage,
    isLoading: query.isLoading,
    isError: query.isError,
  };
}

/**
 * The most recently active threads across every agent — the Home's "pick up
 * where you left off". Deliberately short and read-only: managing a thread
 * happens inside the agent that owns it.
 */
export function useRecentConversations(
  limit: number = RECENT_CONVERSATIONS,
): UseQueryResult<ConversationPage> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...conversationsRootKey(workspaceId), "recent", limit],
    queryFn: () => listConversations({ limit }),
    staleTime: 30_000,
  });
}

/**
 * How many threads each agent owns, for the Home cards.
 *
 * One cheap counted request per agent rather than one big list counted in
 * the browser: a client-side tally over a page would be wrong the moment
 * the workspace outgrows that page, and wrong quietly.
 */
export function useConversationCounts(agentIds: string[]): Map<string, number | undefined> {
  const workspaceId = getApiWorkspaceId();
  const results = useQueries({
    queries: agentIds.map((id) => ({
      queryKey: [...conversationsRootKey(workspaceId), "count", id],
      // limit 1 keeps the payload to a single row; `total` is the answer.
      queryFn: () => listConversations({ agentId: id, limit: 1 }),
      staleTime: 30_000,
    })),
  });
  return new Map(agentIds.map((id, i) => [id, results[i]?.data?.total]));
}

/**
 * One thread, by id, for the case the list cannot answer: a deep link to a
 * conversation that has not been paged in. Disabled when the caller already
 * has the record, so the ordinary path costs nothing.
 */
export function useConversation(
  conversationId: string | undefined,
  enabled: boolean,
): UseQueryResult<ApiConversation> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: [...conversationsRootKey(workspaceId), "one", conversationId ?? "none"],
    queryFn: () => getConversation(conversationId as string),
    enabled: enabled && Boolean(conversationId),
    // A thread that is not there is not going to appear by asking again.
    retry: false,
    staleTime: 30_000,
  });
}

export function useMessages(conversationId: string | null): UseQueryResult<Transcript> {
  const workspaceId = getApiWorkspaceId();
  return useQuery({
    queryKey: messagesKey(workspaceId, conversationId ?? "none"),
    queryFn: () => listMessages(conversationId as string),
    enabled: conversationId !== null,
    // A transcript only changes when this client sends a turn, and that
    // path invalidates explicitly. Refetching on focus would re-render a
    // long thread every time the user tabs back for no new data.
    refetchOnWindowFocus: false,
  });
}

export function useCreateConversation(): UseMutationResult<
  ApiConversation,
  Error,
  { agentId: string; title?: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ agentId, title }: { agentId: string; title?: string }) =>
      createConversation(agentId, title),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: conversationsRootKey(getApiWorkspaceId()) });
    },
  });
}

export function useRenameConversation(): UseMutationResult<
  ApiConversation,
  Error,
  { id: string; title: string }
> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, title }: { id: string; title: string }) => renameConversation(id, title),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: conversationsRootKey(getApiWorkspaceId()) });
    },
  });
}

export function useDeleteConversation(): UseMutationResult<void, Error, string> {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteConversation(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: conversationsRootKey(getApiWorkspaceId()) });
    },
  });
}
