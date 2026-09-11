import { activeFormat } from "@/lib/i18n";
type RelativeLabels = {
  postedNow: string;
  postedRecently: string;
  unitMinutes: string;
  unitHours: string;
  unitDays: string;
};

/**
 * formatRelative — "agora" / "há 5h" / "há 3d" style.
 *
 * The `now` arg is optional so callers can pass a memoized timestamp from
 * useState/useEffect to avoid the `Date.now()`-during-render lint rule.
 */
export function formatRelative(ms: number, labels: RelativeLabels, now = ms): string {
  const reference = Math.max(ms, now);
  const delta = Math.max(0, reference - ms);
  const minutes = Math.floor(delta / 60_000);
  if (minutes < 1) return labels.postedNow;
  if (minutes < 60)
    return labels.postedRecently.replace("{{n}}", String(minutes)).replace("{{unit}}", labels.unitMinutes);
  const hours = Math.floor(minutes / 60);
  if (hours < 24)
    return labels.postedRecently.replace("{{n}}", String(hours)).replace("{{unit}}", labels.unitHours);
  const days = Math.floor(hours / 24);
  return labels.postedRecently.replace("{{n}}", String(days)).replace("{{unit}}", labels.unitDays);
}

export function daysBetween(a: number, b: number): number {
  return Math.max(0, Math.floor((b - a) / 86_400_000));
}

/**
 * `undefined` used to be passed as the locale here, which does not mean
 * "the app's language" — it means the operating system's. The date followed
 * the machine while the label beside it followed the reader.
 */
export function formatAbsolute(ms: number): string {
  return activeFormat().date(ms, "long");
}
