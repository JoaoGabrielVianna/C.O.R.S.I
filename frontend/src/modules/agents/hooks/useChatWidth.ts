import { useCallback, useState } from "react";

/**
 * How wide the transcript reads — a per-browser preference.
 *
 * There is no single right answer, which is why this is a choice rather
 * than a constant. Prose is easier to follow in a bounded column: past
 * roughly 90 characters the eye starts losing the line on the way back to
 * the left margin. But a wide table, a diff, or a code block wants every
 * pixel a 34" monitor can give it, and boxing those into 768px wastes the
 * screen you paid for.
 *
 * Stored in localStorage: it is a display preference, so it belongs to the
 * machine you are sitting at, not to the workspace on the server.
 */

export type ChatWidth = "focus" | "wide";

const KEY = "corsi.agents.chatWidth";

function read(): ChatWidth {
  try {
    return localStorage.getItem(KEY) === "wide" ? "wide" : "focus";
  } catch {
    return "focus";
  }
}

export function useChatWidth(): [ChatWidth, (w: ChatWidth) => void] {
  const [width, setState] = useState<ChatWidth>(read);

  const setWidth = useCallback((w: ChatWidth) => {
    setState(w);
    try {
      localStorage.setItem(KEY, w);
    } catch {
      // private mode / storage disabled — the choice just won't persist.
    }
  }, []);

  return [width, setWidth];
}
