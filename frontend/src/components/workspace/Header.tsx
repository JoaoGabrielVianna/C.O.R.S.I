import { Menu } from "lucide-react";
import { ThemeToggle } from "@/components/ui/ThemeToggle";
import { LanguageSwitcher } from "@/components/ui/LanguageSwitcher";
import { CommandPaletteTrigger } from "@/components/workspace/CommandPaletteTrigger";
import { NotificationCenter } from "@/components/workspace/NotificationCenter";
import { UserMenu } from "@/components/workspace/UserMenu";
import { useT } from "@/lib/i18n";
import { useWorkspace } from "@/lib/workspace";

/**
 * Header — sticky top bar inside the workspace shell.
 *
 * Left: mobile drawer trigger + command-palette trigger (replaces a real
 * search input; the palette owns the search experience). Right: notification
 * center, theme, language, user menu.
 */
export function Header() {
  const t = useT();
  const { openMobile } = useWorkspace();

  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-3 border-b border-(--color-border) bg-(--color-background)/85 px-4 backdrop-blur supports-[backdrop-filter]:bg-(--color-background)/65 sm:px-6">
      <button
        type="button"
        onClick={openMobile}
        aria-label={t.app.header.openSidebar}
        className="flex size-9 items-center justify-center rounded-lg text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground) lg:hidden"
      >
        <Menu className="size-4" />
      </button>

      <div className="flex-1">
        <CommandPaletteTrigger />
      </div>

      <div className="flex items-center gap-1.5">
        <NotificationCenter />
        <ThemeToggle className="hidden sm:inline-flex" />
        <LanguageSwitcher className="hidden sm:inline-flex" />
        <UserMenu />
      </div>
    </header>
  );
}
