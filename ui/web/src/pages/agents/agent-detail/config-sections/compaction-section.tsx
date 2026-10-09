import { useId } from "react";
import { useTranslation } from "react-i18next";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import type { CompactionConfig } from "@/types/agent";
import { InfoLabel, numOrUndef } from "./config-section";

interface CompactionSectionProps {
  value: CompactionConfig;
  onChange: (v: CompactionConfig) => void;
}

export function CompactionSection({ value, onChange }: CompactionSectionProps) {
  const { t } = useTranslation("agents");
  const uid = useId();
  const s = "configSections.compaction";
  return (
    <section className="space-y-3">
      <div>
        <h3 className="text-sm font-medium">{t(`${s}.title`)}</h3>
        <p className="text-xs text-muted-foreground">{t(`${s}.description`)}</p>
      </div>
      <div className="rounded-lg border p-3 space-y-4 sm:p-4">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <InfoLabel htmlFor={`${uid}-maxRequestShare`} tip={t(`${s}.maxRequestShareTip`)}>{t(`${s}.maxRequestShare`)}</InfoLabel>
          <Input
            id={`${uid}-maxRequestShare`}
            type="number"
            step="0.05"
            placeholder="0.85"
            value={value.maxRequestShare ?? ""}
            onChange={(e) => onChange({ ...value, maxRequestShare: numOrUndef(e.target.value) })}
          />
        </div>
        <div className="space-y-2">
          <InfoLabel htmlFor={`${uid}-maxHistoryShare`} tip={t(`${s}.maxHistoryShareTip`)}>{t(`${s}.maxHistoryShare`)}</InfoLabel>
          <Input
            id={`${uid}-maxHistoryShare`}
            type="number"
            step="0.05"
            placeholder="0.85"
            value={value.maxHistoryShare ?? ""}
            onChange={(e) => onChange({ ...value, maxHistoryShare: numOrUndef(e.target.value) })}
          />
        </div>
        <div className="space-y-2">
          <InfoLabel htmlFor={`${uid}-keepLastMessages`} tip={t(`${s}.keepLastMessagesTip`)}>{t(`${s}.keepLastMessages`)}</InfoLabel>
          <Input
            id={`${uid}-keepLastMessages`}
            type="number"
            placeholder="4"
            value={value.keepLastMessages ?? ""}
            onChange={(e) => onChange({ ...value, keepLastMessages: numOrUndef(e.target.value) })}
          />
        </div>
        <div className="space-y-2">
          <InfoLabel htmlFor={`${uid}-timeoutSeconds`} tip={t(`${s}.timeoutSecondsTip`)}>{t(`${s}.timeoutSeconds`)}</InfoLabel>
          <Input
            id={`${uid}-timeoutSeconds`}
            type="number"
            placeholder="120"
            value={value.timeoutSeconds ?? ""}
            onChange={(e) => onChange({ ...value, timeoutSeconds: numOrUndef(e.target.value) })}
          />
        </div>
      </div>
      <div className="flex items-center gap-2">
        <Switch
          id={`${uid}-memoryFlush`}
          checked={value.memoryFlush?.enabled ?? true}
          onCheckedChange={(v) =>
            onChange({ ...value, memoryFlush: { ...value.memoryFlush, enabled: v } })
          }
        />
        <InfoLabel htmlFor={`${uid}-memoryFlush`} tip={t(`${s}.memoryFlushTip`)}>{t(`${s}.memoryFlush`)}</InfoLabel>
      </div>
      </div>
    </section>
  );
}
