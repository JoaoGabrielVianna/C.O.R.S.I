import { createContext } from "react";

export type Theme = "light" | "dark" | "system";
export type Resolved = "light" | "dark";

export type ThemeContextValue = {
  theme: Theme;
  resolved: Resolved;
  setTheme: (theme: Theme) => void;
};

export const ThemeContext = createContext<ThemeContextValue | null>(null);
