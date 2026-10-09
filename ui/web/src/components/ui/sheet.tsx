"use client"

import * as React from "react"
import { XIcon } from "lucide-react"
import { Dialog as SheetPrimitive } from "radix-ui"
import { useTranslation } from "react-i18next"

import { cn } from "@/lib/utils"

/**
 * Remembers the element that was focused when the sheet opened so `SheetContent`
 * can restore focus on close. Radix only restores focus to a `Dialog.Trigger`,
 * but these sheets are opened programmatically by unrelated buttons.
 *
 * The capture runs in a layout effect (before Radix's passive autofocus effect),
 * so the recorded element is still the one the user activated.
 */
const SheetReturnFocusContext = React.createContext<React.RefObject<HTMLElement | null> | null>(null)

function Sheet({
  open,
  onOpenChange,
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Root>) {
  const returnFocusRef = React.useRef<HTMLElement | null>(null)

  React.useLayoutEffect(() => {
    if (open && typeof document !== "undefined" && document.activeElement instanceof HTMLElement) {
      returnFocusRef.current = document.activeElement
    }
  }, [open])

  return (
    <SheetReturnFocusContext.Provider value={returnFocusRef}>
      <SheetPrimitive.Root
        data-slot="sheet"
        open={open}
        onOpenChange={onOpenChange}
        {...props}
      />
    </SheetReturnFocusContext.Provider>
  )
}

function SheetTrigger({
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Trigger>) {
  return <SheetPrimitive.Trigger data-slot="sheet-trigger" {...props} />
}

function SheetPortal({
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Portal>) {
  return <SheetPrimitive.Portal data-slot="sheet-portal" {...props} />
}

function SheetClose({
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Close>) {
  return <SheetPrimitive.Close data-slot="sheet-close" {...props} />
}

function SheetOverlay({
  className,
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Overlay>) {
  return (
    <SheetPrimitive.Overlay
      data-slot="sheet-overlay"
      className={cn(
        "data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 fixed inset-0 z-50 bg-black/50",
        className
      )}
      {...props}
    />
  )
}

function SheetContent({
  className,
  children,
  side = "right",
  title,
  description,
  showCloseButton = true,
  ...props
}: React.ComponentProps<typeof SheetPrimitive.Content> & {
  side?: "left" | "right"
  /** Accessible name for the dialog. Rendered visually hidden. */
  title: string
  /** Optional accessible description. Rendered visually hidden. */
  description?: string
  showCloseButton?: boolean
}) {
  const { t } = useTranslation("common")
  const returnFocusRef = React.useContext(SheetReturnFocusContext)
  return (
    <SheetPortal>
      <SheetOverlay />
      <SheetPrimitive.Content
        data-slot="sheet-content"
        aria-modal="true"
        onCloseAutoFocus={(event) => {
          // Override Radix, which focuses the Dialog.Trigger (absent here).
          event.preventDefault()
          const target = returnFocusRef?.current
          if (target?.isConnected) target.focus()
        }}
        {...(description === undefined ? { "aria-describedby": undefined } : {})}
        className={cn(
          "bg-background data-[state=open]:animate-in data-[state=closed]:animate-out fixed z-50 flex flex-col gap-4 shadow-lg transition ease-in-out data-[state=closed]:duration-300 data-[state=open]:duration-500",
          side === "right" &&
            "data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right inset-y-0 right-0 h-full w-3/4 border-l safe-top safe-bottom safe-right",
          side === "left" &&
            "data-[state=closed]:slide-out-to-left data-[state=open]:slide-in-from-left inset-y-0 left-0 h-full w-3/4 border-r safe-top safe-bottom safe-left",
          className
        )}
        {...props}
      >
        <SheetPrimitive.Title className="sr-only">{title}</SheetPrimitive.Title>
        {description !== undefined && (
          <SheetPrimitive.Description className="sr-only">
            {description}
          </SheetPrimitive.Description>
        )}
        {children}
        {showCloseButton && (
          <SheetPrimitive.Close
            data-slot="sheet-close"
            className="cursor-pointer ring-offset-background focus-visible:ring-ring data-[state=open]:bg-accent data-[state=open]:text-muted-foreground absolute top-4 right-4 rounded-xs opacity-70 transition-opacity hover:opacity-100 focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:outline-hidden disabled:pointer-events-none [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4"
          >
            <XIcon />
            <span className="sr-only">{t("close")}</span>
          </SheetPrimitive.Close>
        )}
      </SheetPrimitive.Content>
    </SheetPortal>
  )
}

export {
  Sheet,
  SheetClose,
  SheetContent,
  SheetOverlay,
  SheetPortal,
  SheetTrigger,
}
