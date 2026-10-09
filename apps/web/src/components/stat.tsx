import * as React from "react";
import { ArrowDownRight, ArrowUpRight } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/misc";
import { cn } from "@/lib/utils";

export function StatCard({ label, value, sub, delta, icon, invert, loading }: {
  label: string;
  value: React.ReactNode;
  sub?: React.ReactNode;
  delta?: number | null;
  icon?: React.ReactNode;
  invert?: boolean; // a rise is bad (e.g. failures)
  loading?: boolean;
}) {
  const good = delta != null && (invert ? delta < 0 : delta > 0);
  return (
    <Card className="p-4">
      <div className="flex items-center justify-between text-sm text-muted-foreground">
        <span>{label}</span>
        {icon && <span className="rounded-md bg-accent p-1.5 text-accent-foreground [&_svg]:size-4">{icon}</span>}
      </div>
      {loading ? (
        <Skeleton className="mt-3 h-8 w-28" />
      ) : (
        <div className="mt-2 text-2xl font-semibold tracking-tight tabular">{value}</div>
      )}
      <div className="mt-1 flex items-center gap-2 text-xs">
        {delta != null && isFinite(delta) && (
          <span className={cn("inline-flex items-center gap-0.5 font-medium", good ? "text-success" : "text-danger")}>
            {delta >= 0 ? <ArrowUpRight className="size-3.5" /> : <ArrowDownRight className="size-3.5" />}
            {Math.abs(delta).toFixed(1)}%
          </span>
        )}
        {sub && <span className="text-muted-foreground">{sub}</span>}
      </div>
    </Card>
  );
}
