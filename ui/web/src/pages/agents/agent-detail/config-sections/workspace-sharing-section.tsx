import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Shield, X, AlertTriangle, Plus, Brain } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { WorkspaceSharingConfig } from "@/types/agent";
import { UserPickerCombobox } from "@/components/shared/user-picker-combobox";
import { useContactResolver } from "@/hooks/use-contact-resolver";
import { formatUserLabel } from "@/lib/format-user-label";
import { InfoLabel } from "./config-section";

const MAX_SHARED_USERS = 100;

interface WorkspaceSharingSectionProps {
  value: WorkspaceSharingConfig;
  onChange: (v: WorkspaceSharingConfig) => void;
}

export function WorkspaceSharingSection({ value, onChange }: WorkspaceSharingSectionProps) {
  const { t } = useTranslation("agents");
  const uid = useId();
  const s = "configSections.workspaceSharing";
  const [contactSearch, setContactSearch] = useState("");

  const existingUsers = value.shared_users ?? [];
  const { resolve } = useContactResolver(existingUsers);

  const addUser = (userId: string) => {
    const trimmed = userId.trim();
    if (!trimmed) return;
    if (existingUsers.includes(trimmed)) return;
    if (existingUsers.length >= MAX_SHARED_USERS) return;
    onChange({ ...value, shared_users: [...existingUsers, trimmed] });
    setContactSearch("");
  };

  const removeUser = (idx: number) => {
    const updated = existingUsers.filter((_, i) => i !== idx);
    onChange({ ...value, shared_users: updated.length > 0 ? updated : undefined });
  };

  const isWorkspaceActive = !!value.shared_dm || !!value.shared_group || existingUsers.length > 0;

  return (
    <div className="space-y-5">
      {/* ── Section 1: Memory & Knowledge Graph ── */}
      <section className="space-y-3">
        <div className="flex items-center gap-2">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-warning-subtle">
            <Brain className="h-4 w-4 text-warning-text" />
          </div>
          <div>
            <h3 className="text-sm font-semibold">{t(`${s}.memoryGroupLabel`)}</h3>
            <p className="text-xs text-muted-foreground">{t(`${s}.memoryGroupDescription`)}</p>
          </div>
        </div>

        <div className="space-y-2">
          <div className={`rounded-lg border p-3 sm:p-4 ${value.share_memory ? "border-warning/25 bg-orange-50/30" : ""}`}>
            <div className="flex items-center justify-between">
              <InfoLabel htmlFor={`${uid}-shareMemory`} tip={t(`${s}.shareMemoryTip`)}>{t(`${s}.shareMemory`)}</InfoLabel>
              <Switch
                id={`${uid}-shareMemory`}
                checked={value.share_memory ?? false}
                onCheckedChange={(v) => onChange({ ...value, share_memory: v })}
              />
            </div>
            {value.share_memory && (
              <p className="mt-2 text-xs text-warning-text">
                {t(`${s}.shareMemoryNote`)}
              </p>
            )}
          </div>

          <div className={`rounded-lg border p-3 sm:p-4 ${value.share_knowledge_graph ? "border-warning/25 bg-orange-50/30" : ""}`}>
            <div className="flex items-center justify-between">
              <InfoLabel htmlFor={`${uid}-shareKG`} tip={t(`${s}.shareKGTip`)}>{t(`${s}.shareKG`)}</InfoLabel>
              <Switch
                id={`${uid}-shareKG`}
                checked={value.share_knowledge_graph ?? false}
                onCheckedChange={(v) => onChange({ ...value, share_knowledge_graph: v })}
              />
            </div>
            {value.share_knowledge_graph && (
              <p className="mt-2 text-xs text-warning-text">
                {t(`${s}.shareKGNote`)}
              </p>
            )}
          </div>

          <div className={`rounded-lg border p-3 sm:p-4 ${value.share_sessions ? "border-warning/25 bg-orange-50/30" : ""}`}>
            <div className="flex items-center justify-between">
              <InfoLabel htmlFor={`${uid}-shareSessions`} tip={t(`${s}.shareSessionsTip`)}>{t(`${s}.shareSessions`)}</InfoLabel>
              <Switch
                id={`${uid}-shareSessions`}
                checked={value.share_sessions ?? false}
                onCheckedChange={(v) => onChange({ ...value, share_sessions: v })}
              />
            </div>
            {value.share_sessions && (
              <p className="mt-2 text-xs text-warning-text">
                {t(`${s}.shareSessionsNote`)}
              </p>
            )}
          </div>
        </div>
      </section>

      {/* ── Section 2: Workspace File Sharing ── */}
      <section className="space-y-3">
        <div className="flex items-center gap-2">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-warning-subtle">
            <Shield className="h-4 w-4 text-warning-text" />
          </div>
          <div>
            <h3 className="text-sm font-semibold">{t(`${s}.title`)}</h3>
            <p className="text-xs text-muted-foreground">{t(`${s}.description`)}</p>
          </div>
        </div>

        <div className={`rounded-lg border p-3 space-y-4 sm:p-4 ${isWorkspaceActive ? "border-warning/25 bg-amber-50/30" : ""}`}>
          {/* Security warning */}
          <Alert variant="destructive" className="border-warning/25 bg-warning-subtle text-warning-text [&>svg]:text-warning-text">
            <AlertTriangle className="h-4 w-4" />
            <AlertDescription className="text-xs">
              {t(`${s}.warning`)}
            </AlertDescription>
          </Alert>

          {/* DM / Group sharing toggles */}
          <div className="space-y-2">
            <p className="text-xs font-medium text-muted-foreground">{t(`${s}.folderGroupLabel`)}</p>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div className="flex items-center justify-between rounded-md border bg-background p-3">
                <InfoLabel htmlFor={`${uid}-sharedDm`} tip={t(`${s}.sharedDmTip`)}>{t(`${s}.sharedDm`)}</InfoLabel>
                <Switch
                  id={`${uid}-sharedDm`}
                  checked={value.shared_dm ?? false}
                  onCheckedChange={(v) => onChange({ ...value, shared_dm: v })}
                />
              </div>
              <div className="flex items-center justify-between rounded-md border bg-background p-3">
                <InfoLabel htmlFor={`${uid}-sharedGroup`} tip={t(`${s}.sharedGroupTip`)}>{t(`${s}.sharedGroup`)}</InfoLabel>
                <Switch
                  id={`${uid}-sharedGroup`}
                  checked={value.shared_group ?? false}
                  onCheckedChange={(v) => onChange({ ...value, shared_group: v })}
                />
              </div>
            </div>
          </div>

          {/* Shared users allowlist */}
          <div className="space-y-2">
            <InfoLabel htmlFor={`${uid}-sharedUsers`} tip={t(`${s}.sharedUsersTip`)}>{t(`${s}.sharedUsers`)}</InfoLabel>
            {existingUsers.length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {existingUsers.map((u, i) => (
                  <Badge key={i} variant="secondary" className="gap-1 pr-1">
                    <span className="max-w-48 truncate">{formatUserLabel(u, resolve)}</span>
                    <button
                      type="button"
                      aria-label={t("common:remove")}
                      onClick={() => removeUser(i)}
                      className="rounded-full p-0.5 hover:bg-muted-foreground/20"
                    >
                      <X className="h-3 w-3" />
                    </button>
                  </Badge>
                ))}
              </div>
            )}
            <div className="flex gap-2">
              <UserPickerCombobox
                id={`${uid}-sharedUsers`}
                value={contactSearch}
                onChange={(val) => setContactSearch(val)}
                placeholder={t(`${s}.userIdPlaceholder`)}
                className="flex-1"
              />
              <Button
                type="button"
                aria-label={t(`${s}.addUser`)}
                variant="outline"
                size="icon"
                onClick={() => addUser(contactSearch)}
                disabled={!contactSearch.trim()}
                className="shrink-0"
              >
                <Plus className="h-4 w-4" />
              </Button>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
