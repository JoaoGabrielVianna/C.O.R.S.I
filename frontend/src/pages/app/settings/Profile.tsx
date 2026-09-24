import { SettingsPanel, SettingsRow } from "@/components/workspace";
import { Badge } from "@/components/ui/Badge";
import { useAuth } from "@/lib/auth";
import { useT } from "@/lib/i18n";

/**
 * Profile — who is authenticated in this workspace.
 *
 * ── What this used to claim ────────────────────────────────────────────
 * Four `<Input>` boxes, all `readOnly`. Two of them — timezone and
 * language — were not read from anywhere: the timezone was the literal
 * string "America/Sao_Paulo" and the language said "PT-BR" even while the
 * interface around it was rendering in English. A field that reports a
 * value nobody set is a fabricated record, and an input box that refuses
 * every keystroke is a false affordance. Both are gone: the two real
 * fields are shown as values, and language is a preference that actually
 * works, on the Preferences section.
 *
 * ── Why the old /app/account folded into this page ─────────────────────
 * It showed name, email, roles, provider and auth status under the title
 * "Account", one menu item away from a "Profile" showing name and email
 * under the title "Profile". Two pages for one subject, and neither
 * complete. This is the surviving one; `/app/account` redirects here.
 */
export function ProfileSettingsPage() {
  const t = useT();
  const { user, isAuthenticated } = useAuth();
  const p = t.app.settings.profile;

  // One provider, and it is real. The amber "mock" badge this used to be
  // able to draw would now be a lie: the session is a server-issued
  // HttpOnly cookie, not a localStorage flag.
  const providerLabel = p.providerLabels.session;

  return (
    <SettingsPanel title={p.title} description={p.description}>
      <SettingsRow label={p.fields.name}>
        <span className="text-sm text-(--color-foreground)">{user?.name ?? "—"}</span>
      </SettingsRow>
      <SettingsRow label={p.fields.email}>
        <span className="text-sm text-(--color-foreground)">{user?.email ?? "—"}</span>
      </SettingsRow>
      <SettingsRow label={t.app.common.shared.roles}>
        <div className="flex flex-wrap gap-1.5">
          {(user?.roles ?? []).map((r) => (
            <Badge key={r} variant="outline" size="sm">
              {r}
            </Badge>
          ))}
        </div>
      </SettingsRow>
      <SettingsRow label={p.provider}>
        <div className="flex items-center gap-2">
          <Badge variant="success" size="sm">{t.app.common.shared.live}</Badge>
          <span className="font-mono text-[12px] text-(--color-muted-foreground)">
            {providerLabel}
          </span>
        </div>
      </SettingsRow>
      <SettingsRow label={p.session} hint={p.readOnlyNote}>
        <span className="font-mono text-[12px] text-(--color-muted-foreground)">
          {isAuthenticated ? p.authState.yes : p.authState.no}
        </span>
      </SettingsRow>
    </SettingsPanel>
  );
}
