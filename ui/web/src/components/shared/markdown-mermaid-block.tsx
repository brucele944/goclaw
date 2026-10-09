/**
 * MermaidBlock — lazy-loads mermaid and renders diagrams as SVG.
 * Re-renders on theme change. Shows skeleton while loading, error fallback on failure.
 */
import { useEffect, useState, useId } from "react";
import { useUiStore } from "@/stores/use-ui-store";

interface MermaidBlockProps {
  code: string;
}

function isDarkMode(theme: string): boolean {
  if (theme === "dark") return true;
  if (theme === "light") return false;
  // "system" — detect from DOM
  return document.documentElement.classList.contains("dark");
}

export function MermaidBlock({ code }: MermaidBlockProps) {
  const uid = useId().replace(/:/g, "");
  const diagramId = `mermaid-${uid}`;
  const { theme } = useUiStore();
  const [svg, setSvg] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    setSvg(null);
    setError(null);
    setLoading(true);

    let cancelled = false;

    (async () => {
      try {
        const mermaid = (await import("mermaid")).default;
        mermaid.initialize({
          startOnLoad: false,
          theme: isDarkMode(theme) ? "dark" : "default",
          securityLevel: "strict",
        });

        const { svg: renderedSvg } = await mermaid.render(diagramId, code);
        if (!cancelled) {
          setSvg(renderedSvg);
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [code, theme, diagramId]);

  if (loading) {
    return (
      <div className="not-prose my-3 overflow-x-auto rounded-lg border border-border/60">
        <div className="animate-pulse bg-muted/50 h-32 w-full rounded-lg" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="not-prose my-3 rounded-lg border dark:border-red-700 bg-destructive-subtle p-3">
        <p className="mb-2 text-xs font-semibold text-destructive-text">
          Mermaid render error
        </p>
        <pre className="overflow-x-auto text-xs text-destructive-text whitespace-pre-wrap">
          {error}
        </pre>
        <hr className="my-2 border-destructive/25" />
        <pre className="overflow-x-auto text-xs text-muted-foreground whitespace-pre">{code}</pre>
      </div>
    );
  }

  return (
    <div
      className="not-prose my-3 overflow-x-auto rounded-lg border border-border/60 bg-background p-4"
      dangerouslySetInnerHTML={{ __html: svg ?? "" }}
    />
  );
}
