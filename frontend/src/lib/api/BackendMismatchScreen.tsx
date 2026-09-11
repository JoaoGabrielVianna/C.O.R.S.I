/**
 * The screen shown instead of the app when the API is not C.O.R.S.I.
 *
 * ── Why it replaces the app rather than warning beside it ──────────────
 * Because the alternative was measured and it is worse. Letting the app
 * boot against a foreign API produces one plausible failure per screen —
 * an empty board here, a parse error there — and every one of them looks
 * like a product bug. One accurate sentence beats twenty misleading ones.
 *
 * ── Why it is development-only ─────────────────────────────────────────
 * In production the frontend and the API are behind the same upstream, so
 * this cannot mean what it means locally: it would mean the health probe
 * hiccuped, and blanking a working product over that would be an outage
 * this guard invented. See main.tsx, which only mounts it in dev.
 */

export function BackendMismatchScreen({
  received,
  target,
}: {
  received: string;
  target: string;
}) {
  return (
    <div
      data-testid="backend-mismatch"
      style={{
        minHeight: "100vh",
        display: "grid",
        placeItems: "center",
        padding: "2rem",
        background: "#0b0b0f",
        color: "#e6e6ea",
        fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
      }}
    >
      <div style={{ maxWidth: "42rem", lineHeight: 1.6 }}>
        <h1 style={{ fontSize: "1.25rem", color: "#ff6b6b", marginBottom: "1rem" }}>
          Backend errado
        </h1>
        <p style={{ marginBottom: "1rem" }}>
          O frontend do C.O.R.S.I. está conectado a uma API que não é o C.O.R.S.I.
          Os erros que você veria a seguir seriam dessa outra API, não deste produto.
        </p>
        <pre
          style={{
            background: "#15151c",
            padding: "1rem",
            borderRadius: "0.5rem",
            overflowX: "auto",
            marginBottom: "1rem",
          }}
        >
{`esperado:  ${"corsi"}
recebido:  ${received}
alvo:      ${target}`}
        </pre>
        <p style={{ color: "#9a9aa8" }}>
          Verifique quem ocupa a porta do backend. Duas aplicações podem
          escutar a mesma porta ao mesmo tempo, uma em 127.0.0.1 e outra em
          ::1, sem que nenhuma falhe ao subir.
        </p>
      </div>
    </div>
  );
}
