/**
 * fallback — what a translation lookup does when a key is not there.
 *
 * ── Why a runtime guard when TypeScript already enforces parity ────────
 * `en.ts` is typed `Translations`, so a key missing from EN is a build
 * error today, and that is the primary defence. It is not the whole one:
 *
 *   1. The dictionaries are plain objects. Anything that builds a key path
 *      at runtime, or reaches a dictionary from untyped code, bypasses the
 *      compiler entirely.
 *   2. A missing key does not fail loudly, it renders `undefined` — React
 *      prints nothing. A button silently loses its label and the UI looks
 *      merely odd rather than broken, which is the failure mode that
 *      survives review longest.
 *
 * So: PT is the canonical schema and the fallback source. A dictionary is
 * deep-merged over it, which means a key that exists only in PT renders
 * its Portuguese text instead of vanishing. In development, reaching a key
 * that resolves to nothing warns with the full path.
 */
import type { Translations } from "./pt";

type Dict = Record<string, unknown>;

function isPlainObject(v: unknown): v is Dict {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/**
 * Deep-merge `override` over `base`. Arrays are replaced wholesale, never
 * merged element-wise: a translated list of seals is a different list, not
 * a patch on the Portuguese one.
 */
export function deepMerge<T>(base: T, override: unknown): T {
  if (!isPlainObject(base) || !isPlainObject(override)) {
    return (override === undefined ? base : (override as T));
  }
  const out: Dict = { ...base };
  for (const key of Object.keys(override)) {
    const o = override[key];
    if (o === undefined) continue;
    out[key] = key in base ? deepMerge(base[key], o) : o;
  }
  return out as T;
}

/** Paths already reported, so one bad key in a list does not spam the console. */
const warned = new Set<string>();

/**
 * Wrap a dictionary so that a lookup which resolves to nothing is visible.
 *
 * Returns the dotted key path as the rendered string. That is deliberate:
 * a blank label reads as a design choice, whereas `app.agents.save` on a
 * button is unmistakably a bug and names its own fix.
 */
function guard<T extends object>(dict: T, path: string[] = []): T {
  return new Proxy(dict, {
    get(target, prop, receiver) {
      if (typeof prop === "symbol" || prop === "toJSON") {
        return Reflect.get(target, prop, receiver);
      }
      const next = [...path, prop];
      const value = Reflect.get(target, prop, receiver);

      if (value === undefined) {
        const dotted = next.join(".");
        if (!warned.has(dotted)) {
          warned.add(dotted);
          console.warn(`[i18n] missing translation key: ${dotted}`);
        }
        return dotted;
      }
      if (isPlainObject(value)) return guard(value as object, next) as unknown;
      return value;
    },
  });
}

/**
 * Build the dictionary a provider hands to consumers.
 *
 * `canonical` is PT — the schema of record. `dictionary` is the one the
 * reader asked for. In development the result is guarded; in production it
 * is the plain merged object, because a Proxy on every string read in every
 * render is a cost with no user-visible benefit once the build is green.
 */
export function resolveDictionary(
  canonical: Translations,
  dictionary: Translations,
  dev = false,
): Translations {
  const merged = deepMerge(canonical, dictionary);
  return dev ? guard(merged as object) as Translations : merged;
}

/** Test seam: forget which paths were already reported. */
export function resetMissingKeyWarnings() {
  warned.clear();
}
