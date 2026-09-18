/**
 * The filter bar.
 *
 * ── Why every control is a native element with a real label ────────────
 * A `<select>` and an `<input>` are keyboard-operable, announce their
 * current value, and work with the browser's own find-in-page. A custom
 * dropdown would need roving tabindex, `aria-activedescendant` and an
 * escape handler to reach the same place, and this screen has no visual
 * requirement that native controls fail to meet.
 *
 * ── What is NOT here, and cannot be added ──────────────────────────────
 * Any control that opts into withheld content. There is no toggle, no
 * checkbox and no hidden key: the status filter moves between `active` and
 * `archived`, which is lifecycle, not visibility. The most withheld
 * content is absent from the API, absent from the types, and therefore
 * absent from every control this file could render.
 */

import type { ReactNode } from "react";
import { Search } from "lucide-react";

import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

export function FilterBar({
  search,
  searchPlaceholder,
  onSearchChange,
  onClear,
  showClear,
  children,
}: {
  search: string;
  searchPlaceholder: string;
  onSearchChange: (value: string) => void;
  onClear: () => void;
  showClear: boolean;
  children?: ReactNode;
}) {
  const t = useT();
  return (
    <div className="flex flex-wrap items-end gap-3 px-4 pt-4 pb-3">
      <div className="min-w-[16rem] flex-1">
        <label
          htmlFor="palace-library-search"
          className="mb-1.5 block text-xs font-medium text-(--color-muted-foreground)"
        >
          {t.app.library.filters.searchLabel}
        </label>
        <div className="relative">
          <Search
            aria-hidden="true"
            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-(--color-muted-foreground)"
          />
          <Input
            id="palace-library-search"
            type="search"
            value={search}
            placeholder={searchPlaceholder}
            onChange={(e) => onSearchChange(e.target.value)}
            className="pl-9"
          />
        </div>
      </div>

      {children}

      {showClear ? (
        <Button variant="ghost" size="sm" onClick={onClear} className="h-10">
          {t.app.library.filters.clear}
        </Button>
      ) : null}
    </div>
  );
}

export function Select({
  id,
  label,
  value,
  onChange,
  children,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  children: ReactNode;
}) {
  return (
    <div>
      <label
        htmlFor={id}
        className="mb-1.5 block text-xs font-medium text-(--color-muted-foreground)"
      >
        {label}
      </label>
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={cn(
          "h-10 rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-sm",
          "text-(--color-foreground) shadow-(--shadow-soft) outline-none",
          "transition-[border-color,box-shadow] duration-[250ms]",
          "hover:border-(--color-slate-300)",
          "focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20",
        )}
      >
        {children}
      </select>
    </div>
  );
}
