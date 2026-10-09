import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useTranslation } from "react-i18next";
import { AlertCircle, Eye, EyeOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { tokenFormSchema, type TokenFormData } from "@/schemas/login.schema";

interface TokenFormProps {
  onSubmit: (userId: string, token: string) => void;
}

export function TokenForm({ onSubmit }: TokenFormProps) {
  const { t } = useTranslation("login");
  const [connecting, setConnecting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [showToken, setShowToken] = useState(false);

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<TokenFormData>({
    resolver: zodResolver(tokenFormSchema),
    defaultValues: { userId: "system", token: "" },
  });

  const onValid = async (data: TokenFormData) => {
    setConnecting(true);
    setError(null);

    try {
      const res = await fetch("/v1/agents", {
        headers: {
          Authorization: `Bearer ${data.token.trim()}`,
          "X-GoClaw-User-Id": data.userId.trim(),
        },
      });

      if (res.status === 401) {
        setError(t("token.errorInvalidCredentials"));
        return;
      }

      if (!res.ok) {
        setError(t("token.errorServer", { status: res.status }));
        return;
      }

      onSubmit(data.userId.trim(), data.token.trim());
    } catch {
      setError(t("token.errorCannotConnect"));
    } finally {
      setConnecting(false);
    }
  };

  const userIdDescribedBy = [errors.userId ? "userId-error" : null, "userId-hint"]
    .filter(Boolean)
    .join(" ");

  return (
    <form onSubmit={handleSubmit(onValid)} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="userId" className="text-sm font-medium">
          {t("token.userId")}
        </Label>
        <Input
          id="userId"
          type="text"
          autoComplete="username"
          {...register("userId")}
          placeholder={t("token.userIdPlaceholder")}
          className="text-base md:text-sm"
          aria-invalid={errors.userId ? true : undefined}
          aria-describedby={userIdDescribedBy}
          autoFocus
          disabled={connecting}
        />
        {errors.userId && (
          <p id="userId-error" role="alert" className="text-xs text-destructive">
            {errors.userId.message}
          </p>
        )}
        <p id="userId-hint" className="text-xs text-muted-foreground">
          {t("token.userIdHint")}
        </p>
      </div>

      <div className="space-y-2">
        <Label htmlFor="token" className="text-sm font-medium">
          {t("token.gatewayToken")}
        </Label>
        <div className="relative">
          <Input
            id="token"
            type={showToken ? "text" : "password"}
            autoComplete="current-password"
            {...register("token")}
            placeholder={t("token.tokenPlaceholder")}
            className="text-base md:text-sm pr-10"
            aria-invalid={errors.token ? true : undefined}
            aria-describedby={errors.token ? "token-error" : undefined}
            disabled={connecting}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={() => setShowToken((v) => !v)}
            aria-pressed={showToken}
            aria-label={showToken ? t("token.hidePassword") : t("token.showPassword")}
            className="absolute inset-y-0 right-1 my-auto text-muted-foreground"
          >
            {showToken ? (
              <EyeOff className="h-4 w-4" aria-hidden="true" />
            ) : (
              <Eye className="h-4 w-4" aria-hidden="true" />
            )}
          </Button>
        </div>
        {errors.token && (
          <p id="token-error" role="alert" className="text-xs text-destructive">
            {errors.token.message}
          </p>
        )}
      </div>

      {error && (
        <div
          role="alert"
          className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <span>{error}</span>
        </div>
      )}

      <Button type="submit" className="w-full" disabled={connecting}>
        {connecting ? t("token.connecting") : t("token.connect")}
      </Button>
    </form>
  );
}
