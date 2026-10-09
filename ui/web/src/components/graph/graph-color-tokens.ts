/**
 * Canvas graph colour resolution.
 *
 * The graph node palettes are declared as CSS custom properties in `index.css`
 * (`--graph-light-*` / `--graph-dark-*`). Sigma renders to a `<canvas>` and its
 * colour parser understands only hex and `rgb()`, so it cannot consume `var()`
 * strings the way the SVG/Recharts layers can. These helpers read the tokens
 * with `getComputedStyle`, selecting the variant for the active theme.
 *
 * Both theme variants are declared outside `.dark` on purpose: the document
 * class is applied in an effect that can run after a child effect, so reading
 * theme-scoped tokens could observe the previous theme.
 */

export const GRAPH_COLOR_NAMES = [
  "blue", "purple", "amber", "emerald", "orange", "rose", "cyan",
  "red", "green", "violet", "pink", "teal", "slate",
] as const;

export type GraphColorName = (typeof GRAPH_COLOR_NAMES)[number];

/** Non-node canvas chrome (labels, edges) that also needs concrete colours. */
export const GRAPH_CHROME_NAMES = ["label", "edge", "edge-highlight"] as const;

export type GraphChromeName = (typeof GRAPH_CHROME_NAMES)[number];

type Palette<T extends string> = Record<T, string>;

const palettes: Partial<Record<"light" | "dark", Palette<GraphColorName>>> = {};
const chromePalettes: Partial<Record<"light" | "dark", Palette<GraphChromeName>>> = {};

function readPalette<T extends string>(mode: "light" | "dark", names: readonly T[]): Palette<T> {
  const out = {} as Palette<T>;
  if (typeof document === "undefined") {
    for (const name of names) out[name] = "";
    return out;
  }
  const style = getComputedStyle(document.documentElement);
  for (const name of names) {
    out[name] = style.getPropertyValue(`--graph-${mode}-${name}`).trim();
  }
  return out;
}

/** Resolve a named graph node colour to a concrete value sigma can parse. */
export function graphColor(name: GraphColorName, isDark: boolean): string {
  const mode = isDark ? "dark" : "light";
  let palette = palettes[mode];
  if (!palette) {
    palette = readPalette(mode, GRAPH_COLOR_NAMES);
    // Only memoise once the stylesheet is available; otherwise keep re-reading.
    if (palette.blue) palettes[mode] = palette;
  }
  return palette[name];
}

/** Resolve a graph chrome colour (labels, edges) for the active theme. */
export function graphChromeColor(name: GraphChromeName, isDark: boolean): string {
  const mode = isDark ? "dark" : "light";
  let palette = chromePalettes[mode];
  if (!palette) {
    palette = readPalette(mode, GRAPH_CHROME_NAMES);
    if (palette.label) chromePalettes[mode] = palette;
  }
  return palette[name];
}
