import { useId } from "react";
import { useTranslation } from "react-i18next";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import type { MemoryConfig, DreamingConfig } from "@/types/agent";
import { InfoLabel, numOrUndef } from "./config-section";

interface MemorySectionProps {
  value: MemoryConfig;
  onChange: (v: MemoryConfig) => void;
}

export function MemorySection({ value, onChange }: MemorySectionProps) {
  const { t } = useTranslation("agents");
  const uid = useId();
  const s = "configSections.memory";
  const ds = "configSections.dreaming";
  const dreaming: DreamingConfig = value.dreaming ?? {};
  const setDreaming = (patch: Partial<DreamingConfig>) =>
    onChange({ ...value, dreaming: { ...dreaming, ...patch } });
  return (
    <section className="space-y-3">
      <div>
        <h3 className="text-sm font-medium">{t(`${s}.title`)}</h3>
        <p className="text-xs text-muted-foreground">{t(`${s}.description`)}</p>
      </div>
      <div className="rounded-lg border p-3 space-y-4 sm:p-4">
        <div className="flex items-center gap-2">
          <Switch
            id={`${uid}-enabled`}
            checked={value.enabled ?? true}
            onCheckedChange={(v) => onChange({ ...value, enabled: v })}
          />
          <InfoLabel htmlFor={`${uid}-enabled`} tip={t(`${s}.enabledTip`)}>{t(`${s}.enabled`)}</InfoLabel>
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <InfoLabel htmlFor={`${uid}-maxChunkLen`} tip={t(`${s}.maxChunkLenTip`)}>{t(`${s}.maxChunkLen`)}</InfoLabel>
            <Input
              id={`${uid}-maxChunkLen`}
              type="number"
              placeholder="1000"
              value={value.max_chunk_len ?? ""}
              onChange={(e) => onChange({ ...value, max_chunk_len: numOrUndef(e.target.value) })}
              className="text-base md:text-sm"
            />
          </div>
          <div className="space-y-2">
            <InfoLabel htmlFor={`${uid}-chunkOverlap`} tip={t(`${s}.chunkOverlapTip`)}>{t(`${s}.chunkOverlap`)}</InfoLabel>
            <Input
              id={`${uid}-chunkOverlap`}
              type="number"
              placeholder="200"
              value={value.chunk_overlap ?? ""}
              onChange={(e) => onChange({ ...value, chunk_overlap: numOrUndef(e.target.value) })}
              className="text-base md:text-sm"
            />
          </div>
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <InfoLabel htmlFor={`${uid}-maxResults`} tip={t(`${s}.maxResultsTip`)}>{t(`${s}.maxResults`)}</InfoLabel>
            <Input
              id={`${uid}-maxResults`}
              type="number"
              placeholder="6"
              value={value.max_results ?? ""}
              onChange={(e) => onChange({ ...value, max_results: numOrUndef(e.target.value) })}
              className="text-base md:text-sm"
            />
          </div>
          <div className="space-y-2">
            <InfoLabel htmlFor={`${uid}-minScore`} tip={t(`${s}.minScoreTip`)}>{t(`${s}.minScore`)}</InfoLabel>
            <Input
              id={`${uid}-minScore`}
              type="number"
              step="0.01"
              placeholder="0.35"
              value={value.min_score ?? ""}
              onChange={(e) => onChange({ ...value, min_score: numOrUndef(e.target.value) })}
              className="text-base md:text-sm"
            />
          </div>
        </div>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <InfoLabel htmlFor={`${uid}-vectorWeight`} tip={t(`${s}.vectorWeightTip`)}>{t(`${s}.vectorWeight`)}</InfoLabel>
            <Input
              id={`${uid}-vectorWeight`}
              type="number"
              step="0.1"
              placeholder="0.7"
              value={value.vector_weight ?? ""}
              onChange={(e) => onChange({ ...value, vector_weight: numOrUndef(e.target.value) })}
              className="text-base md:text-sm"
            />
          </div>
          <div className="space-y-2">
            <InfoLabel htmlFor={`${uid}-textWeight`} tip={t(`${s}.textWeightTip`)}>{t(`${s}.textWeight`)}</InfoLabel>
            <Input
              id={`${uid}-textWeight`}
              type="number"
              step="0.1"
              placeholder="0.3"
              value={value.text_weight ?? ""}
              onChange={(e) => onChange({ ...value, text_weight: numOrUndef(e.target.value) })}
              className="text-base md:text-sm"
            />
          </div>
        </div>
        {/* Phase 8 — Dreaming worker per-agent overrides.
            Nested inside memory since dreaming is memory consolidation.
            All fields optional; nil = use backend defaults. */}
        <div className="rounded-md border border-dashed p-3 space-y-4">
          <div>
            <h4 className="text-sm font-medium">{t(`${ds}.title`)}</h4>
            <p className="text-xs text-muted-foreground">{t(`${ds}.description`)}</p>
          </div>
          <div className="flex items-center gap-2">
            <Switch
              id={`${uid}-dreamingEnabled`}
              checked={dreaming.enabled ?? true}
              onCheckedChange={(v) => setDreaming({ enabled: v })}
            />
            <InfoLabel htmlFor={`${uid}-dreamingEnabled`} tip={t(`${ds}.enabledTip`)}>{t(`${ds}.enabled`)}</InfoLabel>
          </div>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <InfoLabel htmlFor={`${uid}-dreamingThreshold`} tip={t(`${ds}.thresholdTip`)}>{t(`${ds}.threshold`)}</InfoLabel>
              <Input
                id={`${uid}-dreamingThreshold`}
                type="number"
                placeholder="5"
                value={dreaming.threshold ?? ""}
                onChange={(e) => setDreaming({ threshold: numOrUndef(e.target.value) })}
                className="text-base md:text-sm"
              />
            </div>
            <div className="space-y-2">
              <InfoLabel htmlFor={`${uid}-dreamingDebounceMs`} tip={t(`${ds}.debounceMsTip`)}>{t(`${ds}.debounceMs`)}</InfoLabel>
              <Input
                id={`${uid}-dreamingDebounceMs`}
                type="number"
                placeholder="600000"
                value={dreaming.debounce_ms ?? ""}
                onChange={(e) => setDreaming({ debounce_ms: numOrUndef(e.target.value) })}
                className="text-base md:text-sm"
              />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Switch
              id={`${uid}-dreamingVerboseLog`}
              checked={dreaming.verbose_log ?? false}
              onCheckedChange={(v) => setDreaming({ verbose_log: v })}
            />
            <InfoLabel htmlFor={`${uid}-dreamingVerboseLog`} tip={t(`${ds}.verboseLogTip`)}>{t(`${ds}.verboseLog`)}</InfoLabel>
          </div>
        </div>
      </div>
    </section>
  );
}
