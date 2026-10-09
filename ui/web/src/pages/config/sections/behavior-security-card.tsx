import { useId } from "react";
import { ShieldCheck, KeyRound, Info } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

interface SecurityValues {
  injection_action?: string;
  scrub_credentials?: boolean;
}

interface Props {
  value: SecurityValues;
  onChange: (v: SecurityValues) => void;
}

/** Input security settings with visual emphasis on impact. */
export function BehaviorSecurityCard({ value, onChange }: Props) {
  const { t } = useTranslation("config");
  const uid = useId();

  const action = value.injection_action ?? "warn";
  const scrub = value.scrub_credentials !== false;

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-base">{t("behavior.securityTitle")}</CardTitle>
        <CardDescription>{t("behavior.securityDescription")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-0">
        {/* Injection Action */}
        <div className="border-b py-4">
          <div className="flex items-start justify-between gap-4">
            <div className="flex items-start gap-3">
              <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-warning-text" />
              <div className="space-y-1">
                <Label htmlFor={`${uid}-injection-action`} className="text-sm font-medium">{t("gateway.injectionAction")}</Label>
                <p className="text-xs text-muted-foreground">{t("behavior.injectionActionHint")}</p>
              </div>
            </div>
            <Select
              value={action}
              onValueChange={(v) => onChange({ ...value, injection_action: v })}
            >
              <SelectTrigger id={`${uid}-injection-action`} className="w-32 shrink-0"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="off">Off</SelectItem>
                <SelectItem value="log">Log</SelectItem>
                <SelectItem value="warn">Warn</SelectItem>
                <SelectItem value="block">Block</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {action === "block" && (
            <div className="mt-3 flex items-start gap-2 rounded-md border border-warning/25 bg-warning-subtle px-3 py-2 text-xs text-warning-text">
              <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{t("behavior.injectionBlockInfo")}</span>
            </div>
          )}

          {action === "off" && (
            <div className="mt-3 flex items-start gap-2 rounded-md border border-destructive/25 bg-destructive-subtle px-3 py-2 text-xs text-destructive-text">
              <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{t("behavior.injectionOffInfo")}</span>
            </div>
          )}
        </div>

        {/* Scrub Credentials */}
        <div className="py-4">
          <div className="flex items-center justify-between gap-4">
            <div className="flex items-center gap-3">
              <KeyRound className={cn("h-4 w-4 shrink-0", scrub ? "text-success-text" : "text-destructive-text")} />
              <div className="space-y-1">
                <Label htmlFor={`${uid}-scrub-credentials`} className="text-sm font-medium">{t("tools.scrubCredentials")}</Label>
                <p className="text-xs text-muted-foreground">{t("behavior.scrubCredentialsHint")}</p>
              </div>
            </div>
            <Switch
              id={`${uid}-scrub-credentials`}
              checked={scrub}
              onCheckedChange={(v) => onChange({ ...value, scrub_credentials: v })}
              className="shrink-0"
            />
          </div>

          {scrub && (
            <div className="mt-3 flex items-start gap-2 rounded-md border border-success/25 bg-success-subtle px-3 py-2 text-xs text-success-text dark:bg-emerald-950/30">
              <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{t("behavior.scrubCredentialsInfo")}</span>
            </div>
          )}

          {!scrub && (
            <div className="mt-3 flex items-start gap-2 rounded-md border border-destructive/25 bg-destructive-subtle px-3 py-2 text-xs text-destructive-text">
              <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{t("behavior.scrubCredentialsOffInfo")}</span>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
