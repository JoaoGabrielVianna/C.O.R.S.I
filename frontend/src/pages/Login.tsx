import { useEffect, useState, type FormEvent } from "react";
import { motion } from "framer-motion";
import { Link, useLocation, useNavigate } from "react-router-dom";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  Eye,
  EyeOff,
  LoaderCircle,
  Lock,
  Mail,
  ShieldCheck,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import { LogoLockup } from "@/components/ui/Logo";
import { LanguageSwitcher } from "@/components/ui/LanguageSwitcher";
import { ThemeToggle } from "@/components/ui/ThemeToggle";
import { useT } from "@/lib/i18n";
import { useAuth } from "@/lib/auth";
import { safeReturnTo } from "@/lib/auth/returnTo";

/**
 * Login — single-form private access.
 *
 * Desktop (lg+): split `1fr / 1.2fr` (~45/55). LEFT = email/password form
 * centered between top bar + footer legal. RIGHT = always-dark ambient panel
 * (blobs + dot-grid + identity + module preview + stats band). Locked to
 * `h-screen overflow-hidden` on lg+ so neither column nor the document scrolls.
 *
 * Below lg: only the auth column renders, with natural scroll when needed.
 *
 * C.O.R.S.I is private — no registration, no OAuth, no public onboarding.
 * Accounts are provisioned manually. You either have access, or you do not.
 */

const ease = [0.16, 1, 0.3, 1] as const;

type State = "idle" | "loading" | "success";

export function LoginPage() {
  const t = useT();
  const navigate = useNavigate();
  const location = useLocation();
  const { signIn, isAuthenticated } = useAuth();

  // Where the guard was trying to send them. Validated, because router
  // state is attacker-reachable through a crafted link — see safeReturnTo.
  const returnTo = safeReturnTo((location.state as { from?: unknown } | null)?.from);
  const [state, setState] = useState<State>("idle");
  const [error, setError] = useState<string | null>(null);
  const [showPwd, setShowPwd] = useState(false);

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [remember, setRemember] = useState(true);

  useEffect(() => {
    if (isAuthenticated) navigate(returnTo, { replace: true });
  }, [isAuthenticated, navigate, returnTo]);

  const onSubmit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setError(null);
    if (!email || !password) return setError(t.auth.errors.empty);
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email))
      return setError(t.auth.errors.emailInvalid);
    if (password.length < 6) return setError(t.auth.errors.passwordShort);
    setState("loading");
    try {
      await signIn(email, password);
      setState("success");
      window.setTimeout(() => navigate(returnTo, { replace: true }), 600);
    } catch {
      setState("idle");
      setError(t.auth.errors.empty);
    }
  };

  return (
    <div className="min-h-dvh bg-(--color-background) text-(--color-foreground) lg:h-screen lg:overflow-hidden">
      <div className="grid min-h-dvh lg:h-screen lg:grid-cols-[1fr_1.2fr]">

        {/* ── LEFT — Auth column ─────────────────────────────────────────── */}
        <div className="relative flex min-h-dvh flex-col lg:min-h-0 lg:h-screen">

          {/* Top bar */}
          <header className="flex shrink-0 items-center justify-between px-6 py-4 sm:px-10 lg:px-12">
            <Link to="/" className="flex items-center gap-2.5" aria-label="C.O.R.S.I">
              <LogoLockup />
            </Link>
            <div className="flex items-center gap-2">
              <ThemeToggle />
              <LanguageSwitcher />
              <Link
                to="/"
                className="hidden items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[13px] font-medium text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground) sm:inline-flex"
              >
                <ArrowLeft className="size-3.5" />
                {t.nav.backToSite}
              </Link>
            </div>
          </header>

          {/* Form — vertically centered, no scroll */}
          <main className="flex flex-1 items-center justify-center overflow-y-auto px-6 py-4 sm:px-10 lg:px-12 lg:overflow-hidden">
            <motion.div
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.5, ease }}
              className="w-full max-w-sm"
            >
              <span className="eyebrow">{t.auth.eyebrow}</span>
              <h1 className="mt-2.5 font-display text-[26px] font-bold leading-[1.1] tracking-tight text-(--color-foreground) md:text-[28px]">
                {t.auth.title}
              </h1>
              <p className="mt-2 text-[13.5px] leading-relaxed text-(--color-muted-foreground)">
                {t.auth.body}
              </p>

              {state === "success" ? (
                <div className="mt-6 rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-4 text-sm text-emerald-800 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-200">
                  <div className="flex items-start gap-3">
                    <span className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full bg-emerald-200 dark:bg-emerald-500/30">
                      <Check className="size-3 text-emerald-700 dark:text-emerald-200" strokeWidth={3} />
                    </span>
                    <p className="leading-relaxed">{t.auth.actions.success}</p>
                  </div>
                </div>
              ) : (
                <form onSubmit={onSubmit} noValidate className="mt-6 space-y-3">
                  <div className="space-y-1">
                    <Label htmlFor="auth-email">{t.auth.fields.email}</Label>
                    <div className="relative">
                      <Mail aria-hidden className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-(--color-muted-foreground)" />
                      <Input
                        id="auth-email"
                        type="email"
                        autoComplete="email"
                        placeholder={t.auth.fields.emailPlaceholder}
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                        className="pl-10"
                      />
                    </div>
                  </div>

                  <div className="space-y-1">
                    <Label htmlFor="auth-pwd">{t.auth.fields.password}</Label>
                    <div className="relative">
                      <Lock aria-hidden className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-(--color-muted-foreground)" />
                      <Input
                        id="auth-pwd"
                        type={showPwd ? "text" : "password"}
                        autoComplete="current-password"
                        placeholder={t.auth.fields.passwordPlaceholder}
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        className="pl-10 pr-10"
                      />
                      <button
                        type="button"
                        onClick={() => setShowPwd((s) => !s)}
                        aria-label={showPwd ? t.auth.fields.hidePwd : t.auth.fields.showPwd}
                        className="absolute right-2 top-1/2 flex size-7 -translate-y-1/2 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
                      >
                        {showPwd ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                      </button>
                    </div>
                  </div>

                  <label className="flex items-center gap-2.5 select-none">
                    <button
                      type="button"
                      role="checkbox"
                      aria-checked={remember}
                      onClick={() => setRemember((s) => !s)}
                      className={cn(
                        "flex size-4 shrink-0 items-center justify-center rounded-[5px] border transition-colors duration-[250ms]",
                        remember
                          ? "border-(--color-brand-500) bg-(--color-brand-500) text-white"
                          : "border-(--color-border-strong) bg-(--color-card) hover:border-(--color-muted-foreground)",
                      )}
                    >
                      {remember ? <Check className="size-3" strokeWidth={3.5} /> : null}
                    </button>
                    <span className="text-[13px] text-(--color-muted-foreground)">
                      {t.auth.fields.remember}
                    </span>
                  </label>

                  {error ? (
                    <motion.div
                      initial={{ opacity: 0, y: -4 }}
                      animate={{ opacity: 1, y: 0 }}
                      transition={{ duration: 0.25, ease }}
                      className="flex items-start gap-2 rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-[13px] text-rose-700 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-300"
                    >
                      <span className="mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full bg-rose-200 text-rose-700 text-[10px] font-bold dark:bg-rose-500/30 dark:text-rose-200">!</span>
                      <span>{error}</span>
                    </motion.div>
                  ) : null}

                  <Button
                    type="submit"
                    size="lg"
                    disabled={state === "loading"}
                    className="mt-1 w-full shadow-(--shadow-glow)"
                  >
                    {state === "loading" ? (
                      <>
                        <LoaderCircle className="size-4 animate-spin" />
                        {t.auth.actions.signingIn}
                      </>
                    ) : (
                      <>
                        {t.auth.actions.signIn}
                        <ArrowRight className="size-4" />
                      </>
                    )}
                  </Button>
                </form>
              )}
            </motion.div>
          </main>

          {/* Footer legal */}
          <footer className="flex shrink-0 items-center justify-between gap-3 px-6 py-3 text-[11px] text-(--color-muted-foreground) sm:px-10 lg:px-12">
            <p className="inline-flex items-center gap-1.5 font-mono">
              <ShieldCheck className="size-3 text-(--color-brand-600) dark:text-(--color-brand-400)" />
              {t.auth.legal}
            </p>
            <p>© {new Date().getFullYear()} João Corsi</p>
          </footer>
        </div>

        {/* ── RIGHT — Always-dark visual panel ──────────────────────────── */}
        <aside className="relative isolate hidden overflow-hidden bg-(--color-slate-950) text-white lg:flex lg:h-screen lg:flex-col lg:justify-between">
          {/* Ambient blobs */}
          <div className="pointer-events-none absolute -left-24 top-16 size-[420px] rounded-full bg-(--color-brand-600)/40 blur-3xl animate-blob" />
          <div className="pointer-events-none absolute right-[-8%] top-[28%] size-[360px] rounded-full bg-sky-500/12 blur-3xl animate-blob" />
          <div className="pointer-events-none absolute -bottom-24 left-1/3 size-[460px] rounded-full bg-(--color-brand-800)/30 blur-3xl animate-blob" />
          <div className="pointer-events-none absolute inset-0 dot-grid-dark opacity-40" />

          {/* Top row */}
          <div className="relative flex items-center justify-between px-12 py-6">
            <span className="font-mono text-[11px] uppercase tracking-[0.18em] text-white/55">
              {t.app.common.shared.workspacePreview}
            </span>
            <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-400/30 bg-emerald-500/15 px-2 py-0.5 text-[10.5px] font-semibold text-emerald-300">
              <span className="size-1 animate-pulse rounded-full bg-emerald-400" />
              {t.meta.status} · {t.meta.version}
            </span>
          </div>

          {/* Identity + module preview cards */}
          <div className="relative flex-1 overflow-hidden px-12">
            <div className="flex h-full flex-col justify-center">
              <motion.div
                initial={{ opacity: 0, y: 16 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ duration: 0.6, ease, delay: 0.1 }}
                className="max-w-md"
              >
                <p className="font-display text-[44px] font-bold leading-none tracking-tight">
                  <span className="text-brand-gradient">C.O.R.S.I</span>
                </p>
                <p className="mt-3 max-w-sm text-[14px] leading-relaxed text-white/70">
                  {t.app.common.shared.loginTagline}
                </p>
              </motion.div>

              <motion.ul
                initial="hidden"
                animate="show"
                variants={{ hidden: {}, show: { transition: { staggerChildren: 0.06, delayChildren: 0.25 } } }}
                className="mt-7 max-w-md divide-y divide-white/[0.06] border-y border-white/[0.06]"
              >
                {[
                  { name: "Application Tracker AI", building: true },
                  { name: "Finance AI",             building: true },
                  { name: "Market Intelligence",    building: false },
                  { name: "Content Engine",         building: false },
                ].map((m) => (
                  <motion.li
                    key={m.name}
                    variants={{
                      hidden: { opacity: 0, x: -6 },
                      show: { opacity: 1, x: 0, transition: { duration: 0.4, ease } },
                    }}
                    className="flex items-center justify-between py-2.5"
                  >
                    <span className="inline-flex items-center gap-2.5 text-[13px] text-white/85">
                      <span
                        aria-hidden
                        className={cn(
                          "size-1.5 rounded-full",
                          m.building ? "bg-(--color-brand-400)" : "bg-white/25",
                        )}
                      />
                      {m.name}
                    </span>
                    <span className="inline-flex items-center gap-2">
                      <span
                        className={cn(
                          "font-mono text-[10px] uppercase tracking-[0.16em]",
                          m.building ? "text-(--color-brand-300)" : "text-white/45",
                        )}
                      >
                        {m.building ? "building" : "planned"}
                      </span>
                      {m.building ? (
                        <span className="font-mono text-[10px] tracking-[0.06em] text-white/45">
                          v0.0.0
                        </span>
                      ) : null}
                    </span>
                  </motion.li>
                ))}
              </motion.ul>
            </div>
          </div>

          {/* Stats band */}
          <motion.div
            initial={{ opacity: 0, y: 16 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.6, ease, delay: 0.45 }}
            className="relative mx-12 mb-8 grid grid-cols-3 gap-px overflow-hidden rounded-xl bg-white/10 ring-1 ring-white/10"
          >
            {[
              { v: "2",    l: "building" },
              { v: "1",    l: "operator" },
              { v: "Beta", l: "v0.0.0" },
            ].map((s) => (
              <div key={s.l} className="bg-(--color-slate-950)/80 px-4 py-3 backdrop-blur">
                <p className="font-display text-xl font-bold tracking-tight text-white tabular">
                  {s.v}
                </p>
                <p className="mt-0.5 text-[10px] uppercase tracking-wider text-white/55">
                  {s.l}
                </p>
              </div>
            ))}
          </motion.div>
        </aside>
      </div>
    </div>
  );
}
