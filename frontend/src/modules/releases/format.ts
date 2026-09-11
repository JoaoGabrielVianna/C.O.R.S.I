import { LOCALE_TAG, type Lang } from "@/lib/i18n";

/**
 * Dates render in the reader's locale but always as an absolute day.
 * "3 months ago" is friendlier and useless here: the question this area
 * answers is *when*, and a relative label silently changes meaning every
 * time it is read.
 *
 * UTC is forced so a release does not appear to have shipped a day earlier
 * for a reader west of the meridian. A release date is a product fact, not
 * a local timestamp.
 */
export function formatReleaseDate(iso: string | null, lang: Lang): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return new Intl.DateTimeFormat(LOCALE_TAG[lang], {
    day: "2-digit",
    month: "short",
    year: "numeric",
    timeZone: "UTC",
  }).format(d);
}
