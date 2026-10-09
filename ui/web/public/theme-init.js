/*
 * Pre-paint theme bootstrap.
 *
 * Runs before the app bundle so the persisted theme class is present on <html>
 * before first paint (no dark/light flash). Mirrors the logic in
 * src/components/providers/theme-provider.tsx against the zustand persist key
 * "goclaw:ui" (see src/stores/use-ui-store.ts). Default is "dark".
 */
(function () {
  var theme = "dark";
  try {
    var raw = window.localStorage.getItem("goclaw:ui");
    if (raw) {
      var parsed = JSON.parse(raw);
      var stored = parsed && parsed.state && parsed.state.theme;
      if (stored === "light" || stored === "dark" || stored === "system") {
        theme = stored;
      }
    }
  } catch (err) {
    /* localStorage unavailable or malformed JSON — fall back to default */
  }

  if (theme === "system") {
    var prefersDark =
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches;
    theme = prefersDark ? "dark" : "light";
  }

  document.documentElement.classList.remove("light", "dark");
  document.documentElement.classList.add(theme);
})();
