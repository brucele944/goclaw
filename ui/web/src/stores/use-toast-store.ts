import { create } from "zustand";

export interface Toast {
  id: string;
  title: string;
  message?: string;
  variant: "default" | "success" | "destructive" | "warning";
  duration?: number;
}

interface ToastState {
  toasts: Toast[];
  addToast: (toast: Omit<Toast, "id">) => void;
  dismiss: (id: string) => void;
  /** Freeze the auto-dismiss countdown for a toast (hover / focus within). */
  pause: (id: string) => void;
  /** Resume a paused countdown with the time that was remaining. */
  resume: (id: string) => void;
}

/** Errors stay long enough to be read; everything else dismisses quickly. */
const DEFAULT_DURATION = 4000;
const ERROR_DURATION = 8000;

interface TimerRecord {
  timeout: ReturnType<typeof setTimeout> | null;
  /** Milliseconds left when the timer was last paused. */
  remaining: number;
  /** Epoch ms when the current (un-paused) run started. */
  startedAt: number;
}

const timers = new Map<string, TimerRecord>();
/** Pause requests can come from several sources (hover + focus); reference counted. */
const pauseCounts = new Map<string, number>();

let counter = 0;

function removeToast(id: string) {
  const record = timers.get(id);
  if (record?.timeout) clearTimeout(record.timeout);
  timers.delete(id);
  pauseCounts.delete(id);
  useToastStore.setState((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }));
}

function startTimer(id: string, duration: number) {
  const record: TimerRecord = { timeout: null, remaining: duration, startedAt: Date.now() };
  record.timeout = setTimeout(() => removeToast(id), duration);
  timers.set(id, record);
}

function pauseTimer(id: string) {
  const count = pauseCounts.get(id) ?? 0;
  pauseCounts.set(id, count + 1);
  const record = timers.get(id);
  if (count > 0 || !record || record.timeout === null) return;
  clearTimeout(record.timeout);
  record.remaining = Math.max(0, record.remaining - (Date.now() - record.startedAt));
  record.timeout = null;
}

function resumeTimer(id: string) {
  const count = pauseCounts.get(id) ?? 0;
  if (count > 1) {
    pauseCounts.set(id, count - 1);
    return;
  }
  pauseCounts.delete(id);
  const record = timers.get(id);
  if (!record || record.timeout !== null) return;
  record.startedAt = Date.now();
  record.timeout = setTimeout(() => removeToast(id), record.remaining);
}

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  addToast: (toast) => {
    const id = String(++counter);
    set((s) => ({ toasts: [...s.toasts, { ...toast, id }] }));

    const duration = toast.duration ?? (toast.variant === "destructive" ? ERROR_DURATION : DEFAULT_DURATION);
    if (Number.isFinite(duration)) startTimer(id, Math.max(0, duration));
  },
  dismiss: (id) => removeToast(id),
  pause: (id) => pauseTimer(id),
  resume: (id) => resumeTimer(id),
}));

// Convenience functions for one-line usage across the app
export const toast = {
  success: (title: string, message?: string) =>
    useToastStore.getState().addToast({ title, message, variant: "success" }),
  error: (title: string, message?: string) =>
    useToastStore.getState().addToast({ title, message, variant: "destructive" }),
  warning: (title: string, message?: string) =>
    useToastStore.getState().addToast({ title, message, variant: "warning" }),
  info: (title: string, message?: string) =>
    useToastStore.getState().addToast({ title, message, variant: "default" }),
};
