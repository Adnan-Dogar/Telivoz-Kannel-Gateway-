export const num = (n: number | undefined | null) => (n ?? 0).toLocaleString("en-US");

export function compact(n: number | undefined | null) {
  const v = n ?? 0;
  if (Math.abs(v) >= 1e9) return `${(v / 1e9).toFixed(1)}B`;
  if (Math.abs(v) >= 1e6) return `${(v / 1e6).toFixed(1)}M`;
  if (Math.abs(v) >= 1e4) return `${(v / 1e3).toFixed(1)}K`;
  return num(v);
}

export const money = (n: number | undefined | null, digits = 2) =>
  (n ?? 0).toLocaleString("en-US", { minimumFractionDigits: digits, maximumFractionDigits: digits });

export const pct = (part: number, whole: number) => (whole > 0 ? `${((part / whole) * 100).toFixed(1)}%` : "–");

export function duration(ms: number | null | undefined) {
  if (!ms) return "–";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.round(ms / 60_000)} min`;
}

export function ago(iso: string) {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return `${Math.round(s)}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return new Date(iso).toLocaleDateString("en-GB", { day: "2-digit", month: "short" });
}

/** Change against the previous period, e.g. "+12.5%". */
export function delta(now: number, before: number) {
  if (!before) return null;
  const d = ((now - before) / before) * 100;
  return { text: `${d >= 0 ? "+" : ""}${d.toFixed(1)}%`, up: d >= 0 };
}
