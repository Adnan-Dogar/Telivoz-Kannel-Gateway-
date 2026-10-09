import * as React from "react";
import { cn } from "@/lib/utils";

const tones = {
  neutral: "bg-muted text-muted-foreground",
  primary: "bg-accent text-accent-foreground",
  success: "bg-success/12 text-success",
  warning: "bg-warning/15 text-[color-mix(in_oklch,var(--warning)_70%,var(--foreground))]",
  danger: "bg-danger/12 text-danger",
};

export function Badge({ tone = "neutral", className, ...props }: React.HTMLAttributes<HTMLSpanElement> & { tone?: keyof typeof tones }) {
  return <span className={cn("inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium", tones[tone], className)} {...props} />;
}

export function Dot({ tone = "neutral", pulse }: { tone?: "success" | "warning" | "danger" | "neutral"; pulse?: boolean }) {
  const color = { success: "bg-success", warning: "bg-warning", danger: "bg-danger", neutral: "bg-muted-foreground" }[tone];
  return <span className={cn("inline-block size-2 rounded-full", color, pulse && "pulse-dot")} />;
}

const statusTone: Record<string, keyof typeof tones> = {
  delivered: "success", active: "success", enabled: "success", bound: "success", done: "success",
  sent: "primary", queued: "primary", running: "primary", connecting: "warning",
  undelivered: "danger", rejected: "danger", failed: "danger", expired: "warning", unknown: "neutral",
  disabled: "neutral", down: "danger", cancelled: "neutral",
};

export function StatusBadge({ status }: { status: string }) {
  return <Badge tone={statusTone[status] ?? "neutral"}>{status}</Badge>;
}
