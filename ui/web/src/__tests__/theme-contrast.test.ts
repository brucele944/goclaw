/**
 * Theme contrast guard (WCAG 2.2 AA).
 *
 * Parses the OKLCH design tokens in `src/index.css` for `:root` (light) and
 * `.dark`, converts them to relative luminance and asserts that the text/surface
 * pairs the UI actually renders reach 4.5:1 and that form-control borders reach
 * 3:1 (SC 1.4.11 non-text contrast) in both themes.
 */
import { describe, it, expect } from "vitest";
import { readFileSync } from "fs";
import { resolve, dirname } from "path";
import { fileURLToPath } from "url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(resolve(__dirname, "../index.css"), "utf8");

type Triple = [number, number, number];
type Tokens = Record<string, Triple>;

function parseBlock(selector: string): Tokens {
  const start = css.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`block ${selector} not found in index.css`);
  const end = css.indexOf("\n}", start);
  const body = css.slice(start, end);
  const tokens: Tokens = {};
  for (const m of body.matchAll(/--([\w-]+):\s*oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)/g)) {
    tokens[m[1]!] = [Number(m[2]), Number(m[3]), Number(m[4])];
  }
  return tokens;
}

/** OKLCH -> linear sRGB (Ottosson), gamut-clamped, then WCAG relative luminance. */
function luminance([L, C, H]: [number, number, number]): number {
  const h = (H * Math.PI) / 180;
  const a = C * Math.cos(h);
  const b = C * Math.sin(h);
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (L - 0.0894841775 * a - 1.2914855480 * b) ** 3;
  const clamp = (v: number) => Math.min(1, Math.max(0, v));
  const r = clamp(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s);
  const g = clamp(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s);
  const bl = clamp(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s);
  return 0.2126 * r + 0.7152 * g + 0.0722 * bl;
}

function ratio(a: [number, number, number], b: [number, number, number]): number {
  const x = luminance(a);
  const y = luminance(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

const TEXT_PAIRS: Array<[fg: string, bg: string]> = [
  ["foreground", "background"],
  ["card-foreground", "card"],
  ["popover-foreground", "popover"],
  ["muted-foreground", "background"],
  ["muted-foreground", "card"],
  ["muted-foreground", "muted"],
  ["secondary-foreground", "secondary"],
  ["accent-foreground", "accent"],
  ["primary-foreground", "primary"],
  ["destructive-foreground", "destructive"],
  ["success-foreground", "success"],
  ["warning-foreground", "warning"],
  ["sidebar-foreground", "sidebar"],
  ["sidebar-primary-foreground", "sidebar-primary"],
  ["sidebar-accent-foreground", "sidebar-accent"],
  // accent-coloured text (links, destructive messages) on the page surfaces
  ["primary", "background"],
  ["primary", "card"],
  ["destructive", "background"],
  ["destructive", "card"],
  ["success-text", "background"],
  ["success-text", "card"],
  ["warning-text", "background"],
  ["warning-text", "card"],
  ["info-text", "background"],
  ["info-text", "card"],
  ["destructive-text", "background"],
  ["destructive-text", "card"],
];

const CONTROL_PAIRS: Array<[string, string]> = [
  ["control-border", "background"],
  ["control-border", "card"],
];

// Data marks (chart slices, bars and lines) sit on the card surface and must
// stay distinguishable from it: >= 3:1 (WCAG 2.2 SC 1.4.11 non-text contrast).
const CHART_TOKENS = [
  "chart-1", "chart-2", "chart-3", "chart-4",
  "chart-5", "chart-6", "chart-7", "chart-8",
];

for (const [name, selector] of [
  ["light", ":root"],
  ["dark", ".dark"],
] as const) {
  describe(`theme contrast (${name})`, () => {
    // Tokens a theme does not override fall back to :root, mirroring the cascade.
    const tokens = { ...parseBlock(":root"), ...parseBlock(selector) };
    const token = (key: string): Triple => {
      const value = tokens[key];
      if (!value) throw new Error(`--${key} missing or not oklch() in ${selector}`);
      return value;
    };

    it.each(TEXT_PAIRS)("%s on %s >= 4.5:1", (fg, bg) => {
      expect(ratio(token(fg), token(bg))).toBeGreaterThanOrEqual(4.5);
    });

    it.each(CONTROL_PAIRS)("%s against %s >= 3:1", (fg, bg) => {
      expect(ratio(token(fg), token(bg))).toBeGreaterThanOrEqual(3);
    });

    it.each(CHART_TOKENS)("%s data mark against card >= 3:1", (fg) => {
      expect(ratio(token(fg), token("card"))).toBeGreaterThanOrEqual(3);
    });
  });
}
