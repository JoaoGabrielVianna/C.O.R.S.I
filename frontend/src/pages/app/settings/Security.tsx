import { SettingsPanel, SettingsRow } from "@/components/workspace";
import { Badge } from "@/components/ui/Badge";
import { useT } from "@/lib/i18n";

export function SecuritySettingsPage() {
  const t = useT();
  return (
    <SettingsPanel
      title={t.app.settings.security.title}
      description={t.app.settings.security.description}
    >
      <SettingsRow label={t.app.settings.security.provider.label}>
        <div className="flex items-center gap-2">
          <Badge variant="warning" size="sm">{t.app.common.mock}</Badge>
          <span className="font-mono text-[12px] text-(--color-muted-foreground)">
            {t.app.settings.security.provider.value}
          </span>
        </div>
      </SettingsRow>
      <SettingsRow label={t.app.settings.security.session.label} hint={t.app.settings.security.note}>
        <span className="font-mono text-[12px] text-(--color-muted-foreground)">
          {t.app.settings.security.session.value}
        </span>
      </SettingsRow>
    </SettingsPanel>
  );
}
