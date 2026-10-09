import * as React from "react";
import { Calendar } from "lucide-react";
import { Select } from "@/components/ui/input";

export type Period = { key: string; from: string; to: string; label: string };

const presets: { key: string; label: string; hours: number }[] = [
  { key: "1h", label: "Last hour", hours: 1 },
  { key: "24h", label: "Last 24 hours", hours: 24 },
  { key: "7d", label: "Last 7 days", hours: 24 * 7 },
  { key: "30d", label: "Last 30 days", hours: 24 * 30 },
  { key: "90d", label: "Last 90 days", hours: 24 * 90 },
  { key: "365d", label: "Last 12 months", hours: 24 * 365 },
  { key: "all", label: "All time", hours: 24 * 365 * 10 },
];

export function makePeriod(key: string): Period {
  const p = presets.find((x) => x.key === key) ?? presets[1];
  const to = new Date();
  // Round to the minute so query keys stay stable for a while.
  to.setSeconds(0, 0);
  const from = new Date(to.getTime() - p.hours * 3600_000);
  return { key: p.key, label: p.label, from: from.toISOString(), to: new Date(to.getTime() + 60_000).toISOString() };
}

export function usePeriod(initial = "24h") {
  const [key, setKey] = React.useState(() => {
    try {
      return localStorage.getItem("period") ?? initial;
    } catch {
      return initial;
    }
  });
  const [tick, setTick] = React.useState(0);
  React.useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 60_000);
    return () => clearInterval(id);
  }, []);
  const period = React.useMemo(() => makePeriod(key), [key, tick]);
  const set = (k: string) => {
    setKey(k);
    try {
      localStorage.setItem("period", k);
    } catch {
      /* ignore */
    }
  };
  return [period, set] as const;
}

export function PeriodPicker({ value, onChange }: { value: string; onChange: (k: string) => void }) {
  return (
    <div className="relative">
      <Calendar className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Select value={value} onChange={(e) => onChange(e.target.value)} className="w-44 pl-8">
        {presets.map((p) => (
          <option key={p.key} value={p.key}>
            {p.label}
          </option>
        ))}
      </Select>
    </div>
  );
}
