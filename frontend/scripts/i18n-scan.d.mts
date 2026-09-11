/**
 * Types for the scanner. It stays plain `.mjs` so it can be run directly
 * with `node scripts/i18n-scan.mjs` without a build step — the report is
 * meant to be readable on demand, not only inside a test run.
 */
export declare function scanFile(absPath: string): string[];
export declare function scanAll(): Record<string, string[]>;
export declare const INTENTIONAL_EXCLUSIONS: Record<string, string>;
