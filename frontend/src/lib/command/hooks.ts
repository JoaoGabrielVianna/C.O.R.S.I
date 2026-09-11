import { useContext, useEffect, useRef } from "react";
import { CommandContext, type Command, type CommandContextValue } from "./context";

export function useCommand(): CommandContextValue {
  const ctx = useContext(CommandContext);
  if (!ctx) throw new Error("useCommand must be used inside <CommandProvider>");
  return ctx;
}

/**
 * useRegisterCommands — register a list of commands for the lifetime of the
 * caller component. Identity of the list does not matter; the provider keys
 * by command id, so passing a freshly-built array each render is fine.
 *
 * Modules will call this from their entry component to surface their actions
 * in the palette while the module is mounted.
 */
export function useRegisterCommands(commands: readonly Command[]) {
  const { registerCommands } = useCommand();
  // Always re-register the latest list on every render. The provider keys by
  // id, so this is idempotent and trivially cheap.
  const cleanupRef = useRef<(() => void) | null>(null);
  useEffect(() => {
    cleanupRef.current?.();
    cleanupRef.current = registerCommands(commands);
    return () => {
      cleanupRef.current?.();
      cleanupRef.current = null;
    };
  }, [commands, registerCommands]);
}
