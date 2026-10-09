import { useId } from "react";
import { useTranslation } from "react-i18next";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { SandboxConfig } from "@/types/agent";
import { ConfigSection, InfoLabel, numOrUndef } from "./config-section";

interface SandboxSectionProps {
  enabled: boolean;
  value: SandboxConfig;
  onToggle: (v: boolean) => void;
  onChange: (v: SandboxConfig) => void;
}

export function SandboxSection({ enabled, value, onToggle, onChange }: SandboxSectionProps) {
  const { t } = useTranslation("agents");
  const uid = useId();
  const s = "configSections.sandbox";
  return (
    <ConfigSection
      title={t(`${s}.title`)}
      description={t(`${s}.description`)}
      enabled={enabled}
      onToggle={onToggle}
    >
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <InfoLabel htmlFor={`${uid}-mode`} tip="'off' disables sandboxing, 'non-main' sandboxes only sub-agents, 'all' sandboxes every execution including the main agent.">{t(`${s}.mode`)}</InfoLabel>
          <Select
            value={value.mode ?? ""}
            onValueChange={(v) => onChange({ ...value, mode: v as SandboxConfig["mode"] })}
          >
            <SelectTrigger id={`${uid}-mode`}><SelectValue placeholder="off" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="off">off</SelectItem>
              <SelectItem value="non-main">non-main</SelectItem>
              <SelectItem value="all">all</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <InfoLabel htmlFor={`${uid}-workspaceAccess`} tip="How the sandbox accesses the host workspace. 'none' = isolated, 'ro' = read-only mount, 'rw' = full read-write access.">{t(`${s}.workspaceAccess`)}</InfoLabel>
          <Select
            value={value.workspace_access ?? ""}
            onValueChange={(v) =>
              onChange({ ...value, workspace_access: v as SandboxConfig["workspace_access"] })
            }
          >
            <SelectTrigger id={`${uid}-workspaceAccess`}><SelectValue placeholder="rw" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="none">none</SelectItem>
              <SelectItem value="ro">ro (read-only)</SelectItem>
              <SelectItem value="rw">rw (read-write)</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
      <div className="space-y-2">
        <InfoLabel htmlFor={`${uid}-image`} tip="Docker image used for the sandbox container. Must be pre-built and available locally.">{t(`${s}.image`)}</InfoLabel>
        <Input
          id={`${uid}-image`}
          placeholder="goclaw-sandbox:bookworm-slim"
          value={value.image ?? ""}
          onChange={(e) => onChange({ ...value, image: e.target.value || undefined })}
        />
      </div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <InfoLabel htmlFor={`${uid}-scope`} tip="Container lifecycle scope. 'session' = one container per chat session, 'agent' = shared across sessions, 'shared' = shared across all agents.">{t(`${s}.scope`)}</InfoLabel>
          <Select
            value={value.scope ?? ""}
            onValueChange={(v) => onChange({ ...value, scope: v as SandboxConfig["scope"] })}
          >
            <SelectTrigger id={`${uid}-scope`}><SelectValue placeholder="session" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="session">session</SelectItem>
              <SelectItem value="agent">agent</SelectItem>
              <SelectItem value="shared">shared</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <InfoLabel htmlFor={`${uid}-timeout`} tip="Maximum execution time in seconds for each command run inside the sandbox.">{t(`${s}.timeout`)}</InfoLabel>
          <Input
            id={`${uid}-timeout`}
            type="number"
            placeholder="300"
            value={value.timeout_sec ?? ""}
            onChange={(e) => onChange({ ...value, timeout_sec: numOrUndef(e.target.value) })}
          />
        </div>
      </div>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <InfoLabel htmlFor={`${uid}-memoryMb`} tip="Maximum memory allocation for the sandbox container in megabytes.">{t(`${s}.memoryMb`)}</InfoLabel>
          <Input
            id={`${uid}-memoryMb`}
            type="number"
            placeholder="512"
            value={value.memory_mb ?? ""}
            onChange={(e) => onChange({ ...value, memory_mb: numOrUndef(e.target.value) })}
          />
        </div>
        <div className="space-y-2">
          <InfoLabel htmlFor={`${uid}-cpus`} tip="CPU allocation for the sandbox container. Fractional values allowed (e.g. 0.5 = half a core).">{t(`${s}.cpus`)}</InfoLabel>
          <Input
            id={`${uid}-cpus`}
            type="number"
            step="0.5"
            placeholder="1.0"
            value={value.cpus ?? ""}
            onChange={(e) => onChange({ ...value, cpus: numOrUndef(e.target.value) })}
          />
        </div>
      </div>
      <div className="flex items-center gap-2">
        <Switch
          id={`${uid}-networkEnabled`}
          checked={value.network_enabled ?? false}
          onCheckedChange={(v) => onChange({ ...value, network_enabled: v })}
        />
        <InfoLabel htmlFor={`${uid}-networkEnabled`} tip="Allow the sandbox container to access the network. Disable for fully isolated execution.">{t(`${s}.networkEnabled`)}</InfoLabel>
      </div>
    </ConfigSection>
  );
}
