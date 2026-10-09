import { X, MessageSquare, Paperclip } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useIsMobile } from "@/hooks/use-media-query";
import { Sheet, SheetContent } from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";
import type { ActiveTeamTask } from "@/types/chat";

interface TaskPanelProps {
  tasks: ActiveTeamTask[];
  open: boolean;
  onClose: () => void;
}

/** Panel body shared by the inline desktop sidebar and the mobile Sheet. */
function TaskPanelBody({
  tasks,
  onClose,
  className,
}: {
  tasks: ActiveTeamTask[];
  onClose: () => void;
  className?: string;
}) {
  const { t } = useTranslation("chat");
  const { t: tCommon } = useTranslation("common");

  return (
    <div className={cn("flex h-full w-72 shrink-0 flex-col bg-background", className)}>
      {/* Header */}
      <div className="flex items-center justify-between border-b px-3 py-2">
        <span className="text-sm font-medium">
          {t("runningTasksCount", { count: tasks.length })}
        </span>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={onClose}
          aria-label={tCommon("close")}
        >
          <X className="h-4 w-4" aria-hidden="true" />
        </Button>
      </div>

      {/* Task list */}
      <div className="flex-1 overflow-y-auto overscroll-contain p-2 space-y-2">
        {tasks.length === 0 ? (
          <p className="px-2 py-4 text-center text-xs text-muted-foreground">{t("noActiveTasks")}</p>
        ) : (
          tasks.map((task) => <TaskCard key={task.taskId} task={task} />)
        )}
      </div>
    </div>
  );
}

export function TaskPanel({ tasks, open, onClose }: TaskPanelProps) {
  const { t } = useTranslation("chat");
  const isMobile = useIsMobile();

  if (isMobile) {
    return (
      <Sheet
        open={open}
        onOpenChange={(next) => {
          if (!next) onClose();
        }}
      >
        <SheetContent
          side="right"
          title={t("runningTasks")}
          showCloseButton={false}
          className="w-full max-w-[85vw] gap-0"
        >
          <TaskPanelBody
            tasks={tasks}
            onClose={onClose}
            className="w-full max-w-none border-l-0"
          />
        </SheetContent>
      </Sheet>
    );
  }

  if (!open) return null;
  return <TaskPanelBody tasks={tasks} onClose={onClose} className="border-l" />;
}

function TaskCard({ task }: { task: ActiveTeamTask }) {
  const pct = task.progressPercent;

  return (
    <div className="rounded-lg border bg-card p-2.5 text-xs shadow-sm">
      {/* Header: number + owner + counters */}
      <div className="flex items-center gap-1.5 text-muted-foreground">
        <span className="font-mono">#{task.taskNumber}</span>
        <span className="truncate">{task.ownerDisplayName || task.ownerAgentKey || "unassigned"}</span>
        <span className="ml-auto flex items-center gap-2">
          {(task.commentCount ?? 0) > 0 && (
            <span className="flex items-center gap-0.5">
              <MessageSquare className="h-3 w-3" /> {task.commentCount}
            </span>
          )}
          {(task.attachmentCount ?? 0) > 0 && (
            <span className="flex items-center gap-0.5">
              <Paperclip className="h-3 w-3" /> {task.attachmentCount}
            </span>
          )}
        </span>
      </div>

      {/* Progress bar — above the step message */}
      {pct != null && (
        <div className="mt-1.5">
          <div className="flex items-center gap-1.5">
            <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary transition-all duration-300"
                style={{ width: `${Math.min(pct, 100)}%` }}
              />
            </div>
            <span className="shrink-0 text-2xs tabular-nums text-muted-foreground">{pct}%</span>
          </div>
        </div>
      )}

      {/* Step message — full text, no truncation */}
      {task.progressStep && (
        <p className="mt-1 text-xs-plus leading-snug text-muted-foreground">
          {task.progressStep}
        </p>
      )}

      {/* Subject — full text */}
      <p className="mt-1.5 font-medium leading-snug">{task.subject}</p>
    </div>
  );
}
