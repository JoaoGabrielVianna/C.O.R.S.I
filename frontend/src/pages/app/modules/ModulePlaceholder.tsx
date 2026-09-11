import { Construction } from "lucide-react";
import { EmptyState, PageHeader, SectionCard } from "@/components/workspace";
import { useT } from "@/lib/i18n";

type Props = {
  title: string;
  description: string;
  status: "building" | "planned";
};

/**
 * ModulePlaceholder — shared shell for every module that hasn't shipped yet.
 * Surfaces an honest empty state without fake fixtures.
 */
export function ModulePlaceholder({ title, description, status }: Props) {
  const t = useT();
  const statusLabel = status === "building" ? t.app.common.building : t.app.common.planned;

  return (
    <div className="max-w-4xl space-y-8">
      <PageHeader
        eyebrow={`${statusLabel} · v0.0.0`}
        title={title}
        description={description}
      />
      <SectionCard>
        <EmptyState
          icon={Construction}
          title={t.app.modules.comingSoon.title}
          description={t.app.modules.comingSoon.body}
        />
      </SectionCard>
    </div>
  );
}
