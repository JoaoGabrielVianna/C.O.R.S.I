/*
 * LEGACY — the preference it stored no longer has anything to place.
 *
 * It decided where the module's Chat / Configuração switcher was drawn.
 * That switcher is gone: Agents navigates by URL now. Both readers (the
 * module page and the Preferences screen) have been updated, so nothing
 * imports this. The stored localStorage key is simply ignored.
 */
import { useCallback, useEffect, useState } from "react";

/**
 * Where the Chat / Configuração switcher lives — a per-browser preference.
 *
 * Header is the default (the switcher rides in the page header). Sidebar
 * moves it into a left rail on the Agents page. Persisted to localStorage so
 * the choice survives reloads; it is purely presentational, so it stays on
 * the client and never touches the backend.
 *
 * The control that sets this preference lives in the app's general
 * Preferences page, while the Agents page reads it to decide where to render
 * the switcher. Those are two separate component trees, so the hook keeps
 * every instance in sync — same tab (a custom event) and across tabs (the
 * storage event) — and a change in one place lands everywhere at once.
 */

export type NavPlacement = "header" | "sidebar";

const KEY = "corsi.agents.navPlacement";
const SYNC_EVENT = "corsi:navPlacement";

function read(): NavPlacement {
  try {
    return localStorage.getItem(KEY) === "sidebar" ? "sidebar" : "header";
  } catch {
    return "header";
  }
}

export function useNavPlacement(): [NavPlacement, (p: NavPlacement) => void] {
  const [placement, setState] = useState<NavPlacement>(read);

  useEffect(() => {
    const sync = () => setState(read());
    window.addEventListener("storage", sync);
    window.addEventListener(SYNC_EVENT, sync);
    return () => {
      window.removeEventListener("storage", sync);
      window.removeEventListener(SYNC_EVENT, sync);
    };
  }, []);

  const setPlacement = useCallback((p: NavPlacement) => {
    setState(p);
    try {
      localStorage.setItem(KEY, p);
    } catch {
      // private mode / storage disabled — the choice just won't persist.
    }
    // Notify other hook instances mounted in this same tab.
    window.dispatchEvent(new Event(SYNC_EVENT));
  }, []);

  return [placement, setPlacement];
}
