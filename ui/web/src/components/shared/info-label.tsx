import { Info } from "lucide-react";
import { Label } from "@/components/ui/label";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

/** Label with an (i) tooltip icon for field descriptions. */
export function InfoLabel({
  children,
  tip,
  labelClassName,
  htmlFor,
}: {
  children: React.ReactNode;
  tip: string;
  labelClassName?: string;
  /** Associates the label with a form control by id. */
  htmlFor?: string;
}) {
  return (
    <div className="flex items-center gap-1.5">
      <Label className={labelClassName} htmlFor={htmlFor}>
        {children}
      </Label>
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label={tip}
              className="inline-flex h-3.5 w-3.5 shrink-0 cursor-help items-center justify-center text-muted-foreground"
            >
              <Info className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
          </TooltipTrigger>
          <TooltipContent side="top" className="max-w-64">
            {tip}
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    </div>
  );
}
