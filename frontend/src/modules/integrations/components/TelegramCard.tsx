import { useState } from "react";
import { Check, CheckCircle2, Copy, ExternalLink, Loader2, Send } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { cn } from "@/lib/utils";
import { useT, useFormat } from "@/lib/i18n";
import { useTelegramConnection } from "../hooks/useTelegramConnection";
import type { TelegramConnection, TelegramPairing, TelegramStatus } from "../types";
import { IntegrationCard, IntegrationStatusPill } from "./IntegrationCard";
import { TelegramGlyph } from "./integrationIcons";
import { FinanceModulePanel } from "./FinanceModulePanel";

export function TelegramCard() {
  const t = useT();
  const tg = t.app.settings.integrations.telegram;
  const { status, loading, busy, test, remainingMs, actions } = useTelegramConnection();

  if (loading) {
    return (
      <IntegrationCard
        icon={TelegramGlyph}
        title={tg.title}
        tagline={tg.tagline}
        status={<IntegrationStatusPill tone="muted">···</IntegrationStatusPill>}
      >
        <div className="h-16 animate-pulse rounded-lg bg-(--color-muted)/40" />
      </IntegrationCard>
    );
  }

  return renderForStatus({ status, tg, busy, test, remainingMs, actions });
}

type RenderArgs = {
  status: TelegramStatus;
  tg: ReturnType<typeof useT>["app"]["settings"]["integrations"]["telegram"];
  busy: ReturnType<typeof useTelegramConnection>["busy"];
  test: ReturnType<typeof useTelegramConnection>["test"];
  remainingMs: number;
  actions: ReturnType<typeof useTelegramConnection>["actions"];
};

function renderForStatus(args: RenderArgs) {
  if (args.status.kind === "connecting") return <ConnectingView {...args} pairing={args.status.pairing} />;
  if (args.status.kind === "connected") return <ConnectedView {...args} connection={args.status.connection} />;
  return <DisconnectedView {...args} />;
}

function DisconnectedView({ tg, busy, actions }: RenderArgs) {
  return (
    <IntegrationCard
      icon={TelegramGlyph}
      title={tg.title}
      tagline={tg.tagline}
      status={<IntegrationStatusPill tone="muted">{tg.statusDisconnected}</IntegrationStatusPill>}
      actions={
        <Button
          size="sm"
          variant="primary"
          onClick={() => actions.connect()}
          disabled={busy === "connect"}
        >
          {busy === "connect" ? <Loader2 className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}
          {tg.actions.connect}
        </Button>
      }
    />
  );
}

function ConnectingView({ tg, busy, remainingMs, actions, pairing }: RenderArgs & { pairing: TelegramPairing }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const mins = Math.floor(remainingMs / 60000);
  const secs = Math.floor((remainingMs % 60000) / 1000);
  const countdown = `${mins.toString().padStart(2, "0")}:${secs.toString().padStart(2, "0")}`;
  const command = `/start ${pairing.code}`;

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* no-op */
    }
  };

  return (
    <IntegrationCard
      icon={TelegramGlyph}
      title={tg.title}
      tagline={tg.tagline}
      status={<IntegrationStatusPill tone="warning">{tg.statusConnecting}</IntegrationStatusPill>}
      actions={
        <>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => actions.cancel()}
            disabled={busy === "cancel"}
          >
            {tg.actions.cancel}
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => actions.simulate()}
            disabled={busy === "simulate"}
            title={t.app.settings.telegram.devHelper}
          >
            {busy === "simulate" ? <Loader2 className="size-3.5 animate-spin" /> : null}
            {tg.actions.simulate}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <p className="text-[13px] font-medium text-(--color-foreground)">{tg.connecting.heading}</p>

        <ol className="space-y-2 text-[12.5px] text-(--color-muted-foreground)">
          <Step n={1}>{tg.connecting.step1}</Step>
          <Step n={2}>
            {tg.connecting.step2}{" "}
            <span className="font-mono text-(--color-foreground)">{tg.connecting.botHandle}</span>
          </Step>
          <Step n={3}>{tg.connecting.step3}</Step>
        </ol>

        <div className="grid gap-3 rounded-xl border border-(--color-border) bg-(--color-background)/40 p-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
          <div className="min-w-0 space-y-1">
            <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
              {tg.connecting.codeLabel}
            </p>
            <p className="truncate font-mono text-[16px] font-semibold tracking-[0.04em] text-(--color-foreground)">
              /start <span className="text-(--color-brand-500)">{pairing.code}</span>
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Button size="sm" variant="outline" onClick={copy}>
              {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
              {copied ? tg.actions.copied : tg.actions.copy}
            </Button>
            <Button size="sm" variant="primary" asChild>
              <a href={pairing.deepLink} target="_blank" rel="noreferrer">
                <ExternalLink className="size-3.5" />
                {tg.connecting.openTelegram}
              </a>
            </Button>
          </div>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-2 text-[12px] text-(--color-muted-foreground)">
          <span className="inline-flex items-center gap-2">
            <Loader2 className="size-3 animate-spin text-(--color-brand-500)" />
            {tg.connecting.polling}
          </span>
          <span className="font-mono">
            {tg.connecting.expiresIn} {countdown}
          </span>
        </div>
      </div>
    </IntegrationCard>
  );
}

function Step({ n, children }: { n: number; children: React.ReactNode }) {
  return (
    <li className="flex items-start gap-2.5">
      <span className="mt-0.5 inline-flex size-4 shrink-0 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card) font-mono text-[10px] text-(--color-muted-foreground)">
        {n}
      </span>
      <span>{children}</span>
    </li>
  );
}

function ConnectedView({ tg, busy, test, actions, connection }: RenderArgs & { connection: TelegramConnection }) {
  const fmt = useFormat();
  const connectedOn = fmt.date(connection.connectedAt, "long");

  return (
    <IntegrationCard
      icon={TelegramGlyph}
      title={tg.title}
      tagline={tg.tagline}
      status={<IntegrationStatusPill tone="active">{tg.statusConnected}</IntegrationStatusPill>}
      actions={
        <>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => actions.disconnect()}
            disabled={busy === "disconnect"}
          >
            {tg.actions.disconnect}
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => actions.runTest()}
            disabled={test.state === "running"}
          >
            {test.state === "running" ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : test.state === "ok" ? (
              <CheckCircle2 className="size-3.5 text-(--color-brand-500)" />
            ) : (
              <Send className="size-3.5" />
            )}
            {test.state === "running" ? tg.actions.sending : tg.actions.test}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label={tg.connected.account}>
            <span className="font-mono text-[13px] text-(--color-foreground)">@{connection.username}</span>
          </Field>
          <Field label={tg.connected.since}>
            <span className="text-[13px] text-(--color-foreground)">{connectedOn}</span>
          </Field>
        </dl>

        {test.state === "ok" || test.state === "fail" ? (
          <p
            className={cn(
              "inline-flex items-center gap-2 rounded-lg border px-3 py-1.5 text-[12px]",
              test.state === "ok"
                ? "border-(--color-brand-500)/30 bg-(--color-brand-500)/10 text-(--color-brand-500)"
                : "border-rose-400/30 bg-rose-400/10 text-rose-300",
            )}
          >
            {test.state === "ok" ? <CheckCircle2 className="size-3.5" /> : null}
            {test.state === "ok" ? tg.connected.testReply : tg.connected.testFailed}
          </p>
        ) : null}

        <div className="space-y-2">
          <p className="font-mono text-[10.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
            {tg.connected.modules}
          </p>
          <ModuleRow
            label={tg.modules.finance}
            hint={tg.modules.financeHint}
            checked={connection.modules.finance}
            onChange={(on) => actions.toggleModule("finance", on)}
          />
          <ModuleRow
            label={tg.modules.crm}
            hint={tg.modules.crmHint}
            checked={false}
            disabled
            soon
          />
          <ModuleRow
            label={tg.modules.calendar}
            hint={tg.modules.calendarHint}
            checked={false}
            disabled
            soon
          />
        </div>

        {connection.modules.finance ? (
          <>
            <div className="h-px bg-(--color-border)" />
            <FinanceModulePanel />
          </>
        ) : null}
      </div>
    </IntegrationCard>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0 space-y-1">
      <dt className="font-mono text-[10.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {label}
      </dt>
      <dd className="truncate">{children}</dd>
    </div>
  );
}

function ModuleRow({
  label,
  hint,
  checked,
  disabled = false,
  soon = false,
  onChange,
}: {
  label: string;
  hint: string;
  checked: boolean;
  disabled?: boolean;
  soon?: boolean;
  onChange?: (on: boolean) => void;
}) {
  const t = useT();
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => !disabled && onChange?.(!checked)}
      className={cn(
        "flex w-full items-center justify-between gap-4 rounded-xl border border-(--color-border) bg-(--color-card) px-4 py-3 text-left transition-colors",
        !disabled && "hover:bg-(--color-muted)/50",
        disabled && "opacity-60",
      )}
    >
      <span className="min-w-0">
        <span className="flex items-center gap-2">
          <span className="text-[13px] font-medium text-(--color-foreground)">{label}</span>
          {soon ? (
            <span className="rounded-full border border-(--color-border) px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {t.app.settings.telegram.soon}
            </span>
          ) : null}
        </span>
        <span className="mt-0.5 block text-[12px] text-(--color-muted-foreground)">{hint}</span>
      </span>
      <span
        aria-hidden
        className={cn(
          "inline-flex h-6 w-10 shrink-0 items-center rounded-full border px-0.5 transition-colors",
          checked
            ? "border-(--color-brand-500) bg-(--color-brand-500)"
            : "border-(--color-border) bg-(--color-muted)",
        )}
      >
        <span
          className={cn(
            "flex size-4.5 items-center justify-center rounded-full bg-(--color-card) shadow-(--shadow-soft) transition-transform",
            checked ? "translate-x-4" : "translate-x-0",
          )}
        >
          {checked ? <Check className="size-3 text-(--color-brand-600)" strokeWidth={3} /> : null}
        </span>
      </span>
    </button>
  );
}
