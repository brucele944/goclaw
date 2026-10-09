import Graph from "graphology";
import type { VaultDocument, VaultLink } from "@/types/vault";
import type { VaultGraphNode, VaultGraphEdge } from "@/types/graph-dto";
import { getNodeSize, truncateMiddle } from "@/components/graph/graph-utils";
import { graphColor, type GraphColorName } from "@/components/graph/graph-color-tokens";

// Node colours per vault document type — matches DOC_TYPE_ICONS in vault-tree.tsx.
// Names map onto the shared graph palette tokens in index.css; the concrete hex is
// resolved per theme because sigma's canvas cannot read CSS variables.
export const VAULT_TYPE_COLOR_NAMES: Record<string, GraphColorName> = {
  context: "blue",
  memory: "purple",
  note: "amber",
  skill: "emerald",
  episodic: "orange",
  media: "rose",
  document: "cyan",
};
const DEFAULT_COLOR_NAME: GraphColorName = "slate";

/** Resolved type → colour map for the active theme (legend dots, filters). */
export function vaultTypeColors(isDark: boolean): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [type, name] of Object.entries(VAULT_TYPE_COLOR_NAMES)) {
    out[type] = graphColor(name, isDark);
  }
  return out;
}

/** Get node color based on doc type and theme */
export function getVaultNodeColor(docType: string, isDark: boolean): string {
  return graphColor(VAULT_TYPE_COLOR_NAMES[docType] ?? DEFAULT_COLOR_NAME, isDark);
}

/** Limit documents by degree centrality (highest-connected first). */
export function limitVaultDocsByDegree(
  docs: VaultDocument[],
  links: VaultLink[],
  nodeLimit: number,
): VaultDocument[] {
  if (docs.length <= nodeLimit) return docs;
  const ids = new Set(docs.map((d) => d.id));
  const deg = new Map<string, number>();
  for (const l of links) {
    if (ids.has(l.from_doc_id)) deg.set(l.from_doc_id, (deg.get(l.from_doc_id) ?? 0) + 1);
    if (ids.has(l.to_doc_id)) deg.set(l.to_doc_id, (deg.get(l.to_doc_id) ?? 0) + 1);
  }
  return [...docs].sort((a, b) => (deg.get(b.id) ?? 0) - (deg.get(a.id) ?? 0)).slice(0, nodeLimit);
}

/** Build graph from lightweight DTOs (degree pre-computed, no client loop). */
export function buildVaultGraphFromDTO(nodes: VaultGraphNode[], edges: VaultGraphEdge[], isDark: boolean): Graph {
  const graph = new Graph({ multi: false, type: "directed" });
  const nodeIds = new Set(nodes.map((n) => n.id));

  for (const n of nodes) {
    graph.addNode(n.id, {
      label: truncateMiddle(n.t || n.p.split("/").pop() || n.id.slice(0, 8), 28),
      x: 0, y: 0,
      size: getNodeSize(n.deg, nodes.length),
      color: getVaultNodeColor(n.dt, isDark),
      docType: n.dt,
    });
  }

  for (const e of edges) {
    if (nodeIds.has(e.from) && nodeIds.has(e.to) && !graph.hasEdge(e.from, e.to)) {
      graph.addEdgeWithKey(e.id, e.from, e.to, {
        label: e.type, type: "curvedArrow",
        color: graphColor("slate", isDark), size: 0.4,
      });
    }
  }

  return graph;
}

/** Build a Graphology graph from vault documents and their links. */
export function buildVaultGraph(
  documents: VaultDocument[],
  links: VaultLink[],
  isDark: boolean,
): Graph {
  const graph = new Graph({ multi: false, type: "directed" });
  const docIds = new Set(documents.map((d) => d.id));

  // Pre-compute degree map
  const degreeMap = new Map<string, number>();
  for (const link of links) {
    if (docIds.has(link.from_doc_id))
      degreeMap.set(link.from_doc_id, (degreeMap.get(link.from_doc_id) ?? 0) + 1);
    if (docIds.has(link.to_doc_id))
      degreeMap.set(link.to_doc_id, (degreeMap.get(link.to_doc_id) ?? 0) + 1);
  }

  // Add nodes (x/y assigned by container via circular layout before FA2)
  for (const doc of documents) {
    const degree = degreeMap.get(doc.id) ?? 0;
    const rawLabel = doc.title || doc.path.split("/").pop() || doc.id.slice(0, 8);
    graph.addNode(doc.id, {
      label: truncateMiddle(rawLabel, 28),
      x: 0,
      y: 0,
      size: getNodeSize(degree, documents.length),
      // Color set by nodeReducer based on theme; resolved here as a fallback
      color: getVaultNodeColor(doc.doc_type, isDark),
      docType: doc.doc_type,
    });
  }

  // Add edges (only where both endpoints exist)
  for (const link of links) {
    if (docIds.has(link.from_doc_id) && docIds.has(link.to_doc_id)) {
      // Avoid duplicate edges for same source→target
      if (!graph.hasEdge(link.from_doc_id, link.to_doc_id)) {
        graph.addEdgeWithKey(link.id, link.from_doc_id, link.to_doc_id, {
          label: link.link_type,
          type: "curvedArrow",
          color: graphColor("slate", isDark),
          size: 0.4,
        });
      }
    }
  }

  return graph;
}
