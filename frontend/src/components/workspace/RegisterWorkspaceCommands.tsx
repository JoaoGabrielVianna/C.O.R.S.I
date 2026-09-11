import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import {
  ArrowLeft,
  BarChart3,
  Bot,
  History,
  LayoutDashboard,
  LogOut,
  Monitor,
  Moon,
  Plug,
  Radar,
  ShieldCheck,
  SlidersHorizontal,
  SquareStack,
  Sparkles,
  Sun,
  User as UserIcon,
  Wallet,
  Languages,
} from "lucide-react";
import { useRegisterCommands, type Command } from "@/lib/command";
import { isRouteHidden } from "@/lib/navVisibility";
import { useT, useLang } from "@/lib/i18n";
import { useTheme } from "@/lib/theme";
import { useAuth } from "@/lib/auth";

/**
 * Registers the workspace's baseline commands.
 *
 * Re-runs whenever the active language, theme handler, or auth handler
 * changes — so palette titles stay localized. Modules will later call
 * `useRegisterCommands(...)` from their own entry components to push their
 * actions into the same registry while mounted.
 */
export function RegisterWorkspaceCommands() {
  const t = useT();
  const navigate = useNavigate();
  const { setTheme, resolved } = useTheme();
  const [, setLang] = useLang();
  const { signOut } = useAuth();

  const ThemeToggleIcon = resolved === "dark" ? Sun : Moon;

  const commands = useMemo<Command[]>(() => {
    const c = t.app.palette.commands;
    /** Route commands carry their target so hidden ones can be filtered out. */
    type RouteCommand = Command & { route: string };
    const nav = (id: string, title: string, route: string, icon: Command["icon"]): RouteCommand => ({
      id,
      title,
      category: "Navigation",
      icon,
      keywords: [route],
      weight: 10,
      route,
      perform: () => navigate(route),
    });
    const mod = (id: string, title: string, route: string, icon: Command["icon"]): RouteCommand => ({
      id,
      title,
      category: "Modules",
      icon,
      keywords: [route, "module"],
      weight: 8,
      route,
      perform: () => navigate(route),
    });
    const set = (id: string, title: string, route: string, icon: Command["icon"]): RouteCommand => ({
      id,
      title,
      category: "Settings",
      icon,
      keywords: [route, "settings"],
      weight: 5,
      route,
      perform: () => navigate(route),
    });

    // Same source of truth as the sidebar — a module hidden there must not be
    // reachable from ⌘K either.
    const routeCommands: RouteCommand[] = [
      // Navigation
      nav("nav.dashboard",  c.openDashboard, "/app/dashboard",  LayoutDashboard),
      // Release history is in the sidebar, and used to be the one item
      // there with no palette entry — so ⌘K denied the existence of a
      // destination the rail was advertising.
      nav("nav.releases",   c.openReleases,  "/app/releases",   History),

      // Modules
      mod("mod.jobRadar",     c.openJobRadar,     "/app/modules/job-radar", Radar),
      mod("mod.finance",      c.openFinance,      "/app/modules/finance",   Wallet),
      mod("mod.agents",       c.openAgents,       "/app/modules/agents",    Bot),
      mod("mod.intelligence", c.openIntelligence, "/app/modules/news",      BarChart3),
      mod("mod.content",      c.openContent,      "/app/modules/content",   Sparkles),

      // Settings — same order the settings shell lists them in.
      set("set.profile",      c.openProfile,      "/app/settings/profile",      UserIcon),
      set("set.integrations", c.openIntegrations, "/app/settings/integrations", Plug),
      set("set.preferences",  c.openPreferences,  "/app/settings/preferences",  SlidersHorizontal),
      set("set.modules",      c.openModules,      "/app/settings/modules",      SquareStack),
      set("set.security",     c.openSecurity,     "/app/settings/security",     ShieldCheck),
    ].filter((cmd) => !isRouteHidden(cmd.route));

    return [
      ...routeCommands,

      // Actions — theme
      {
        id: "act.theme.light",
        title: c.themeLight,
        category: "Actions",
        icon: Sun,
        keywords: ["theme", "tema", "light", "claro"],
        perform: () => setTheme("light"),
      },
      {
        id: "act.theme.dark",
        title: c.themeDark,
        category: "Actions",
        icon: Moon,
        keywords: ["theme", "tema", "dark", "escuro"],
        perform: () => setTheme("dark"),
      },
      {
        id: "act.theme.system",
        title: c.themeSystem,
        category: "Actions",
        icon: Monitor,
        keywords: ["theme", "tema", "system", "sistema"],
        perform: () => setTheme("system"),
      },
      {
        id: "act.theme.toggle",
        title: c.themeToggle,
        category: "Actions",
        icon: ThemeToggleIcon,
        keywords: ["theme", "tema", "toggle", "alternar"],
        weight: 4,
        perform: () => setTheme(resolved === "dark" ? "light" : "dark"),
      },

      // Actions — language
      {
        id: "act.lang.pt",
        title: c.languagePt,
        category: "Actions",
        icon: Languages,
        keywords: ["language", "idioma", "português", "portuguese", "pt", "br"],
        perform: () => setLang("pt"),
      },
      {
        id: "act.lang.en",
        title: c.languageEn,
        category: "Actions",
        icon: Languages,
        keywords: ["language", "idioma", "english", "inglês", "en"],
        perform: () => setLang("en"),
      },

      // Account
      {
        id: "acc.signOut",
        title: c.signOut,
        category: "Account",
        icon: LogOut,
        keywords: ["sign out", "logout", "sair", "encerrar"],
        weight: 2,
        perform: async () => {
          await signOut();
          navigate("/login", { replace: true });
        },
      },
      {
        id: "acc.backToLanding",
        title: c.backToLanding,
        category: "Account",
        icon: ArrowLeft,
        keywords: ["site", "landing", "back"],
        perform: () => navigate("/"),
      },
    ];
  }, [t, navigate, setTheme, setLang, signOut, resolved, ThemeToggleIcon]);

  useRegisterCommands(commands);

  return null;
}
