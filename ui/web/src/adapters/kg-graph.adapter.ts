import Graph from "graphology";
import type { KGEntity, KGRelation } from "@/types/knowledge-graph";
import type { KGGraphNode, KGGraphEdge } from "@/types/graph-dto";
import { getNodeSize, truncateMiddle } from "@/components/graph/graph-utils";
import { graphColor, type GraphColorName } from "@/components/graph/graph-color-tokens";

// Entity type → shared graph palette token (resolved per theme for the canvas).
export const KG_TYPE_COLOR_NAMES: Record<string, GraphColorName> = {
  person: "orange", organization: "red", project: "green",
  product: "teal", technology: "blue", task: "amber",
  event: "pink", document: "violet", concept: "purple", location: "cyan",
};
const KG_DEFAULT_COLOR_NAME: GraphColorName = "slate";

/** Resolved entity type → colour map for the active theme (legend, filters). */
export function kgTypeColors(isDark: boolean): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [type, name] of Object.entries(KG_TYPE_COLOR_NAMES)) {
    out[type] = graphColor(name, isDark);
  }
  return out;
}

/** Concrete node colour for an entity type, resolved for the active theme. */
export function getKgNodeColor(entityType: string, isDark: boolean): string {
  return graphColor(KG_TYPE_COLOR_NAMES[entityType] ?? KG_DEFAULT_COLOR_NAME, isDark);
}

/** Compute degree (edge count) for each entity id. */
export function computeDegreeMap(entities: KGEntity[], relations: KGRelation[]): Map<string, number> {
  const deg = new Map<string, number>();
  const ids = new Set(entities.map((e) => e.id));
  for (const r of relations) {
    if (ids.has(r.source_entity_id)) deg.set(r.source_entity_id, (deg.get(r.source_entity_id) ?? 0) + 1);
    if (ids.has(r.target_entity_id)) deg.set(r.target_entity_id, (deg.get(r.target_entity_id) ?? 0) + 1);
  }
  return deg;
}

/** Build KG graph from compact DTOs. */
export function buildKGGraphFromDTO(nodes: KGGraphNode[], edges: KGGraphEdge[], isDark: boolean): Graph {
  const graph = new Graph({ multi: false, type: "directed" });
  const nodeIds = new Set(nodes.map((n) => n.id));

  // Pre-compute degree from edges (compact DTO doesn't include it)
  const deg = new Map<string, number>();
  for (const e of edges) {
    if (nodeIds.has(e.src)) deg.set(e.src, (deg.get(e.src) ?? 0) + 1);
    if (nodeIds.has(e.tgt)) deg.set(e.tgt, (deg.get(e.tgt) ?? 0) + 1);
  }

  for (const n of nodes) {
    graph.addNode(n.id, {
      label: truncateMiddle(n.n, 28),
      x: 0, y: 0,
      size: getNodeSize(deg.get(n.id) ?? 0, nodes.length),
      color: getKgNodeColor(n.t, isDark),
      entityType: n.t,
    });
  }

  for (const e of edges) {
    if (nodeIds.has(e.src) && nodeIds.has(e.tgt) && !graph.hasEdge(e.src, e.tgt)) {
      graph.addEdgeWithKey(e.id, e.src, e.tgt, {
        label: e.type.replace(/_/g, " "), type: "curvedArrow",
      });
    }
  }

  return graph;
}

/** Build a Graphology graph from KG entities and relations. */
export function buildKGGraph(entities: KGEntity[], allRelations: KGRelation[], isDark: boolean): Graph {
  const graph = new Graph({ multi: false, type: "directed" });
  const entityIds = new Set(entities.map((e) => e.id));
  const degreeMap = computeDegreeMap(entities, allRelations);

  // Add nodes (x/y assigned by container via circular layout before FA2)
  for (const e of entities) {
    if (!graph.hasNode(e.id)) {
      const degree = degreeMap.get(e.id) ?? 0;
      graph.addNode(e.id, {
        label: truncateMiddle(e.name, 28),
        x: 0,
        y: 0,
        size: getNodeSize(degree, entities.length),
        color: getKgNodeColor(e.entity_type, isDark),
        entityType: e.entity_type,
      });
    }
  }

  // Add edges (straight arrows for KG)
  for (const r of allRelations) {
    if (entityIds.has(r.source_entity_id) && entityIds.has(r.target_entity_id)) {
      if (!graph.hasEdge(r.source_entity_id, r.target_entity_id)) {
        graph.addEdgeWithKey(r.id, r.source_entity_id, r.target_entity_id, {
          label: r.relation_type.replace(/_/g, " "),
          type: "curvedArrow",
        });
      }
    }
  }

  return graph;
}

/** Limit entities to nodeLimit by degree centrality (highest-degree first). */
export function limitEntitiesByDegree(
  allEntities: KGEntity[],
  allRelations: KGRelation[],
  nodeLimit: number,
): KGEntity[] {
  if (allEntities.length <= nodeLimit) return allEntities;
  const deg = computeDegreeMap(allEntities, allRelations);
  return [...allEntities].sort((a, b) => (deg.get(b.id) ?? 0) - (deg.get(a.id) ?? 0)).slice(0, nodeLimit);
}
