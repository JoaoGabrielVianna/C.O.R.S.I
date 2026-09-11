import { activeFormat } from "@/lib/i18n";
/**
 * Date of record for a memory, in the short form the module already uses
 * for conversations.
 *
 * Its own file rather than sitting next to the component that first needed
 * it: a module that exports both a component and a helper loses fast
 * refresh, and both surfaces of Memory read dates.
 */
export function memoryDate(iso: string): string {
  return activeFormat().date(iso, "short");
}
