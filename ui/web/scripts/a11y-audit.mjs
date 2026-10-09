#!/usr/bin/env node
/**
 * Static accessibility audit for `ui/web/src`.
 *
 * eslint-plugin-jsx-a11y@6.x declares `eslint: ^3 || ... || ^9`, so it cannot be
 * installed against this package's ESLint 10. This script is the phase-2 fallback:
 * it uses the TypeScript compiler API (from `ui/web/node_modules/typescript`) to
 * find the same classes of issue that the sweeps in phases 3-4 fix.
 *
 * Checks:
 *   1. Icon-only `<Button>` with no accessible name (no text, no aria-label/title).
 *   2. `onClick` on a non-interactive element with no keyboard handler, role or tabIndex.
 *   3. `<Label>` / `InfoLabel` without `htmlFor` (control association).
 *
 * Exits 1 when any finding is present, so it can gate the later sweeps.
 */
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const ts = require("typescript");

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(scriptDir, "..");
const srcDir = path.join(webRoot, "src");

const SKIP_DIRS = new Set(["node_modules", "dist", "coverage"]);
const SKIP_FILE = /\.(test|spec)\.[cm]?tsx?$/;

// Intrinsic elements that are not interactive on their own.
const NON_INTERACTIVE_TAGS = {
  div: true,
  span: true,
  tr: true,
  td: true,
  th: true,
  li: true,
  ul: true,
  ol: true,
  dl: true,
  dt: true,
  dd: true,
  section: true,
  article: true,
  aside: true,
  header: true,
  footer: true,
  main: true,
  nav: true,
  p: true,
  h1: true,
  h2: true,
  h3: true,
  h4: true,
  h5: true,
  h6: true,
  table: true,
  tbody: true,
  thead: true,
  tfoot: true,
  figure: true,
  figcaption: true,
  img: true,
  canvas: true,
  svg: true,
  form: true,
};

const NAME_ATTRS = ["aria-label", "aria-labelledby", "title"];

function walk(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (SKIP_DIRS.has(entry.name)) continue;
      out.push(...walk(full));
    } else if (/\.tsx?$/.test(entry.name) && !SKIP_FILE.test(entry.name)) {
      out.push(full);
    }
  }
  return out;
}

function hasAttr(attributes, names) {
  return attributes.properties.some(
    (a) => ts.isJsxAttribute(a) && names.includes(a.name.getText()),
  );
}

/** Does any child render text (including an `sr-only` span) or declare its own name? */
function childrenHaveText(children) {
  for (const child of children) {
    if (ts.isJsxText(child)) {
      if (child.getText().trim() !== "") return true;
    } else if (ts.isJsxExpression(child)) {
      if (child.expression) return true; // dynamic content - assume it names the button
    } else if (ts.isJsxElement(child)) {
      if (hasAttr(child.openingElement.attributes, NAME_ATTRS)) return true;
      if (childrenHaveText(child.children)) return true;
    } else if (ts.isJsxSelfClosingElement(child)) {
      if (hasAttr(child.attributes, NAME_ATTRS)) return true;
    } else if (ts.isJsxFragment(child)) {
      if (childrenHaveText(child.children)) return true;
    }
  }
  return false;
}

function baseTagName(tagName) {
  const text = tagName.getText();
  const dot = text.lastIndexOf(".");
  return dot === -1 ? text : text.slice(dot + 1);
}

function main() {
  const files = walk(srcDir).sort();
  const findings = {
    iconButton: [],
    clickable: [],
    label: [],
  };

  for (const file of files) {
    const text = fs.readFileSync(file, "utf8");
    const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
    const rel = path.relative(webRoot, file).split(path.sep).join("/");

    const report = (bucket, node, message) => {
      const { line } = sf.getLineAndCharacterOfPosition(node.getStart(sf));
      const snippet = node.getText(sf).replace(/\s+/g, " ").slice(0, 140);
      findings[bucket].push({ file: rel, line: line + 1, message, snippet });
    };

    const visit = (node) => {
      if (ts.isJsxElement(node)) {
        const opening = node.openingElement;
        const tag = opening.tagName.getText(sf);
        const base = baseTagName(opening.tagName);
        const attrs = opening.attributes;

        if ((tag === "Button" || tag === "button") && !hasAttr(attrs, NAME_ATTRS) && !childrenHaveText(node.children)) {
          report("iconButton", node, "icon-only <Button> without accessible name");
        }
        if (NON_INTERACTIVE_TAGS[base] && hasAttr(attrs, ["onClick"])) {
          if (
            !hasAttr(attrs, ["onKeyDown", "onKeyUp", "onKeyPress"]) &&
            !hasAttr(attrs, ["role"]) &&
            !hasAttr(attrs, ["tabIndex"])
          ) {
            report("clickable", node, `<${base}> has onClick but no keyboard handler/role/tabIndex`);
          }
        }
        // `id` on a Label = group heading referenced by `aria-labelledby` (role="group" pattern).
        if ((tag === "Label" || tag === "InfoLabel") && !hasAttr(attrs, ["htmlFor", "id"])) {
          report("label", node, `<${tag}> without htmlFor (control association)`);
        }
      } else if (ts.isJsxSelfClosingElement(node)) {
        const tag = node.tagName.getText(sf);
        const base = baseTagName(node.tagName);
        const attrs = node.attributes;

        if ((tag === "Button" || tag === "button") && !hasAttr(attrs, NAME_ATTRS)) {
          report("iconButton", node, "icon-only <Button> without accessible name");
        }
        if (NON_INTERACTIVE_TAGS[base] && hasAttr(attrs, ["onClick"])) {
          if (
            !hasAttr(attrs, ["onKeyDown", "onKeyUp", "onKeyPress"]) &&
            !hasAttr(attrs, ["role"]) &&
            !hasAttr(attrs, ["tabIndex"])
          ) {
            report("clickable", node, `<${base}> has onClick but no keyboard handler/role/tabIndex`);
          }
        }
        if ((tag === "Label" || tag === "InfoLabel") && !hasAttr(attrs, ["htmlFor", "id"])) {
          report("label", node, `<${tag}> without htmlFor (control association)`);
        }
      }
      ts.forEachChild(node, visit);
    };

    visit(sf);
  }

  // Reviewed false positives live in a11y-audit-allow.json as per-file, per-rule counts:
  //   { "src/x.tsx": { "label": 1, "clickable": 0, "iconButton": 0 } }
  // A file may not exceed its allowance (new finding) nor fall below it (stale entry: delete it).
  const allowPath = path.join(scriptDir, "a11y-audit-allow.json");
  const allow = fs.existsSync(allowPath) ? JSON.parse(fs.readFileSync(allowPath, "utf8")) : {};
  const RULES = [
    ["iconButton", "icon-only <Button> without accessible name"],
    ["clickable", "onClick without keyboard handler (non-interactive element)"],
    ["label", "<Label>/<InfoLabel> without htmlFor"],
  ];

  console.log(`a11y audit - ${path.relative(webRoot, srcDir)} (${files.length} files)`);
  let failures = 0;
  let allowed = 0;
  for (const [rule, title] of RULES) {
    const byFile = new Map();
    for (const f of findings[rule]) {
      if (!byFile.has(f.file)) byFile.set(f.file, []);
      byFile.get(f.file).push(f);
    }
    const lines = [];
    for (const [file, list] of byFile) {
      const budget = allow[file]?.[rule] ?? 0;
      allowed += Math.min(budget, list.length);
      if (list.length > budget) {
        failures += list.length - budget;
        for (const f of list) lines.push(`  ${f.file}:${f.line}  ${f.snippet}`);
      }
    }
    for (const [file, rules] of Object.entries(allow)) {
      const budget = rules[rule] ?? 0;
      const actual = byFile.get(file)?.length ?? 0;
      if (budget > actual) {
        failures += budget - actual;
        lines.push(`  ${file}: allowlist expects ${budget} ${rule} finding(s) but found ${actual} - update a11y-audit-allow.json`);
      }
    }
    console.log(`\n${title}: ${lines.length === 0 ? "ok" : lines.length}`);
    for (const l of lines) console.log(l);
  }

  console.log(`\nUnreviewed findings: ${failures} (reviewed false positives allowed: ${allowed})`);
  if (failures > 0) {
    console.log("FAIL: accessibility findings present.");
    process.exit(1);
  }
  console.log("PASS: no unreviewed accessibility findings.");
}

main();
