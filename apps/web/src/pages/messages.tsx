import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { useSearch } from "@tanstack/react-router";
import { CheckCircle2, Circle, CircleX, Download, Search, Send, Server, Users } from "lucide-react";
import { api, qs } from "@/lib/api";
import { dateTime, duration, money, rate } from "@/lib/format";
import { can, useMe } from "@/lib/session";
import type { Message } from "@/lib/types";
import { PageHeader } from "@/components/page";
import { PeriodPicker, usePeriod } from "@/components/period";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Input, Select } from "@/components/ui/input";
import { Empty, Skeleton } from "@/components/ui/misc";
import { Sheet } from "@/components/ui/sheet";
import { TBody, TD, TH, THead, TR, Table } from "@/components/ui/table";

const statuses = ["", "queued", "sent", "delivered", "undelivered", "rejected", "failed", "expired", "unknown", "received", "unrouted"];

function Timeline({ m }: { m: Message }) {
  const failed = ["failed", "rejected", "undelivered", "expired"].includes(m.status);
  const steps = [
    { icon: Users, label: "Received from client", at: m.created_at, done: true },
    { icon: Server, label: m.connection_name ? `Routed to ${m.connection_name}` : "Routed", at: m.created_at, done: !!m.connection_name },
    { icon: Send, label: "Accepted by vendor", at: m.sent_at, done: !!m.sent_at },
    { icon: failed ? CircleX : CheckCircle2, label: m.dlr_status ? `DLR: ${m.dlr_status}` : "Waiting for DLR", at: m.dlr_at, done: !!m.dlr_at, bad: failed },
    { icon: Send, label: "DLR sent to client", at: m.dlr_sent_at, done: !!m.dlr_sent_at },
  ];
  return (
    <ol className="relative space-y-5 border-l pl-6">
      {steps.map((s, i) => (
        <li key={i} className="relative">
          <span className={`absolute -left-[33px] grid size-6 place-items-center rounded-full border bg-card ${s.done ? (s.bad ? "text-danger" : "text-success") : "text-muted-foreground"}`}>
            {s.done ? <s.icon className="size-3.5" /> : <Circle className="size-3" />}
          </span>
          <p className={`text-sm font-medium ${s.done ? "" : "text-muted-foreground"}`}>{s.label}</p>
          <p className="text-xs text-muted-foreground">{s.done ? dateTime(s.at) : "—"}</p>
        </li>
      ))}
    </ol>
  );
}

function Detail({ id, onClose }: { id: string | null; onClose: () => void }) {
  const me = useMe();
  const q = useQuery({ queryKey: ["message", id], queryFn: () => api.get<Message>(`/api/messages/${id}`), enabled: !!id });
  const m = q.data;
  const row = (k: string, v: React.ReactNode) => (
    <div className="flex justify-between gap-4 py-2 text-sm">
      <span className="text-muted-foreground">{k}</span>
      <span className="text-right font-medium">{v}</span>
    </div>
  );
  return (
    <Sheet open={!!id} onOpenChange={(o) => !o && onClose()} title="Message details" description={id ?? undefined} wide>
      {!m ? (
        <Skeleton className="h-64" />
      ) : (
        <div className="grid gap-8 md:grid-cols-2">
          <div>
            <div className="divide-y">
              {row("Status", <StatusBadge status={m.status} />)}
              {row("To", <span className="font-mono">{m.destination}</span>)}
              {row("From", m.source || "—")}
              {row("Client", m.client_name)}
              {row("Country / network", [m.country_iso, m.network_name].filter(Boolean).join(" · ") || "—")}
              {can.staff(me) && row("Route", m.route_name ?? "—")}
              {can.staff(me) && row("Vendor connection", m.connection_name ?? "—")}
              {row("Parts", m.parts)}
              {row("Price", rate(m.price))}
              {can.staff(me) && row("Cost", rate(m.cost))}
              {can.staff(me) && row("Attempts", m.attempts)}
              {row("Time to DLR", duration(m.dlr_ms))}
              {m.client_ref && row("Client reference", m.client_ref)}
              {(m.error || (m.dlr_error && m.dlr_error !== "000" && m.status !== "delivered")) &&
                row("Error", <span className="text-danger">{m.error || `Network error code ${m.dlr_error}`}</span>)}
            </div>
            <p className="mt-4 text-xs font-medium uppercase tracking-wide text-muted-foreground">Text</p>
            <p className="mt-1 whitespace-pre-wrap rounded-md bg-muted p-3 text-sm">{m.body || "—"}</p>
          </div>
          <div>
            <p className="mb-4 text-xs font-medium uppercase tracking-wide text-muted-foreground">Timeline</p>
            <Timeline m={m} />
            {m.ledger && m.ledger.length > 0 && (
              <>
                <p className="mb-2 mt-8 text-xs font-medium uppercase tracking-wide text-muted-foreground">Billing</p>
                {m.ledger.map((l) => (
                  <div key={l.id} className="flex justify-between text-sm">
                    <span className="capitalize">{l.kind}</span>
                    <span className={`tabular ${l.amount < 0 ? "" : "text-success"}`}>{money(l.amount, 4)}</span>
                  </div>
                ))}
              </>
            )}
          </div>
        </div>
      )}
    </Sheet>
  );
}

export function MessagesPage() {
  const search = useSearch({ strict: false }) as { q?: string };
  const [q, setQ] = React.useState(search.q ?? "");
  const [term, setTerm] = React.useState(search.q ?? "");
  const [status, setStatus] = React.useState("");
  const [direction, setDirection] = React.useState("");
  const [period, setPeriod] = usePeriod("7d");
  const [open, setOpen] = React.useState<string | null>(null);
  React.useEffect(() => {
    const id = setTimeout(() => setTerm(q), 350);
    return () => clearTimeout(id);
  }, [q]);
  React.useEffect(() => {
    if (search.q) {
      setQ(search.q);
      setTerm(search.q);
    }
  }, [search.q]);

  const list = useQuery({
    queryKey: ["messages", term, status, direction, period.from, period.to],
    queryFn: () => api.get<Message[]>(`/api/messages${qs({ q: term, status, direction, from: period.from, to: period.to, limit: 200 })}`),
    refetchInterval: 15_000,
  });

  return (
    <>
      <PageHeader
        title="Messages"
        description="Search any message by number, sender, text or message ID"
        actions={
          <>
            <PeriodPicker value={period.key} onChange={setPeriod} />
            {(["xlsx", "csv"] as const).map((format) => (
              <Button key={format} variant="outline" asChild>
                <a href={`/api/messages${qs({ q: term, status, direction, from: period.from, to: period.to, format })}`} title="Download messages with their DLRs (up to 100,000)">
                  <Download /> {format === "xlsx" ? "Excel" : "CSV"}
                </a>
              </Button>
            ))}
          </>
        }
      />
      <Card>
        <div className="flex flex-wrap items-center gap-3 border-b p-3">
          <div className="relative min-w-64 flex-1">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Number, sender, text or message ID" className="pl-8" />
          </div>
          <Select value={direction} onChange={(e) => setDirection(e.target.value)} className="w-40">
            <option value="">All directions</option>
            <option value="mt">Outgoing</option>
            <option value="mo">Incoming (MO)</option>
          </Select>
          <Select value={status} onChange={(e) => setStatus(e.target.value)} className="w-40">
            {statuses.map((s) => (
              <option key={s} value={s}>
                {s ? s[0].toUpperCase() + s.slice(1) : "All statuses"}
              </option>
            ))}
          </Select>
        </div>
        {list.isLoading ? (
          <div className="space-y-2 p-4">{Array.from({ length: 10 }).map((_, i) => <Skeleton key={i} className="h-9" />)}</div>
        ) : !list.data?.length ? (
          <Empty title="No messages found">Try a wider period or a different search.</Empty>
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>Time</TH>
                <TH>To</TH>
                <TH>From</TH>
                <TH>Client</TH>
                <TH>Text</TH>
                <TH>Status</TH>
                <TH className="text-right">Price</TH>
                <TH className="text-right">DLR time</TH>
              </tr>
            </THead>
            <TBody>
              {list.data.map((m) => (
                <TR key={m.id} className="cursor-pointer" onClick={() => setOpen(m.id)}>
                  <TD className="whitespace-nowrap text-muted-foreground tabular">{dateTime(m.created_at)}</TD>
                  <TD className="font-mono text-xs">{m.destination}</TD>
                  <TD>{m.source}</TD>
                  <TD className="max-w-40 truncate">{m.client_name}</TD>
                  <TD className="max-w-72 truncate text-muted-foreground">{m.body}</TD>
                  <TD>
                    <StatusBadge status={m.status} />
                  </TD>
                  <TD className="text-right tabular">{rate(m.price)}</TD>
                  <TD className="text-right tabular text-muted-foreground">{duration(m.dlr_ms)}</TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </Card>
      <Detail id={open} onClose={() => setOpen(null)} />
    </>
  );
}
