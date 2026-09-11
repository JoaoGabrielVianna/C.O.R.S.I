import { useT } from "@/lib/i18n";
import { ModulePlaceholder } from "./ModulePlaceholder";

export function IntelligencePage() {
  const t = useT();
  return (
    <ModulePlaceholder
      title={t.app.modules.intelligence.title}
      description={t.app.modules.intelligence.description}
      status="planned"
    />
  );
}
