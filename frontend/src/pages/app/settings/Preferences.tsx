import { SettingsPanel, SettingsRow } from "@/components/workspace";
import { ThemeToggle } from "@/components/ui/ThemeToggle";
import { LanguageSwitcher } from "@/components/ui/LanguageSwitcher";
import { useT } from "@/lib/i18n";

/**
 * The "Navegação do módulo Agents" row used to live here, switching where
 * the module's Chat / Configuração selector was drawn. That selector no
 * longer exists — Agents navigates by URL now — so the control had nothing
 * left to move, and a preference that changes nothing is worse than an
 * absent one.
 */
export function PreferencesSettingsPage() {
  const t = useT();
  return (
    <SettingsPanel
      title={t.app.settings.preferences.title}
      description={t.app.settings.preferences.description}
    >
      <SettingsRow
        label={t.app.settings.preferences.theme.label}
        hint={t.app.settings.preferences.theme.hint}
      >
        <ThemeToggle />
      </SettingsRow>
      <SettingsRow
        label={t.app.settings.preferences.language.label}
        hint={t.app.settings.preferences.language.hint}
      >
        <LanguageSwitcher />
      </SettingsRow>
      <SettingsRow
        label={t.app.settings.preferences.experimental.label}
        hint={t.app.settings.preferences.experimental.hint}
      >
        <span className="font-mono text-[12px] text-(--color-muted-foreground)">—</span>
      </SettingsRow>
    </SettingsPanel>
  );
}
