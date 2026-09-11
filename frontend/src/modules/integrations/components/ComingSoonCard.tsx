import type { ComponentType, SVGProps } from "react";
import { IntegrationCard, IntegrationStatusPill } from "./IntegrationCard";

type Props = {
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  title: string;
  tagline: string;
  label: string;
};

export function ComingSoonCard({ icon, title, tagline, label }: Props) {
  return (
    <IntegrationCard
      icon={icon}
      iconTint="muted"
      title={title}
      tagline={tagline}
      status={<IntegrationStatusPill tone="muted">{label}</IntegrationStatusPill>}
      muted
    />
  );
}
