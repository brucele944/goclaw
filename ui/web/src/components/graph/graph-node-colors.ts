import { getVaultNodeColor } from "@/adapters/vault-graph-adapter";
import { getKgNodeColor } from "@/adapters/kg-graph.adapter";

export function resolveGraphNodeDisplayColor(attrs: Record<string, unknown>, isDark: boolean): string {
  const docType = typeof attrs.docType === "string" ? attrs.docType : "";
  if (docType) {
    return getVaultNodeColor(docType, isDark);
  }
  const entityType = typeof attrs.entityType === "string" ? attrs.entityType : "";
  if (entityType) {
    return getKgNodeColor(entityType, isDark);
  }
  return typeof attrs.color === "string" && attrs.color
    ? attrs.color
    : getVaultNodeColor("other", isDark);
}
