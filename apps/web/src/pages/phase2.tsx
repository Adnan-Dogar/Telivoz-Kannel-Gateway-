import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearch } from "@tanstack/react-router";
import { Printer, Upload } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import { date, money, num } from "@/lib/format";
import { can, useLookups, useMe } from "@/lib/session";
import { ResourcePage } from "@/components/resource";
import { PageHeader } from "@/components/page";
import { Card } from "@/components/ui/card";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, Select, Textarea } from "@/components/ui/input";
import { Dialog } from "@/components/ui/sheet";
import { Empty, Skeleton } from "@/components/ui/misc";
import { pickSheet, SHEET_TYPES } from "@/lib/sheet";
import { TBody, TD, TH, THead, TR, Table } from "@/components/ui/table";

function ImportBlacklist() {
  const me = useMe();
  const qc = useQueryClient();
  const { data: l } = useLookups();
  const [open, setOpen] = React.useState(false);
  const [client, setClient] = React.useState(me.client_id ? String(me.client_id) : "");
  const [text, setText] = React.useState("");
  const [file, setFile] = React.useState<File | null>(null);
  const [reason, setReason] = React.useState("");
  const run = useMutation({
    mutationFn: () => {
      const path = `/api/blacklist/import${qs({ client_id: client, reason })}`;
      return file ? api.postFile<{ added: number; valid: number }>(path, file) : api.postText<{ added: number; valid: number }>(path, text);
    },
    onSuccess: (r) => {
      toast.success(`${num(r.added)} numbers added (${num(r.valid - r.added)} already listed)`);
      setOpen(false);
      setText("");
      setFile(null);
      qc.invalidateQueries({ queryKey: ["blacklist"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });
  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}><Upload /> Import list</Button>
      <Dialog open={open} onOpenChange={setOpen} title="Import numbers" description="One number per line, separated by commas, or an Excel file. Invalid and duplicate numbers are skipped."
        footer={<><Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button><Button onClick={() => run.mutate()} disabled={(!text && !file) || run.isPending}>Import</Button></>}>
        <div className="grid gap-4">
          {can.staff(me) && (
            <Field label="For client" hint={me.role === "admin" ? "Leave empty to block for every client" : undefined}>
              <Select value={client} onChange={(e) => setClient(e.target.value)}>
                <option value="">{me.role === "admin" ? "All clients (global)" : "Choose…"}</option>
                {l?.clients.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </Select>
            </Field>
          )}
          <Field label="Reason"><Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Customer request, DNC list…" /></Field>
          <Field label="Numbers">
            {file ? (
              <div className="flex items-center justify-between rounded-md border px-3 py-2 text-sm">
                <span className="truncate">{file.name}</span>
                <Button variant="ghost" size="sm" onClick={() => setFile(null)}>Remove</Button>
              </div>
            ) : (
              <Textarea rows={8} className="font-mono text-xs" value={text} onChange={(e) => setText(e.target.value)} placeholder={"923001234567\n923451234567"} />
            )}
            <input type="file" accept={SHEET_TYPES} className="text-xs" onChange={(e) => pickSheet(e.target.files?.[0], setText, setFile)} />
          </Field>
        </div>
      </Dialog>
    </>
  );
}

export function BlacklistPage() {
  const me = useMe();
  return (
    <ResourcePage
      title="Blacklist"
      description="Numbers that never receive messages. Subscribers who reply STOP are added automatically."
      path="blacklist"
      noun="Number"
      canWrite
      search="Search number or reason…"
      headerActions={<ImportBlacklist />}
      columns={[
        { key: "number", header: "Number", render: (r) => <span className="font-mono">{r.number}</span> },
        ...(can.staff(me) ? [{ key: "client_name", header: "Applies to", render: (r: Record<string, unknown>) => (r.client_name ? String(r.client_name) : <Badge tone="danger">All clients</Badge>) }] : []),
        { key: "reason", header: "Reason", render: (r) => r.reason || <span className="text-muted-foreground">—</span> },
        { key: "created_at", header: "Added", render: (r) => <span className="text-muted-foreground">{date(r.created_at)}</span> },
      ]}
      fields={[
        { name: "number", label: "Number", required: true, placeholder: "923001234567" },
        ...(can.staff(me) ? [{ name: "client_id", label: me.role === "admin" ? "For client (empty = all clients)" : "For client", type: "select" as const, options: (l: { clients: { id: number; name: string }[] }) => l.clients.map((c) => ({ value: String(c.id), label: c.name })) }] : []),
        { name: "reason", label: "Reason" },
      ]}
    />
  );
}

export function MORoutesPage() {
  const me = useMe();
  return (
    <ResourcePage
      title="Incoming routes"
      description="Where replies and incoming messages (MO) go. A keyword route wins over a number-only route."
      path="mo-routes"
      noun="Incoming route"
      canWrite={can.manage(me)}
      columns={[
        { key: "name", header: "Route", render: (r) => <span className="font-medium">{r.name}</span> },
        { key: "number_prefix", header: "Number", render: (r) => (r.number_prefix ? <span className="font-mono">{r.number_prefix}</span> : <span className="text-muted-foreground">any</span>) },
        { key: "keyword", header: "Keyword", render: (r) => (r.keyword ? <Badge tone="primary">{r.keyword}</Badge> : <span className="text-muted-foreground">any</span>) },
        { key: "client_name", header: "Client" },
        { key: "account_name", header: "Delivered to", render: (r) => (r.account_name ? <span>SMPP · {r.account_name}</span> : <span className="text-muted-foreground">MO webhook</span>) },
        { key: "auto_opt_out", header: "STOP opt-out", render: (r) => (r.auto_opt_out ? <Badge tone="success">on</Badge> : <Badge>off</Badge>) },
        { key: "status", header: "Status", render: (r) => <StatusBadge status={r.status} /> },
      ]}
      fields={[
        { name: "name", label: "Name", required: true, span: 1 },
        { name: "priority", label: "Priority", type: "number", default: 100, span: 1 },
        { name: "number_prefix", label: "Short code / number", span: 1, placeholder: "8899", hint: "Prefix of the number subscribers write to" },
        { name: "keyword", label: "Keyword", span: 1, placeholder: "PROMO", hint: "First word of the message (optional)" },
        { name: "client_id", label: "Client", type: "select", required: true, options: (l) => l.clients.map((c) => ({ value: String(c.id), label: c.name })) },
        { name: "account_id", label: "Deliver to SMPP account", type: "select", hint: "Empty sends it to the client's MO webhook instead", options: (l, v) => l.accounts.filter((a) => a.kind === "smpp" && (!v.client_id || String(a.client_id) === String(v.client_id))).map((a) => ({ value: String(a.id), label: a.username })) },
        { name: "auto_opt_out", label: "Blacklist senders who reply STOP", type: "switch", default: true },
        { name: "status", label: "Status", type: "select", required: true, default: "active", options: [{ value: "active", label: "Active" }, { value: "disabled", label: "Disabled" }] },
      ]}
    />
  );
}

type Statement = {
  number: string;
  period_start: string;
  period_end: string;
  issued_at: string;
  client: { id: number; name: string; email: string; phone: string; country_iso: string; currency: string; billing_type: string };
  opening_balance: number;
  closing_balance: number;
  charged: number;
  payments: { date: string; kind: string; amount: number; note: string }[];
  usage: { country: string; messages: number; parts: number; delivered: number; amount: number; unit_price: number | null }[];
};

function lastMonth() {
  const d = new Date();
  d.setUTCDate(1);
  d.setUTCMonth(d.getUTCMonth() - 1);
  return d.toISOString().slice(0, 7);
}

export function StatementPage() {
  const me = useMe();
  const { data: l } = useLookups();
  const search = useSearch({ strict: false }) as { client?: string; month?: string };
  const [client, setClient] = React.useState(search.client ?? (me.client_id ? String(me.client_id) : ""));
  const [month, setMonth] = React.useState(search.month ?? lastMonth());
  const st = useQuery({
    queryKey: ["statement", client, month],
    queryFn: () => api.get<Statement>(`/api/clients/${client}/statement?month=${month}`),
    enabled: !!client,
  });
  const s = st.data;
  const cur = s?.client.currency ?? "";
  return (
    <>
      <div className="print:hidden">
        <PageHeader
          title="Statements"
          description="Monthly statement / invoice per client"
          actions={
            <>
              {can.staff(me) && (
                <Select value={client} onChange={(e) => setClient(e.target.value)} className="w-56">
                  <option value="">Choose client…</option>
                  {l?.clients.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
                </Select>
              )}
              <Input type="month" value={month} onChange={(e) => setMonth(e.target.value)} className="w-44" />
              <Button onClick={() => window.print()} disabled={!s}><Printer /> Print / PDF</Button>
            </>
          }
        />
      </div>
      {!client ? <Empty title="Choose a client" /> : st.isLoading ? <Skeleton className="h-96" /> : !s ? <Empty title="No statement" /> : (
        <Card className="mx-auto max-w-4xl p-8 print:border-0 print:shadow-none">
          <div className="flex flex-wrap items-start justify-between gap-6 border-b pb-6">
            <div>
              <p className="text-2xl font-semibold">Statement</p>
              <p className="mt-1 text-sm text-muted-foreground">{s.number}</p>
            </div>
            <div className="text-right text-sm">
              <p className="font-semibold">Telivoz</p>
              <p className="text-muted-foreground">Issued {date(s.issued_at)}</p>
            </div>
          </div>
          <div className="grid gap-6 border-b py-6 sm:grid-cols-2">
            <div>
              <p className="text-xs uppercase tracking-wide text-muted-foreground">Bill to</p>
              <p className="mt-1 font-semibold">{s.client.name}</p>
              <p className="text-sm text-muted-foreground">{[s.client.email, s.client.phone].filter(Boolean).join(" · ")}</p>
            </div>
            <div className="sm:text-right">
              <p className="text-xs uppercase tracking-wide text-muted-foreground">Period</p>
              <p className="mt-1 font-medium">{date(s.period_start)} – {date(new Date(new Date(s.period_end).getTime() - 1).toISOString())}</p>
              <p className="text-sm capitalize text-muted-foreground">{s.client.billing_type} · {cur}</p>
            </div>
          </div>
          <p className="mt-6 text-sm font-semibold">Usage</p>
          {s.usage.length === 0 ? <p className="mt-2 text-sm text-muted-foreground">No messages in this period.</p> : (
            <Table className="mt-2">
              <THead><tr><TH>Destination</TH><TH className="text-right">Messages</TH><TH className="text-right">Parts</TH><TH className="text-right">Delivered</TH><TH className="text-right">Unit price</TH><TH className="text-right">Amount</TH></tr></THead>
              <TBody>
                {s.usage.map((u) => (
                  <TR key={u.country}>
                    <TD>{u.country}</TD>
                    <TD className="text-right tabular">{num(u.messages)}</TD>
                    <TD className="text-right tabular">{num(u.parts)}</TD>
                    <TD className="text-right tabular">{num(u.delivered)}</TD>
                    <TD className="text-right tabular">{u.unit_price != null ? money(u.unit_price, 4) : "—"}</TD>
                    <TD className="text-right tabular">{money(u.amount)}</TD>
                  </TR>
                ))}
              </TBody>
            </Table>
          )}
          {s.payments.length > 0 && (
            <>
              <p className="mt-8 text-sm font-semibold">Payments and adjustments</p>
              <Table className="mt-2">
                <THead><tr><TH>Date</TH><TH>Type</TH><TH>Note</TH><TH className="text-right">Amount</TH></tr></THead>
                <TBody>
                  {s.payments.map((p, i) => (
                    <TR key={i}><TD>{date(p.date)}</TD><TD className="capitalize">{p.kind}</TD><TD className="text-muted-foreground">{p.note}</TD><TD className="text-right tabular">{money(p.amount)}</TD></TR>
                  ))}
                </TBody>
              </Table>
            </>
          )}
          <div className="ml-auto mt-8 w-full max-w-xs space-y-2 text-sm">
            <div className="flex justify-between"><span className="text-muted-foreground">Opening balance</span><span className="tabular">{money(s.opening_balance)} {cur}</span></div>
            <div className="flex justify-between"><span className="text-muted-foreground">Messages charged</span><span className="tabular">{money(s.charged)} {cur}</span></div>
            <div className="flex justify-between border-t pt-2 text-base font-semibold"><span>Closing balance</span><span className="tabular">{money(s.closing_balance)} {cur}</span></div>
          </div>
        </Card>
      )}
    </>
  );
}
