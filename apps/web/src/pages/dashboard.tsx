import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, CheckCircle2, CircleDollarSign, Gauge, MessagesSquare, Timer } from "lucide-react";
import { api, qs } from "@/lib/api";
import { change, duration, money, num, pct, short } from "@/lib/format";
import { can, useMe } from "@/lib/session";
import type { BreakdownRow, Live, Overview } from "@/lib/types";
import { baseOption, Chart, useChartTheme } from "@/components/chart";
import { PageHeader } from "@/components/page";
import { PeriodPicker, usePeriod } from "@/components/period";
import { StatCard } from "@/components/stat";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { Dot } from "@/components/ui/badge";
import { Empty, Skeleton } from "@/components/ui/misc";

export function useOverview(from: string, to: string) {
  return useQuery({
    queryKey: ["overview", from, to],
    queryFn: () => api.get<Overview>(`/api/stats/overview${qs({ from, to })}`),
    refetchInterval: 30_000,
  });
}

export function useBreakdown(dim: string, from: string, to: string, enabled = true) {
  return useQuery({
    queryKey: ["breakdown", dim, from, to],
    queryFn: () => api.get<{ rows: BreakdownRow[] }>(`/api/stats/breakdown${qs({ dim, from, to })}`),
    enabled,
  });
}

export function TrafficChart({ data, bucket, height = 300 }: { data: Overview["series"]; bucket: string; height?: number }) {
  const t = useChartTheme();
  const fmt = (s: string) => {
    const d = new Date(s);
    if (bucket === "month") return d.toLocaleDateString("en-GB", { month: "short", year: "numeric" });
    return bucket === "day" ? d.toLocaleDateString("en-GB", { day: "2-digit", month: "short" }) : d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
  };
  return (
    <Chart
      height={height}
      option={{
        ...baseOption(t),
        xAxis: { type: "category", data: data.map((p) => fmt(p.t)), axisLine: { lineStyle: { color: t.grid } }, axisLabel: { color: t.text } },
        yAxis: { type: "value", splitLine: { lineStyle: { color: t.grid, type: "dashed" } }, axisLabel: { color: t.text, formatter: (v: number) => short(v) } },
        series: [
          { name: "Delivered", type: "bar", stack: "s", data: data.map((p) => p.delivered), itemStyle: { color: t.success, borderRadius: 0 }, barMaxWidth: 28 },
          { name: "Undelivered", type: "bar", stack: "s", data: data.map((p) => p.undelivered), itemStyle: { color: t.warning } },
          { name: "Failed", type: "bar", stack: "s", data: data.map((p) => p.failed), itemStyle: { color: t.danger, borderRadius: [4, 4, 0, 0] } },
          { name: "Submitted", type: "line", smooth: true, symbol: "none", data: data.map((p) => p.submitted), lineStyle: { width: 2, color: t.primary }, itemStyle: { color: t.primary } },
        ],
      }}
    />
  );
}

function LiveMini() {
  const t = useChartTheme();
  const live = useQuery({ queryKey: ["live"], queryFn: () => api.get<Live>("/api/stats/live"), refetchInterval: 2000 });
  const s = live.data?.series ?? [];
  const last = s.slice(-10);
  const tps = last.length ? last.reduce((a, p) => a + p.in, 0) / last.length : 0;
  const conns = Object.values(live.data?.connections ?? {});
  const down = conns.flatMap((c) => c.binds).filter((b) => b.state !== "bound").length;
  return (
    <Card>
      <CardHeader
        title={
          <span className="flex items-center gap-2">
            <Dot tone="success" pulse /> Live traffic
          </span>
        }
        description="Messages per second, last 2 minutes"
        actions={<Link to="/live" className="text-sm font-medium text-primary hover:underline">Open</Link>}
      />
      <CardBody className="pb-2">
        <div className="flex items-end gap-6">
          <div>
            <p className="text-3xl font-semibold tabular">{tps.toFixed(1)}</p>
            <p className="text-xs text-muted-foreground">TPS in (10s avg)</p>
          </div>
          <div>
            <p className="text-lg font-semibold tabular">{num(live.data?.queue ?? 0)}</p>
            <p className="text-xs text-muted-foreground">queued</p>
          </div>
          <div>
            <p className={`text-lg font-semibold tabular ${down ? "text-danger" : "text-success"}`}>{down ? `${down} down` : "All up"}</p>
            <p className="text-xs text-muted-foreground">vendor binds</p>
          </div>
        </div>
        <Chart
          height={120}
          option={{
            ...baseOption(t),
            legend: { show: false },
            grid: { left: 0, right: 0, top: 10, bottom: 0 },
            xAxis: { type: "category", show: false, data: s.map((p) => p.t) },
            yAxis: { type: "value", show: false },
            series: [
              { type: "line", name: "In", smooth: true, symbol: "none", data: s.map((p) => p.in), areaStyle: { opacity: 0.15 }, lineStyle: { width: 2 } },
              { type: "line", name: "Out", smooth: true, symbol: "none", data: s.map((p) => p.out), lineStyle: { width: 1.5 } },
            ],
          }}
        />
      </CardBody>
    </Card>
  );
}

function TopList({ title, rows, loading, to }: { title: string; rows?: BreakdownRow[]; loading: boolean; to: string }) {
  const max = Math.max(1, ...(rows ?? []).map((r) => r.submitted || r.sent));
  return (
    <Card>
      <CardHeader title={title} actions={<Link to={to} className="text-sm font-medium text-primary hover:underline">Details</Link>} />
      <CardBody className="space-y-3">
        {loading ? (
          Array.from({ length: 5 }).map((_, i) => <Skeleton key={i} className="h-8" />)
        ) : !rows?.length ? (
          <Empty title="No traffic in this period" />
        ) : (
          rows.slice(0, 6).map((r) => {
            const vol = r.submitted || r.sent;
            return (
              <div key={r.key}>
                <div className="flex items-center justify-between gap-3 text-sm">
                  <span className="truncate font-medium">{r.label}</span>
                  <span className="shrink-0 tabular text-muted-foreground">
                    {short(vol)} · <span className={r.dlr_rate >= 0.8 ? "text-success" : r.dlr_rate >= 0.5 ? "text-warning" : "text-danger"}>{(r.dlr_rate * 100).toFixed(1)}%</span>
                  </span>
                </div>
                <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted">
                  <div className="h-full rounded-full bg-primary" style={{ width: `${(vol / max) * 100}%` }} />
                </div>
              </div>
            );
          })
        )}
      </CardBody>
    </Card>
  );
}

export function DashboardPage() {
  const me = useMe();
  const staff = can.staff(me);
  const [period, setPeriod] = usePeriod();
  const ov = useOverview(period.from, period.to);
  const clients = useBreakdown("client", period.from, period.to, staff);
  const vendors = useBreakdown("vendor", period.from, period.to, staff);
  const countries = useBreakdown("country", period.from, period.to);
  const t = ov.data?.totals;
  const p = ov.data?.previous;
  const finals = (t?.delivered ?? 0) + (t?.undelivered ?? 0);
  const prevFinals = (p?.delivered ?? 0) + (p?.undelivered ?? 0);
  const dlrRate = finals ? (t!.delivered / finals) * 100 : 0;
  const prevRate = prevFinals ? (p!.delivered / prevFinals) * 100 : 0;
  const margin = (t?.revenue ?? 0) - (t?.cost ?? 0);
  const prevMargin = (p?.revenue ?? 0) - (p?.cost ?? 0);

  return (
    <>
      <PageHeader
        title={me.client_name ? `Welcome, ${me.client_name}` : `Welcome back${me.name ? `, ${me.name.split(" ")[0]}` : ""}`}
        description={`Traffic overview · ${period.label.toLowerCase()}`}
        actions={<PeriodPicker value={period.key} onChange={setPeriod} />}
      />
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-6">
        <StatCard loading={ov.isLoading} label="Messages" icon={<MessagesSquare />} value={num(t?.submitted)} delta={change(t?.submitted ?? 0, p?.submitted ?? 0)} sub={`${num(t?.parts)} parts`} />
        <StatCard loading={ov.isLoading} label="Delivery rate" icon={<CheckCircle2 />} value={finals ? `${dlrRate.toFixed(1)}%` : "—"} delta={prevFinals ? dlrRate - prevRate : null} sub={`${num(t?.delivered)} delivered`} />
        <StatCard loading={ov.isLoading} label="Failed / rejected" icon={<AlertTriangle />} invert value={num((t?.failed ?? 0) + (t?.rejected ?? 0))} delta={change((t?.failed ?? 0) + (t?.rejected ?? 0), (p?.failed ?? 0) + (p?.rejected ?? 0))} sub={pct((t?.failed ?? 0) + (t?.rejected ?? 0), t?.submitted ?? 0)} />
        <StatCard loading={ov.isLoading} label={staff ? "Revenue" : "Spend"} icon={<CircleDollarSign />} value={money(t?.revenue)} delta={change(t?.revenue ?? 0, p?.revenue ?? 0)} />
        {staff ? (
          <StatCard loading={ov.isLoading} label="Margin" icon={<Gauge />} value={money(margin)} delta={change(margin, prevMargin)} sub={t?.revenue ? `${((margin / t.revenue) * 100).toFixed(1)}% of revenue` : undefined} />
        ) : (
          <StatCard loading={ov.isLoading} label="Sent to network" icon={<Gauge />} value={num(t?.sent)} />
        )}
        <StatCard loading={ov.isLoading} label="Avg DLR time" icon={<Timer />} invert value={duration(t?.avg_dlr_ms)} delta={change(t?.avg_dlr_ms ?? 0, p?.avg_dlr_ms ?? 0)} />
      </div>

      <div className="mt-4 grid gap-4 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader title="Traffic" description={`By ${ov.data?.bucket ?? "hour"} · delivered, undelivered and failed`} />
          <CardBody>{ov.isLoading ? <Skeleton className="h-[300px]" /> : ov.data?.series.length ? <TrafficChart data={ov.data.series} bucket={ov.data.bucket} /> : <Empty title="No traffic in this period" />}</CardBody>
        </Card>
        {staff ? <LiveMini /> : <TopList title="Top countries" rows={countries.data?.rows} loading={countries.isLoading} to="/analytics" />}
      </div>

      {staff && (
        <div className="mt-4 grid gap-4 lg:grid-cols-3">
          <TopList title="Top clients" rows={clients.data?.rows} loading={clients.isLoading} to="/analytics" />
          <TopList title="Top vendors" rows={vendors.data?.rows} loading={vendors.isLoading} to="/analytics" />
          <TopList title="Top countries" rows={countries.data?.rows} loading={countries.isLoading} to="/analytics" />
        </div>
      )}
    </>
  );
}
