/**
 * The two markers a Palace row can carry.
 *
 * ── Why neither renders its default ────────────────────────────────────
 * An "active" badge on every row of the active view, or a "normal" badge
 * on every row of a surface where normal is the default, is a label that
 * says nothing and trains a reader to stop looking at that column. The
 * badge appears when the value is the exception, which is the only time
 * it is information.
 *
 * ── The level that cannot reach here ───────────────────────────────────
 * `highly_sensitive` is not in `PalaceSensitivity`, so this component
 * could not render it if a response somehow carried one. That is the type
 * system restating the backend's guarantee, not a second check.
 */

import { Badge } from "@/components/ui/Badge";
import { useT } from "@/lib/i18n";

import type { PalaceSensitivity, PalaceStatus } from "../api/types";
import { useStatusLabel } from "./labels";

export function StatusBadge({ status }: { status: PalaceStatus }) {
  const label = useStatusLabel()(status);
  if (status === "active") return null;
  return (
    <Badge variant="neutral" size="sm">
      {label}
    </Badge>
  );
}

export function SensitivityBadge({ sensitivity }: { sensitivity: PalaceSensitivity }) {
  const t = useT();
  if (sensitivity !== "private") return null;
  return (
    <Badge variant="info" size="sm">
      {t.app.palace.sensitivity.private}
    </Badge>
  );
}
