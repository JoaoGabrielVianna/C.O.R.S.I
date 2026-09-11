import { createContext } from "react";
import type { SearchItem } from "@/lib/search";

/**
 * Command — a registered, runnable entry in the global palette.
 *
 * Extends `SearchItem`, so the palette UI renders commands and future search
 * results through the same row component. The only thing commands add is
 * `perform`: a synchronous or async action invoked on Enter / click.
 */
export type Command = SearchItem & {
  perform: () => void | Promise<void>;
};

export type CommandContextValue = {
  commands: readonly Command[];
  isOpen: boolean;
  open: () => void;
  close: () => void;
  toggle: () => void;
  /** Register a single command. Returns an unregister function. */
  registerCommand: (cmd: Command) => () => void;
  /** Register many commands at once. Returns a batch unregister function. */
  registerCommands: (cmds: readonly Command[]) => () => void;
};

export const CommandContext = createContext<CommandContextValue | null>(null);
