import { useT } from "@/lib/i18n";
import { ModulePlaceholder } from "./ModulePlaceholder";

export function ContentPage() {
  const t = useT();
  return (
    <ModulePlaceholder
      title={t.app.modules.content.title}
      description={t.app.modules.content.description}
      status="planned"
    />
  );
}
