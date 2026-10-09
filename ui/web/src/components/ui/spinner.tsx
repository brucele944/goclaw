import * as React from "react"

import { cn } from "@/lib/utils"

const sizeClass = {
  sm: "size-4 border-2",
  md: "size-5 border-2",
  lg: "size-6 border-2",
} as const

function Spinner({
  className,
  size = "md",
  ...props
}: React.ComponentProps<"span"> & { size?: keyof typeof sizeClass }) {
  return (
    <span
      role="status"
      aria-hidden
      data-slot="spinner"
      className={cn(
        "inline-block animate-spin rounded-full border-muted-foreground border-t-transparent",
        sizeClass[size],
        className,
      )}
      {...props}
    />
  )
}

export { Spinner }
