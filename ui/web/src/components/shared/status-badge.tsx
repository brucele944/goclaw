import { cn } from "@/lib/utils";

type Status = "success" | "warning" | "error" | "info" | "default";

const statusClasses: Record<Status, string> = {
  success: "bg-success-subtle text-success-text",
  warning: "bg-warning-subtle text-warning-text",
  error: "bg-destructive-subtle text-destructive-text",
  info: "bg-info-subtle text-info-text",
  default: "bg-muted text-muted-foreground",
};

interface StatusBadgeProps {
  status: Status;
  label: string;
  className?: string;
}

export function StatusBadge({ status, label, className }: StatusBadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium",
        statusClasses[status],
        className,
      )}
    >
      <span className={cn(
        "h-1.5 w-1.5 rounded-full",
        status === "success" && "bg-success",
        status === "warning" && "bg-warning",
        status === "error" && "bg-destructive",
        status === "info" && "bg-info",
        status === "default" && "bg-muted-foreground",
      )} />
      {label}
    </span>
  );
}
