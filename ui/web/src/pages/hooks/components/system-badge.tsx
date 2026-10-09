import { useTranslation } from "react-i18next";

// SystemBadge marks a hook row as shipped by the binary (source='builtin').
// User cannot edit content — only toggle enabled. Placed next to the event
// chip in hook-list-row so it's visible without opening the detail panel.
export function SystemBadge() {
  const { t } = useTranslation("hooks");
  return (
    <span
      className="ml-1 inline-flex items-center rounded px-1.5 py-0.5 text-2xs font-medium bg-info-subtle"
      title={t("form.builtinReadonly")}
    >
      {t("table.systemBadge")}
    </span>
  );
}
