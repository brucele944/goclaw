import { beforeAll, describe, expect, it } from "vitest";
import { resolveGraphNodeDisplayColor } from "./graph-node-colors";

// jsdom does not load index.css, so provide the graph tokens the resolver reads.
beforeAll(() => {
  const root = document.documentElement;
  root.style.setProperty("--graph-light-blue", "#2563eb");
  root.style.setProperty("--graph-light-orange", "#ea580c");
  root.style.setProperty("--graph-light-cyan", "#0891b2");
  root.style.setProperty("--graph-light-slate", "#475569");
  root.style.setProperty("--graph-dark-blue", "#60a5fa");
  root.style.setProperty("--graph-dark-orange", "#fb923c");
  root.style.setProperty("--graph-dark-cyan", "#22d3ee");
  root.style.setProperty("--graph-dark-slate", "#94a3b8");
});

describe("resolveGraphNodeDisplayColor", () => {
  it("resolves kg entity nodes from the graph palette", () => {
    expect(resolveGraphNodeDisplayColor({
      entityType: "person",
      color: "#E85D24",
    }, false)).toBe("#ea580c");
  });

  it("resolves vault document nodes from the theme palette", () => {
    expect(resolveGraphNodeDisplayColor({
      docType: "document",
      entityType: "document",
      color: "#8b5cf6",
    }, false)).toBe("#0891b2");
  });

  it("uses the dark palette for dark theme", () => {
    expect(resolveGraphNodeDisplayColor({ docType: "document" }, true)).toBe("#22d3ee");
  });

  it("falls back to the default palette colour for unknown attrs", () => {
    expect(resolveGraphNodeDisplayColor({}, false)).toBe("#475569");
  });
});
