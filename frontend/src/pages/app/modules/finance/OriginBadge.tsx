import { Bot, FileSpreadsheet, MessageCircle, PenLine } from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";
import type { TransactionSource } from "./types";

const ICON: Record<TransactionSource, React.ComponentType<React.SVGProps<SVGSVGElement>>> = {
  manual:   PenLine,
  whatsapp: MessageCircle,
  ai:       Bot,
  import:   FileSpreadsheet,
};

const TONE: Record<TransactionSource, string> = {
  manual:   "border-(--color-border) bg-(--color-muted) text-(--color-muted-foreground)",
  whatsapp: "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300",
  ai:       "border-violet-200 bg-violet-50 text-violet-700 dark:border-violet-700 dark:bg-violet-500/10 dark:text-violet-300",
  import:   "border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-700 dark:bg-sky-500/10 dark:text-sky-300",
};

/**
 * OriginBadge — visual identity for a transaction's source channel.
 *
 *   manual   · neutral · pen icon
 *   whatsapp · emerald · message icon
 *   ai       · violet  · bot icon
 *   import   · sky     · file icon
 *
 * Used in the Transactions table to make ingestion path scannable. When the
 * collector/normalizer ships, future-source labels will already render
 * correctly without any additional UI work.
 */
export function OriginBadge({
  source,
  showLabel = true,
  size = "sm",
}: {
  source: TransactionSource;
  showLabel?: boolean;
  size?: "sm" | "xs";
}) {
  const t = useT();
  const Icon = ICON[source];
  const label = t.app.modules.finance.sourceLabels[source];
  const sizeCls = size === "xs"
    ? "h-4 px-1.5 text-[9px]"
    : "h-5 px-1.5 text-[10px]";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border font-mono uppercase tracking-[0.14em]",
        sizeCls,
        TONE[source],
      )}
      title={label}
    >
      <Icon className="size-2.5" />
      {showLabel ? label : null}
    </span>
  );
}
