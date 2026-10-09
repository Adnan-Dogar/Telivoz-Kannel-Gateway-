import * as React from "react";
import { Download } from "lucide-react";
import { qs } from "@/lib/api";
import { duration, money, num } from "@/lib/format";
import { can, useMe } from "@/lib/session";
import type { BreakdownRow } from "@/lib/types";
import { baseOption, Chart, useChartTheme } from "@/components/chart";
import { PageHeader } from "@/components/page";
import { PeriodPicker, usePeriod } from "@/components/period";
import { StatCard } from "@/components/stat";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Empty, Skeleton, Tabs } from "@/components/ui/misc";
import { TBody, TD, TH, THead, TR, Table } from "@/components/ui/table";
import { TrafficChart, useBreakdown, useOverview } from "./dashboard";

const dims = [
  { value: "client", label: "Clients", staff: true },
  { value: "vendor", label: "Vendors", staff: true },
  { value: "connection", label: "Connections", staff: true },
  { value: "country", label: "Countries", staff: false },
  { value: "network", label: "Networks", staff: false },
  { value: "owner", label: "Account managers", staff: true },
];

type SortKey = keyof Pick<BreakdownRow, "submitted" | "delivered" | "dlr_rate" | "revenue" | "cost" | "margin" | "avg_dlr_ms">;

export function AnalyticsPage() {
  const me = useMe();
  const staff = can.staff(me);
  const available = dims.filter((d) => staff || !d.staff);
  const [dim, setDim] = React.useState(available[0].value);
  const [sort, setSort] = React.useState<SortKey>("submitted");
  const [period, setPeriod] = usePeriod("7d");
  const ov = useOverview(period.from, period.to);
  const bd = useBreakdown(dim, period.from, period.to);
  const t = useChartTheme();
  const rows = [...(bd.data?.rows ?? [])].sort((a, b) => (b[sort] as number) - (a[sort] as number));
  const top = rows.slice(0, 12);
  const tot = ov.data?.totals;
  const finals = (tot?.delivered ?? 0) + (tot?.undelivered ?? 0);

  const SortTH = ({ k, children }: { k: SortKey; children: React.ReactNode }) => (
    <TH className="cursor-pointer select-none text-right hover:text-foreground" onClick={() => setSort(k)}>
      {children} {sort === k ? "↓" : ""}
    </TH>
  );

  return (
    <>
      <PageHeader
        title="Analytics"
        description="Volumes, delivery and profitability by dimension"
        actions={
          <>
            <PeriodPicker value={period.key} onChange={setPeriod} />
            <Button variant="outline" asChild>
              <a href={`/api/stats/breakdown${qs({ dim, from: period.from, to: period.to, format: "csv" })}`}>
                <Download /> Export CSV
              </a>
            </Button>
          </>
        }
      />
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard loading={ov.isLoading} label="Messages" value={num(tot?.submitted)} sub={`${num(tot?.sent)} sent to network`} />
        <StatCard loading={ov.isLoading} label="Delivery rate" value={finals ? `${((tot!.delivered / finals) * 100).toFixed(1)}%` : "—"} sub={`${num(tot?.delivered)} delivered · ${num(tot?.undelivered)} undelivered`} />
        <StatCard loading={ov.isLoading} label={staff ? "Revenue / cost" : "Spend"} value={money(tot?.revenue)} sub={staff ? `cost ${money(tot?.cost)}` : undefined} />
        <StatCard loading={ov.isLoading} label="Avg time to DLR" value={duration(tot?.avg_dlr_ms)} />
      </div>
      <Card className="mt-4">
        <CardHeader title="Traffic trend" />
        <CardBody>{ov.data?.series.length ? <TrafficChart data={ov.data.series} bucket={ov.data.bucket} height={260} /> : <Empty title="No traffic in this period" />}</CardBody>
      </Card>
      <div className="mt-6 mb-3">
        <Tabs value={dim} onValueChange={setDim} items={available} />
      </div>
      <div className="grid gap-4 xl:grid-cols-5">
        <Card className="xl:col-span-2">
          <CardHeader title={`Top ${available.find((d) => d.value === dim)?.label.toLowerCase()}`} description={`By ${sort.replace("_", " ")}`} />
          <CardBody>
            {bd.isLoading ? (
              <Skeleton className="h-[360px]" />
            ) : top.length === 0 ? (
              <Empty title="No data" />
            ) : (
              <Chart
                height={Math.max(220, top.length * 30)}
                option={{
                  ...baseOption(t),
                  tooltip: { ...baseOption(t).tooltip, trigger: "axis", axisPointer: { type: "shadow" } },
                  grid: { left: 8, right: 16, top: 24, bottom: 8, containLabel: true },
                  xAxis: { type: "value", splitLine: { lineStyle: { color: t.grid, type: "dashed" } }, axisLabel: { color: t.text } },
                  yAxis: { type: "category", inverse: true, data: top.map((r) => (r.label.length > 22 ? r.label.slice(0, 21) + "…" : r.label)), axisLabel: { color: t.fg } },
                  series: staff
                    ? [
                        { name: "Delivered", type: "bar", stack: "v", data: top.map((r) => r.delivered), itemStyle: { color: t.success }, barMaxWidth: 18 },
                        { name: "Undelivered", type: "bar", stack: "v", data: top.map((r) => r.undelivered), itemStyle: { color: t.warning } },
                        { name: "Failed", type: "bar", stack: "v", data: top.map((r) => r.failed), itemStyle: { color: t.danger, borderRadius: [0, 4, 4, 0] } },
                      ]
                    : [{ name: "Messages", type: "bar", data: top.map((r) => r.submitted), itemStyle: { color: t.primary, borderRadius: [0, 4, 4, 0] }, barMaxWidth: 18 }],
                }}
              />
            )}
          </CardBody>
        </Card>
        <Card className="xl:col-span-3">
          <CardHeader title="Breakdown" description="Click a column to sort" />
          {bd.isLoading ? (
            <div className="space-y-2 p-4">{Array.from({ length: 8 }).map((_, i) => <Skeleton key={i} className="h-8" />)}</div>
          ) : rows.length === 0 ? (
            <Empty title="No data for this period" />
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Name</TH>
                  <SortTH k="submitted">Messages</SortTH>
                  <SortTH k="delivered">Delivered</SortTH>
                  <SortTH k="dlr_rate">DLR %</SortTH>
                  <SortTH k="revenue">{staff ? "Revenue" : "Spend"}</SortTH>
                  {staff && <SortTH k="cost">Cost</SortTH>}
                  {staff && <SortTH k="margin">Margin</SortTH>}
                  <SortTH k="avg_dlr_ms">DLR time</SortTH>
                </tr>
              </THead>
              <TBody>
                {rows.map((r) => (
                  <TR key={r.key}>
                    <TD className="max-w-56 truncate font-medium">{r.label}</TD>
                    <TD className="text-right tabular">{num(r.submitted || r.sent)}</TD>
                    <TD className="text-right tabular">{num(r.delivered)}</TD>
                    <TD className="text-right">
                      <div className="ml-auto flex w-28 items-center gap-2">
                        <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                          <div className={`h-full rounded-full ${r.dlr_rate >= 0.8 ? "bg-success" : r.dlr_rate >= 0.5 ? "bg-warning" : "bg-danger"}`} style={{ width: `${r.dlr_rate * 100}%` }} />
                        </div>
                        <span className="w-12 text-right tabular text-xs">{(r.dlr_rate * 100).toFixed(1)}%</span>
                      </div>
                    </TD>
                    <TD className="text-right tabular">{money(r.revenue)}</TD>
                    {staff && <TD className="text-right tabular text-muted-foreground">{money(r.cost)}</TD>}
                    {staff && <TD className={`text-right tabular font-medium ${r.margin < 0 ? "text-danger" : "text-success"}`}>{money(r.margin)}</TD>}
                    <TD className="text-right tabular text-muted-foreground">{duration(r.avg_dlr_ms)}</TD>
                  </TR>
                ))}
              </TBody>
            </Table>
          )}
        </Card>
      </div>
    </>
  );
}
