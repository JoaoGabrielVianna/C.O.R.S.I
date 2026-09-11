import { useCallback, useState } from "react";

/**
 * Copy-to-clipboard with a two-second confirmation.
 *
 * `navigator.clipboard` requires a secure context. `localhost` counts as
 * one, but opening the dev server from another device on the LAN over
 * plain http does not — the fallback is what keeps the copy buttons
 * working when you test the chat from your phone.
 */
export function useCopy(text: string) {
  const [copied, setCopied] = useState(false);

  const copy = useCallback(() => {
    const done = () => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    };

    const legacyCopy = () => {
      const el = document.createElement("textarea");
      el.value = text;
      el.style.position = "fixed";
      el.style.opacity = "0";
      document.body.appendChild(el);
      el.select();
      try {
        document.execCommand("copy");
        done();
      } finally {
        document.body.removeChild(el);
      }
    };

    if (navigator.clipboard?.writeText) {
      void navigator.clipboard.writeText(text).then(done).catch(legacyCopy);
      return;
    }
    legacyCopy();
  }, [text]);

  return { copied, copy };
}
