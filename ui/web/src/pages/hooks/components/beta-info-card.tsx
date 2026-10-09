import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronUp, Info, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";

const LS_KEY = "goclaw:hooks:beta-card-collapsed";

function loadCollapsed(): boolean {
  try {
    return localStorage.getItem(LS_KEY) === "1";
  } catch {
    return false;
  }
}

function saveCollapsed(v: boolean) {
  try {
    localStorage.setItem(LS_KEY, v ? "1" : "0");
  } catch {
    // ignore quota/private-mode errors
  }
}

export function BetaInfoCard() {
  const { t } = useTranslation("hooks");
  const [collapsed, setCollapsed] = useState<boolean>(loadCollapsed);
  const [modalOpen, setModalOpen] = useState(false);

  const toggle = () => {
    const next = !collapsed;
    setCollapsed(next);
    saveCollapsed(next);
  };

  return (
    <>
      <div className="rounded-lg border border-info/25 bg-info-subtle">
        <button
          type="button"
          onClick={toggle}
          className="flex w-full items-center gap-2 px-4 py-3 text-left"
          aria-expanded={!collapsed}
        >
          <Sparkles className="h-4 w-4 shrink-0 text-info-text" />
          <span className="text-sm font-medium dark:text-blue-100">
            {t("beta.title")}
          </span>
          <span className="rounded-full bg-info-subtle px-1.5 py-0.5 text-2xs font-semibold uppercase dark:text-blue-200">
            {t("beta.badge")}
          </span>
          <div className="flex-1" />
          {collapsed ? (
            <ChevronDown className="h-4 w-4 text-info-text" />
          ) : (
            <ChevronUp className="h-4 w-4 text-info-text" />
          )}
        </button>

        {!collapsed && (
          <div className="space-y-3 px-4 pb-4 text-sm dark:text-blue-100/90">
            <p>{t("beta.description")}</p>

            <div className="grid gap-3 sm:grid-cols-3">
              <BetaPoint title={t("beta.howItWorksTitle1")} body={t("beta.howItWorksBody1")} />
              <BetaPoint title={t("beta.howItWorksTitle2")} body={t("beta.howItWorksBody2")} />
              <BetaPoint title={t("beta.howItWorksTitle3")} body={t("beta.howItWorksBody3")} />
            </div>

            <div className="flex items-center justify-between">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setModalOpen(true)}
                className="h-7 gap-1 text-xs text-info-text hover:bg-info-subtle hover:text-blue-900 dark:hover:text-blue-100"
              >
                <Info className="h-3.5 w-3.5" />
                {t("beta.learnMore")}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={toggle}
                className="h-7 text-xs text-info-text hover:bg-info-subtle hover:text-blue-900 dark:hover:text-blue-100"
              >
                {t("beta.collapse")}
              </Button>
            </div>
          </div>
        )}
      </div>

      <HooksExplainerModal open={modalOpen} onOpenChange={setModalOpen} />
    </>
  );
}

function HooksExplainerModal({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation("hooks");
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto max-sm:inset-0 max-sm:rounded-none sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="text-base">{t("beta.modalTitle")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4 text-sm text-foreground/90">
          <p>{t("beta.modalIntro")}</p>
          <ExplainerSection title={t("beta.modalSkillTitle")} body={t("beta.modalSkillBody")} />
          <ExplainerSection title={t("beta.modalMcpTitle")} body={t("beta.modalMcpBody")} />
          <ExplainerSection title={t("beta.modalBuiltinTitle")} body={t("beta.modalBuiltinBody")} />
        </div>
        <DialogFooter>
          <Button variant="secondary" size="sm" onClick={() => onOpenChange(false)}>
            {t("beta.modalClose")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ExplainerSection({ title, body }: { title: string; body: string }) {
  return (
    <div className="rounded-md border bg-muted/40 p-3">
      <p className="text-xs font-semibold">{title}</p>
      <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">{body}</p>
    </div>
  );
}

function BetaPoint({ title, body }: { title: string; body: string }) {
  return (
    <div className="rounded border border-blue-200/60 bg-white/60 p-3">
      <p className="text-xs font-semibold dark:text-blue-100">{title}</p>
      <p className="mt-1 text-xs dark:text-blue-200/70">{body}</p>
    </div>
  );
}
