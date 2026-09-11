import { Navigate, Route, Routes } from "react-router-dom";
import { AgentShell } from "@/modules/agents/components/AgentShell";
import { AgentToolsPage } from "./AgentTools";
import { AgentsHomePage } from "./AgentsHome";
import { AgentBrainPage } from "./AgentBrain";
import { AgentConversationsPage } from "./AgentConversations";
import { AgentMemoryPage } from "./AgentMemory";
import { AgentMemoryListPage } from "./AgentMemoryList";
import { AgentSettingsPage } from "./AgentSettings";
import { AgentSourceEditorPage } from "./AgentSourceEditor";
import { AgentSourcesPage } from "./AgentSources";
import { AgentUsagePage } from "./AgentUsage";
import { NewAgentPage } from "./NewAgent";
import { ProvidersPage } from "./Providers";

/**
 * Agents — the module's own router.
 *
 * ── Why the agent is the address ───────────────────────────────────────
 * Every route below starts from an agent, because the agent is the thing
 * that persists: its instructions, its conversations, and later its memory,
 * sources and limits all belong to it. Before this, the whole module lived
 * at one URL with the visible state in `useState` and `localStorage`, which
 * meant no conversation could be linked, reload landed somewhere else, and
 * the browser's back button left the module entirely.
 *
 *   /app/modules/agents                            Home: agents + today
 *   /app/modules/agents/new                        create an agent
 *   /app/modules/agents/providers                  the connections
 *   /app/modules/agents/:agentId                   → conversations
 *   /app/modules/agents/:agentId/conversations     the agent's threads
 *   /app/modules/agents/:agentId/c/:conversationId one thread, linkable
 *   /app/modules/agents/:agentId/memory            what it remembers
 *   /app/modules/agents/:agentId/memory/brain      the same, as a graph
 *   /app/modules/agents/:agentId/sources           what it can consult
 *   /app/modules/agents/:agentId/sources/new       a new document
 *   /app/modules/agents/:agentId/sources/:sourceId one document, linkable
 *   /app/modules/agents/:agentId/tools             what it may execute
 *   /app/modules/agents/:agentId/usage             what it consumed
 *   /app/modules/agents/:agentId/settings          what it is
 *
 * The paths keep the platform's `/app/modules/<module>` prefix rather than
 * the shorter `/app/agents`, because the sidebar, the command palette, the
 * default landing route and the shell's full-width rule all key off that
 * prefix. A shorter URL is not worth teaching four other places about an
 * exception.
 *
 * ── Why `conversations` and `c/:id` are the same component ─────────────
 * They are one screen in two states: the list is a column beside the
 * transcript, and picking a thread should not swap the page out from under
 * the reader. Two routes, one component, and the URL is what says which
 * thread is open.
 *
 * ── Unknown addresses ──────────────────────────────────────────────────
 * Anything unrecognised inside an agent falls back to that agent's
 * conversations; anything unrecognised in the module falls back to the
 * Home. An id in a URL is never authorisation: the workspace scope is
 * applied server-side on every request, and an agent this workspace does
 * not own reads exactly like one that never existed.
 */
export function AgentsPage() {
  return (
    <Routes>
      <Route index element={<AgentsHomePage />} />
      <Route path="new" element={<NewAgentPage />} />
      <Route path="providers" element={<ProvidersPage />} />

      <Route path=":agentId" element={<AgentShell />}>
        {/* No mandatory overview screen: the shell header already carries
            what one would have shown. See AgentShell. */}
        <Route index element={<Navigate to="conversations" replace />} />
        <Route path="conversations" element={<AgentConversationsPage />} />
        <Route path="c/:conversationId" element={<AgentConversationsPage />} />
        {/* Two views of one collection, so each is an address of its own.
            See AgentMemory. */}
        <Route path="memory" element={<AgentMemoryPage />}>
          <Route index element={<AgentMemoryListPage />} />
          <Route path="brain" element={<AgentBrainPage />} />
        </Route>
        {/* No element on the parent: the children render straight into the
            agent shell's outlet, so each one still receives its context.
            A source is a page rather than a dialog because it holds up to
            twenty thousand characters. See AgentSourceEditor. */}
        <Route path="sources">
          <Route index element={<AgentSourcesPage />} />
          <Route path="new" element={<AgentSourceEditorPage />} />
          <Route path=":sourceId" element={<AgentSourceEditorPage />} />
        </Route>
        <Route path="tools" element={<AgentToolsPage />} />
        <Route path="usage" element={<AgentUsagePage />} />
        <Route path="settings" element={<AgentSettingsPage />} />
        <Route path="*" element={<Navigate to="conversations" replace />} />
      </Route>

      <Route path="*" element={<Navigate to="/app/modules/agents" replace />} />
    </Routes>
  );
}
