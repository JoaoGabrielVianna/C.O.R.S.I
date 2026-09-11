/**
 * USD → BRL exchange rate, fetched live from a public source.
 *
 * The backend prices everything in USD because that is what the gateway
 * bills. Showing the number in reais is a presentation concern, so the
 * conversion lives here on the client and never touches the stored cost.
 *
 * Source: open.er-api.com — no key, CORS-enabled, updated daily. It is a
 * best-effort convenience: if it is unreachable the UI falls back to USD
 * rather than inventing a rate.
 */

const PRIMARY = "https://open.er-api.com/v6/latest/USD";
// Frankfurter (ECB) as a second try if the primary is down.
const FALLBACK = "https://api.frankfurter.dev/v1/latest?from=USD&to=BRL";

export interface FxRate {
  /** How many BRL one USD buys. */
  rate: number;
  /** Human date the rate was published (source's own string). */
  asOf: string;
  source: string;
}

async function fetchJSON(url: string, signal: AbortSignal): Promise<unknown> {
  const res = await fetch(url, { signal, headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`fx source ${res.status}`);
  return res.json();
}

export async function getUsdToBrl(): Promise<FxRate> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 8000);
  try {
    try {
      const d = (await fetchJSON(PRIMARY, controller.signal)) as {
        rates?: { BRL?: number };
        time_last_update_utc?: string;
      };
      const rate = d.rates?.BRL;
      if (typeof rate === "number" && rate > 0) {
        return { rate, asOf: d.time_last_update_utc ?? "", source: "open.er-api.com" };
      }
    } catch {
      // fall through to the backup source
    }
    const d2 = (await fetchJSON(FALLBACK, controller.signal)) as {
      rates?: { BRL?: number };
      date?: string;
    };
    const rate2 = d2.rates?.BRL;
    if (typeof rate2 === "number" && rate2 > 0) {
      return { rate: rate2, asOf: d2.date ?? "", source: "frankfurter" };
    }
    throw new Error("no BRL rate in either source");
  } finally {
    clearTimeout(timeout);
  }
}
