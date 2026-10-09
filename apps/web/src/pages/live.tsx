import { useMutation, useQuery } from "@tanstack/react-query";
import { RotateCw } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ago, num } from "@/lib/format";
import { can, useMe } from "@/lib/session";
import type { Live } from "@/lib/types";
import { baseOption, Chart, useChartTheme } from "@/components/chart";
import { PageHeader } from "@/components/page";
import { StatCard } from "@/components/stat";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { Badge, Dot } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, Tooltip } from "@/components/ui/misc";
import { TBody, TD, TH, THead, TR, Table } from "@/components/ui/table";

export function LivePage() {
  const me = useMe();
  const t = useChartTheme();
  const live = useQuery({ queryKey: ["live"], queryFn: () => api.get<Live>("/api/stats/live"), refetchInterval: 2000 });
  const restart = useMutation({
    mutationFn: (id: number) => api.post(`/api/connections/${id}/restart`),
    onSuccess: () => toast.success("Connection restarting"),
    onError: (e: Error) => toast.error(e.message),
  });
  const s = live.data?.series ?? [];
  const avg = (k: "in" | "out" | "dlr") => {
    const last = s.slice(-10);
    return last.length ? last.reduce((a, p) => a + p[k], 0) / last.length : 0;
  };
  const peak = Math.max(0, ...s.map((p) => p.in));
  const conns = Object.values(live.data?.connections ?? {}).sort((a, b) => a.name.localeCompare(b.name));
  const binds = live.data?.client_binds ?? [];

  return (
    <>
      <PageHeader
        title="Live traffic"
        description="Updates every 2 seconds"
        actions={
          <Badge tone="success" className="px-3 py-1">
            <Dot tone="success" pulse /> Live
          </Badge>
        }
      />
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
        <StatCard label="TPS in" value={avg("in").toFixed(1)} sub="10 second average" />
        <StatCard label="TPS out" value={avg("out").toFixed(1)} sub="sent to vendors" />
        <StatCard label="DLRs / second" value={avg("dlr").toFixed(1)} />
        <StatCard label="Peak TPS (2 min)" value={num(peak)} />
        <StatCard label="Queued" value={num(live.data?.queue ?? 0)} sub={`${num(live.data?.dlr_outbox ?? 0)} DLRs waiting for clients`} />
      </div>
      <Card className="mt-4">
        <CardHeader title="Messages per second" description="Last 2 minutes" />
        <CardBody>
          <Chart
            height={300}
            option={{
              ...baseOption(t),
              xAxis: {
                type: "category",
                data: s.map((p) => new Date(p.t * 1000).toLocaleTimeString("en-GB")),
                axisLabel: { color: t.text },
                axisLine: { lineStyle: { color: t.grid } },
              },
              yAxis: { type: "value", splitLine: { lineStyle: { color: t.grid, type: "dashed" } }, axisLabel: { color: t.text } },
              series: [
                { name: "In", type: "line", smooth: true, symbol: "none", data: s.map((p) => p.in), areaStyle: { opacity: 0.12 }, lineStyle: { width: 2 } },
                { name: "Out", type: "line", smooth: true, symbol: "none", data: s.map((p) => p.out), lineStyle: { width: 2 } },
                { name: "DLR", type: "line", smooth: true, symbol: "none", data: s.map((p) => p.dlr), lineStyle: { width: 1.5, type: "dashed" } },
              ],
            }}
          />
        </CardBody>
      </Card>

      <div className="mt-4 grid gap-4 xl:grid-cols-5">
        <Card className="xl:col-span-3">
          <CardHeader title="Vendor connections" description="Each bind is reconnected automatically; restart drops and re-creates the binds of one connection only." />
          {conns.length === 0 ? (
            <Empty title="No enabled connections">Enable a vendor connection to start sending.</Empty>
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Connection</TH>
                  <TH>Binds</TH>
                  <TH className="text-right">Sent</TH>
                  <TH className="text-right">Errors</TH>
                  <TH className="text-right">In flight</TH>
                  <TH />
                </tr>
              </THead>
              <TBody>
                {conns.map((c) => (
                  <TR key={c.id}>
                    <TD className="font-medium">{c.name}</TD>
                    <TD>
                      <div className="flex flex-wrap gap-1.5">
                        {c.binds.map((b) => (
                          <Tooltip key={b.index} content={`Bind ${b.index + 1}: ${b.state} since ${ago(b.since)}${b.error ? ` — ${b.error}` : ""}`}>
                            <span className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs">
                              <Dot tone={b.state === "bound" ? "success" : b.state === "connecting" ? "warning" : "danger"} pulse={b.state !== "bound"} />
                              {b.index + 1}
                            </span>
                          </Tooltip>
                        ))}
                      </div>
                    </TD>
                    <TD className="text-right tabular">{num(c.sent)}</TD>
                    <TD className={`text-right tabular ${c.errors ? "text-danger" : ""}`}>{num(c.errors)}</TD>
                    <TD className="text-right tabular">{num(c.in_flight)}</TD>
                    <TD className="text-right">
                      {can.manage(me) && (
                        <Button variant="ghost" size="sm" onClick={() => restart.mutate(c.id)} disabled={restart.isPending}>
                          <RotateCw /> Restart
                        </Button>
                      )}
                    </TD>
                  </TR>
                ))}
              </TBody>
            </Table>
          )}
        </Card>
        <Card className="xl:col-span-2">
          <CardHeader title="Client binds" description={`${binds.length} SMPP sessions`} />
          {binds.length === 0 ? (
            <Empty title="No clients bound" />
          ) : (
            <Table>
              <THead>
                <tr>
                  <TH>Account</TH>
                  <TH>Mode</TH>
                  <TH>Remote</TH>
                </tr>
              </THead>
              <TBody>
                {binds.map((b, i) => (
                  <TR key={i}>
                    <TD className="font-medium">{b.system_id}</TD>
                    <TD>
                      <Badge tone="primary">{b.mode.toUpperCase()}</Badge>
                    </TD>
                    <TD className="font-mono text-xs text-muted-foreground">{b.remote}</TD>
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
