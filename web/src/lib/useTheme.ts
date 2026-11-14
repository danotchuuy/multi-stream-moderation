import { useCallback, useEffect, useState } from "react";

export type Theme = "light" | "dark";

const STORAGE_KEY = "msm.theme";

function getSystemTheme(): Theme {
  return window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

function getStoredTheme(): Theme | null {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    return stored === "light" || stored === "dark" ? stored : null;
  } catch {
    return null;
  }
}

/** useTheme manages light/dark mode: it starts from any explicit choice
 * the visitor already made (persisted in localStorage), or otherwise
 * from the OS preference — and keeps following the OS preference live
 * until toggleTheme is actually called, at which point that choice
 * takes over and persists. index.html sets the same initial value
 * synchronously (before React or CSS paints) to avoid a flash of the
 * wrong theme; this hook must stay in sync with that logic. */
export function useTheme() {
  const [theme, setTheme] = useState<Theme>(() => getStoredTheme() ?? getSystemTheme());

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
  }, [theme]);

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => {
      // Only follow the OS live if the visitor hasn't made an explicit
      // choice of their own yet.
      if (getStoredTheme() === null) {
        setTheme(getSystemTheme());
      }
    };
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, []);

  const toggleTheme = useCallback(() => {
    setTheme((prev) => {
      const next: Theme = prev === "dark" ? "light" : "dark";
      try {
        localStorage.setItem(STORAGE_KEY, next);
      } catch {
        // Private-browsing/storage-disabled: the toggle still works for
        // this page load, it just won't be remembered next time.
      }
      return next;
    });
  }, []);

  return { theme, toggleTheme };
}
