import { cn } from "@/lib/utils";
import { detectBank, detectNetwork, detectVariant, mockLast4 } from "./cards";
import { formatBRL } from "./format";
import type { Cents, CreditCard } from "./types";

type Size = "sm" | "md" | "lg";

type Props = {
  card: CreditCard;
  /** Amount used on the current open invoice (cents). Drives the bottom strip. */
  used?: Cents;
  /** Optional invoice total to surface as a small line next to the digits. */
  invoice?: Cents;
  /** Last 4 digits if known; otherwise derived deterministically from id. */
  last4?: string;
  size?: Size;
  className?: string;
};

/**
 * CardPreview — personalized credit-card visual.
 *
 * Detects bank / network / variant from the card name + brand fields and
 * renders a coloured surface with bank name, last 4 digits, network text
 * badge, optional variant tag and a limit-usage strip at the bottom.
 *
 * No official logos or trademarked assets. Bank label and network badge are
 * plain stylized text. Premium variants (Black, Infinite, Platinum, Gold)
 * add a soft sheen overlay.
 *
 * Sizing: parent controls width via `className` (e.g. `w-full`, `max-w-xs`);
 * aspect ratio stays at 8/5 — the `size` prop only scales typography.
 */
export function CardPreview({
  card,
  used,
  invoice,
  last4,
  size = "md",
  className,
}: Props) {
  const bank = detectBank(card.name);
  const network = detectNetwork(card.name, card.brand);
  const variant = detectVariant(card.name);
  const digits = last4 ?? mockLast4(card.id);

  const usedAmount = used ?? 0;
  const usedPct = card.limit > 0 ? Math.min(100, (usedAmount / card.limit) * 100) : 0;

  const t = TYPOGRAPHY[size];
  const isPremium = variant.key === "black" || variant.key === "infinite" || variant.key === "platinum";

  return (
    <div
      className={cn(
        "relative isolate flex aspect-[8/5] w-full flex-col justify-between overflow-hidden rounded-xl shadow-(--shadow-card)",
        t.padding,
        className,
      )}
      style={{
        backgroundImage: `linear-gradient(135deg, ${bank.bgFrom} 0%, ${bank.bgTo} 100%)`,
        color: bank.fg,
      }}
      data-bank={bank.key}
      data-network={network.key}
      data-variant={variant.key}
    >
      {/* Premium sheen */}
      {isPremium ? (
        <div
          aria-hidden
          className="pointer-events-none absolute inset-0"
          style={{
            backgroundImage:
              "linear-gradient(120deg, rgba(255,255,255,0.08) 0%, transparent 30%, rgba(255,255,255,0.05) 55%, transparent 90%)",
          }}
        />
      ) : null}

      {/* Top row · bank label · variant chip */}
      <header className="relative flex items-start justify-between gap-2">
        <p className={cn("font-display font-bold tracking-tight", t.bank)}>
          {bank.label}
        </p>
        {variant.label ? (
          <span
            className={cn("rounded-sm px-1.5 py-px font-mono uppercase tracking-[0.18em]", t.variant)}
            style={{ borderWidth: 1, borderStyle: "solid", borderColor: bank.accent, color: bank.fg }}
          >
            {variant.label}
          </span>
        ) : null}
      </header>

      {/* Bottom row · digits + (optional) invoice line · network badge */}
      <footer className="relative flex items-end justify-between gap-2">
        <div className="min-w-0">
          <p className={cn("font-mono tracking-wider opacity-90", t.digits)}>
            •••• {digits}
          </p>
          {invoice !== undefined ? (
            <p className={cn("mt-1 font-mono opacity-70", t.invoice)}>
              {formatBRL(invoice)}
            </p>
          ) : null}
        </div>
        {network.label ? (
          <span className={cn("font-display font-extrabold tracking-tight", t.network)}>
            {network.label}
          </span>
        ) : null}
      </footer>

      {/* Limit usage strip */}
      {card.limit > 0 ? (
        <div
          aria-hidden
          className="absolute inset-x-0 bottom-0 h-1"
          style={{ backgroundColor: `${bank.accent}33` }}
        >
          <div className="h-full" style={{ width: `${usedPct}%`, backgroundColor: bank.accent }} />
        </div>
      ) : null}
    </div>
  );
}

const TYPOGRAPHY = {
  sm: {
    padding: "px-3 py-2.5",
    bank:    "text-[12px]",
    variant: "text-[8.5px]",
    digits:  "text-[13px]",
    invoice: "text-[10px]",
    network: "text-[13px]",
  },
  md: {
    padding: "px-4 py-3.5",
    bank:    "text-[15px]",
    variant: "text-[9.5px]",
    digits:  "text-[16px]",
    invoice: "text-[11px]",
    network: "text-[16px]",
  },
  lg: {
    padding: "px-5 py-4",
    bank:    "text-[19px]",
    variant: "text-[10.5px]",
    digits:  "text-[20px]",
    invoice: "text-[12px]",
    network: "text-[20px]",
  },
} satisfies Record<Size, {
  padding: string;
  bank: string;
  variant: string;
  digits: string;
  invoice: string;
  network: string;
}>;
