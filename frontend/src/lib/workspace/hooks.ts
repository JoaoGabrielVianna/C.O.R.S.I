import { useContext } from "react";
import { WorkspaceContext, type WorkspaceContextValue } from "./context";

export function useWorkspace(): WorkspaceContextValue {
  const ctx = useContext(WorkspaceContext);
  if (!ctx) throw new Error("useWorkspace must be used inside <WorkspaceProvider>");
  return ctx;
}
