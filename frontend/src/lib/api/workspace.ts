/**
 * Workspace id source.
 *
 * ── Decision: single-workspace MVP ─────────────────────────────────────
 * The Finance MVP ships with a single workspace per user. There is no
 * workspace switcher in the UI and no multi-tenant flows in the read or
 * write paths. Every API call carries the sentinel UUID below in the
 * `X-Workspace-Id` header, matching the backend's `DevWorkspaceID`
 * sentinel in `backend/internal/platform/workspace/workspace.go` and
 * the dev-mode header policy in `.env.example`
 * (`WORKSPACE_REQUIRE_HEADER=false`).
 *
 * When external auth lands and the product needs multi-workspace,
 * replace this getter with one that reads the verified workspace id
 * from the auth context. Every API call goes through `apiFetch`, which
 * calls this — no other consumer needs to change. Query keys throughout
 * the finance module already embed the workspace id, so cache scoping
 * survives the switch.
 */
export const DEV_SENTINEL_WORKSPACE_ID = "00000000-0000-0000-0000-000000000001";

export function getApiWorkspaceId(): string {
  return DEV_SENTINEL_WORKSPACE_ID;
}
