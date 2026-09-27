import { useTranslation } from "react-i18next";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { GripVertical, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Combobox } from "@/components/ui/combobox";
import { ProviderCatalogueStatus } from "@/components/shared/model-catalogue-status";
import { useProviderCapability } from "@/pages/providers/hooks/use-provider-capabilities";
import { buildCapabilityModelOptions } from "@/types/provider";
import type { ModelFallbackCandidate } from "@/types/agent";
import type { ProviderData } from "@/types/provider";

interface SortableFallbackRowProps {
  id: string;
  candidate: ModelFallbackCandidate;
  providers: ProviderData[];
  onChange: (candidate: ModelFallbackCandidate) => void;
  onRemove: () => void;
}

function providerLabel(provider: ProviderData): string {
  return provider.display_name || provider.name;
}

export function SortableFallbackRow({
  id,
  candidate,
  providers,
  onChange,
  onRemove,
}: SortableFallbackRowProps) {
  const { t } = useTranslation("agents");
  const providerName = candidate.provider ?? "";
  const selectedProvider = providers.find((provider) => provider.name === providerName);
  // Model identity comes from the capability catalogue; the fallback candidate
  // itself stores the bare model id the transport sends upstream.
  const { models } = useProviderCapability(providerName);
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id });

  const modelOptions = buildCapabilityModelOptions(providerName, models);

  return (
    <div
      ref={setNodeRef}
      style={{
        transform: CSS.Transform.toString(transform),
        transition,
      }}
      className={`space-y-1 rounded-md border bg-background p-2 ${isDragging ? "shadow-md" : ""}`}
    >
      <div className="grid gap-2 sm:grid-cols-[auto_minmax(0,1fr)_minmax(0,1fr)_auto]">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="h-9 w-9 cursor-grab text-muted-foreground active:cursor-grabbing"
          aria-label={t("configSections.modelFallback.reorder")}
          {...attributes}
          {...listeners}
        >
          <GripVertical className="h-4 w-4" />
        </Button>
        <Combobox
          value={providerName}
          onChange={(provider) => onChange({ provider, model: "" })}
          options={providers.map((provider) => ({
            value: provider.name,
            label: providerLabel(provider),
          }))}
          placeholder={t("configSections.modelFallback.providerPlaceholder")}
        />
        <Combobox
          value={candidate.model ?? ""}
          onChange={(model) => onChange({ ...candidate, model })}
          options={modelOptions}
          placeholder={t("configSections.modelFallback.modelPlaceholder")}
          allowCustom
        />
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="h-9 w-9 text-muted-foreground hover:text-destructive"
          aria-label={t("configSections.modelFallback.remove")}
          onClick={onRemove}
        >
          <Trash2 className="h-4 w-4" />
        </Button>
      </div>
      {providerName && <ProviderCatalogueStatus provider={providerName} providerId={selectedProvider?.id} />}
    </div>
  );
}
