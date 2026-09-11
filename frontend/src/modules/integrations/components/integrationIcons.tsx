import type { SVGProps } from "react";

export function TelegramGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden {...props}>
      <path d="M21.94 4.51a1 1 0 0 0-1.39-1L2.6 10.51a1 1 0 0 0 .07 1.88l4.42 1.39 1.7 5.34a1 1 0 0 0 1.64.42l2.62-2.55 4.41 3.26a1 1 0 0 0 1.57-.6l3.34-15.04a1 1 0 0 0-.43-.1Zm-4.43 3.21-7.54 6.74-.31 3.34-1.18-3.69 9.03-6.39Z" />
    </svg>
  );
}

export function WhatsappGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden {...props}>
      <path d="M12.04 2.03a9.9 9.9 0 0 0-8.5 14.95L2 22l5.18-1.5a9.9 9.9 0 1 0 4.86-18.47Zm0 18.04a8.1 8.1 0 0 1-4.13-1.13l-.3-.18-3.07.89.92-3-.2-.31a8.13 8.13 0 1 1 6.78 3.73Zm4.71-5.95c-.26-.13-1.52-.75-1.76-.83-.24-.09-.41-.13-.59.13-.17.26-.67.83-.82 1-.15.17-.3.2-.56.07-.26-.13-1.08-.4-2.06-1.27a7.78 7.78 0 0 1-1.43-1.78c-.15-.26-.02-.4.11-.53.11-.11.26-.3.39-.45.13-.15.17-.26.26-.43.09-.17.04-.32-.02-.45-.07-.13-.59-1.43-.81-1.96-.21-.51-.43-.44-.59-.45h-.5c-.17 0-.45.07-.69.32-.24.26-.91.89-.91 2.18 0 1.29.93 2.53 1.06 2.71.13.17 1.83 2.79 4.43 3.91.62.27 1.1.43 1.47.55.62.2 1.18.17 1.62.1.5-.07 1.52-.62 1.74-1.22.21-.61.21-1.13.15-1.22-.07-.09-.24-.15-.5-.28Z" />
    </svg>
  );
}

export function GmailGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden {...props}>
      <path d="M3 6.5a2.5 2.5 0 0 1 4-2L12 8.5l5-4A2.5 2.5 0 0 1 21 6.5v11A2.5 2.5 0 0 1 18.5 20H17V9.83l-5 4-5-4V20H5.5A2.5 2.5 0 0 1 3 17.5v-11Z" />
    </svg>
  );
}

export function ApiGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden {...props}>
      <path d="M3 9h2a2 2 0 1 1 0 4H3" />
      <path d="M3 5v14" />
      <path d="M9 5v14" />
      <path d="M13 9h3a2 2 0 1 1 0 4h-3v-4Zm0 4v6" />
      <path d="M20 5v14" />
    </svg>
  );
}

/**
 * The Meta Threads mark — the "@" ligature the product uses.
 *
 * Drawn rather than fetched, for the reason every glyph here is: a card
 * that loads a remote logo is a card that flashes, and an integration's
 * icon must render before its network does.
 */
export function MetaThreadsGlyph(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden {...props}>
      <path d="M16.2 11.6c-.15-.07-.3-.14-.46-.2-.27-2.9-1.9-4.56-4.6-4.58h-.04c-1.62 0-2.96.68-3.79 1.92l1.49 1.02c.62-.93 1.58-1.13 2.3-1.13h.03c.9 0 1.58.26 2.02.77.32.38.53.9.64 1.55a12 12 0 0 0-2.5-.13c-2.52.14-4.14 1.6-4.03 3.63.06 1.03.57 1.91 1.45 2.48.74.48 1.7.72 2.69.66 1.31-.07 2.34-.57 3.05-1.48.54-.69.89-1.58 1.04-2.71.62.37 1.07.86 1.33 1.45.43 1 .46 2.65-.88 3.99-1.18 1.17-2.59 1.68-4.72 1.7-2.36-.02-4.15-.78-5.31-2.25C6.83 16.9 6.28 14.94 6.26 12c.02-2.94.57-4.9 1.64-6.28C9.06 4.25 10.85 3.49 13.21 3.47c2.38.02 4.2.78 5.42 2.27.6.73 1.05 1.65 1.35 2.72l1.75-.47c-.36-1.32-.93-2.46-1.7-3.4C18.46 2.68 16.16 1.7 13.22 1.68h-.01c-2.94.02-5.21 1-6.75 2.92C5.09 6.3 4.4 8.66 4.38 11.99v.02c.02 3.33.71 5.69 2.08 7.39 1.54 1.91 3.81 2.9 6.75 2.92h.01c2.61-.02 4.45-.7 5.97-2.21 1.98-1.98 1.92-4.46 1.27-5.98-.47-1.09-1.36-1.98-2.56-2.57Zm-4.77 4.2c-1.1.06-2.24-.43-2.3-1.5-.04-.79.56-1.67 2.37-1.78l.5-.01c.66 0 1.27.06 1.83.19-.21 2.6-1.43 3.05-2.4 3.1Z" />
    </svg>
  );
}
