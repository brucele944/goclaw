import { useState, useEffect, useId } from "react";
import { Save } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { InfoLabel } from "@/components/shared/info-label";
import { KeyValueEditor } from "@/components/shared/key-value-editor";

interface TelemetryData {
  enabled?: boolean;
  endpoint?: string;
  protocol?: string;
  insecure?: boolean;
  service_name?: string;
  headers?: Record<string, string>;
}

const DEFAULT: TelemetryData = {};

interface Props {
  data: TelemetryData | undefined;
  onSave: (value: TelemetryData) => Promise<void>;
  saving: boolean;
}

export function TelemetrySection({ data, onSave, saving }: Props) {
  const { t } = useTranslation("config");
  const uid = useId();
  const [draft, setDraft] = useState<TelemetryData>(data ?? DEFAULT);
  const [headers, setHeaders] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    setDraft(data ?? DEFAULT);
    setHeaders(data?.headers ?? {});
    setDirty(false);
  }, [data]);

  const update = (patch: Partial<TelemetryData>) => {
    setDraft((prev) => ({ ...prev, ...patch }));
    setDirty(true);
  };

  const handleSave = () => {
    onSave({ ...draft, headers: Object.keys(headers).length > 0 ? headers : undefined }).catch(() => {});
  };

  if (!data) return null;

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="text-base">{t("telemetry.title")}</CardTitle>
        <CardDescription>{t("telemetry.description")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-center justify-between">
          <InfoLabel tip={t("telemetry.enabledTip")} htmlFor={`${uid}-enabled`}>{t("telemetry.enabled")}</InfoLabel>
          <Switch id={`${uid}-enabled`} checked={draft.enabled ?? false} onCheckedChange={(v) => update({ enabled: v })} />
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="grid gap-1.5">
            <InfoLabel tip={t("telemetry.endpointTip")} htmlFor={`${uid}-endpoint`}>{t("telemetry.endpoint")}</InfoLabel>
            <Input
              id={`${uid}-endpoint`}
              value={draft.endpoint ?? ""}
              onChange={(e) => update({ endpoint: e.target.value })}
              placeholder="localhost:4317"
            />
          </div>
          <div className="grid gap-1.5">
            <InfoLabel tip={t("telemetry.protocolTip")} htmlFor={`${uid}-protocol`}>{t("telemetry.protocol")}</InfoLabel>
            <Select value={draft.protocol ?? "grpc"} onValueChange={(v) => update({ protocol: v })}>
              <SelectTrigger id={`${uid}-protocol`}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="grpc">gRPC</SelectItem>
                <SelectItem value="http">HTTP</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="grid gap-1.5">
            <InfoLabel tip={t("telemetry.serviceNameTip")} htmlFor={`${uid}-service-name`}>{t("telemetry.serviceName")}</InfoLabel>
            <Input
              id={`${uid}-service-name`}
              value={draft.service_name ?? ""}
              onChange={(e) => update({ service_name: e.target.value })}
              placeholder="goclaw-gateway"
            />
          </div>
          <div className="flex items-center justify-between">
            <InfoLabel tip={t("telemetry.insecureTip")} htmlFor={`${uid}-insecure`}>{t("telemetry.insecure")}</InfoLabel>
            <Switch id={`${uid}-insecure`} checked={draft.insecure ?? false} onCheckedChange={(v) => update({ insecure: v })} />
          </div>
        </div>

        <div className="grid gap-1.5" role="group" aria-label={t("telemetry.headers")}>
          <InfoLabel tip={t("telemetry.headersTip")}>{t("telemetry.headers")}</InfoLabel>
          <KeyValueEditor
            value={headers}
            onChange={(v) => { setHeaders(v); setDirty(true); }}
            keyPlaceholder={t("telemetry.headerKeyPlaceholder")}
            valuePlaceholder={t("telemetry.headerValuePlaceholder")}
            addLabel={t("telemetry.addHeader")}
          />
        </div>

        {dirty && (
          <div className="flex justify-end pt-2">
            <Button size="sm" onClick={handleSave} disabled={saving} className="gap-1.5">
              <Save className="h-3.5 w-3.5" /> {saving ? t("saving") : t("save")}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
