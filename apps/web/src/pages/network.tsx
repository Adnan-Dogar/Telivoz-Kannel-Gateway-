import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, FlaskConical, RotateCw, Upload, X } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { date, rate } from "@/lib/format";
import { can, useLookups, useMe } from "@/lib/session";
import type { Live } from "@/lib/types";
import { ResourcePage, type Row } from "@/components/resource";
import { PageHeader } from "@/components/page";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { Badge, Dot, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, Select, Textarea } from "@/components/ui/input";
import { Dialog } from "@/components/ui/sheet";
import { Empty, Tabs } from "@/components/ui/misc";

const active = [{ value: "active", label: "Active" }, { value: "disabled", label: "Disabled" }];

export function VendorsPage() {
  const me = useMe();
  return (
    <ResourcePage
      title="Vendors"
      description="Carriers and aggregators you send traffic to"
      path="vendors"
      noun="Vendor"
      canWrite={can.manage(me)}
      columns={[
        { key: "name", header: "Vendor", render: (r) => <span className="font-medium">{r.name}</span> },
        { key: "email", header: "Email" },
        { key: "currency", header: "Currency" },
        { key: "status", header: "Status", render: (r) => <StatusBadge status={r.status} /> },
      ]}
      fields={[
        { name: "name", label: "Name", required: true },
        { name: "email", label: "Email", span: 1 },
        { name: "currency", label: "Currency", default: "USD", span: 1 },
        { name: "owner_id", label: "Account manager", type: "select", options: (l) => l.users.map((u) => ({ value: String(u.id), label: u.name })) },
        { name: "status", label: "Status", type: "select", required: true, default: "active", options: active },
      ]}
    />
  );
}

export function ConnectionsPage() {
  const me = useMe();
  const live = useQuery({ queryKey: ["live"], queryFn: () => api.get<Live>("/api/stats/live"), refetchInterval: 3000 });
  const restart = useMutation({
    mutationFn: (id: number) => api.post(`/api/connections/${id}/restart`),
    onSuccess: () => toast.success("Restarting binds"),
    onError: (e: Error) => toast.error(e.message),
  });
  const state = (id: number) => {
    const c = live.data?.connections?.[String(id)];
    if (!c) return null;
    const up = c.binds.filter((b) => b.state === "bound").length;
    return { up, total: c.binds.length };
  };
  return (
    <ResourcePage
      title="Connections"
      description="SMPP binds to vendors. Changes apply live: only the edited connection reconnects."
      path="connections"
      noun="Connection"
      canWrite={can.manage(me)}
      columns={[
        { key: "name", header: "Connection", render: (r) => <div><p className="font-medium">{r.name}</p><p className="text-xs text-muted-foreground">{r.vendor_name}</p></div> },
        { key: "host", header: "Host", render: (r) => <span className="font-mono text-xs">{r.host}:{r.port}</span> },
        { key: "bind_mode", header: "Mode", render: (r) => <Badge>{r.bind_mode.toUpperCase()} × {r.binds}</Badge> },
        { key: "tps", header: "TPS", className: "text-right" },
        {
          key: "live", header: "Live", render: (r) => {
            if (r.status !== "enabled") return <StatusBadge status="disabled" />;
            const s = state(r.id);
            if (!s) return <Badge tone="warning">starting</Badge>;
            return <span className="inline-flex items-center gap-1.5 text-sm"><Dot tone={s.up === s.total ? "success" : s.up ? "warning" : "danger"} pulse={s.up !== s.total} /> {s.up}/{s.total} bound</span>;
          },
        },
      ]}
      rowActions={(r) => r.status === "enabled" && can.manage(me) && <Button size="sm" variant="ghost" onClick={() => restart.mutate(r.id)}><RotateCw /> Restart</Button>}
      fields={[
        { name: "vendor_id", label: "Vendor", type: "select", required: true, options: (l) => l.vendors.map((v) => ({ value: String(v.id), label: v.name })) },
        { name: "name", label: "Connection name", required: true, span: 1 },
        { name: "status", label: "Status", type: "select", required: true, default: "enabled", span: 1, options: [{ value: "enabled", label: "Enabled" }, { value: "disabled", label: "Disabled" }] },
        { name: "host", label: "Host", required: true, span: 1 },
        { name: "port", label: "Port", type: "number", required: true, default: 2775, span: 1 },
        { name: "system_id", label: "System ID", required: true, span: 1 },
        { name: "password", label: "Password", type: "password", span: 1, hint: "Stored encrypted. Leave empty to keep." },
        { name: "system_type", label: "System type", span: 1 },
        { name: "bind_mode", label: "Bind mode", type: "select", required: true, default: "trx", span: 1, options: [{ value: "trx", label: "Transceiver" }, { value: "tx", label: "Transmitter" }, { value: "rx", label: "Receiver" }] },
        { name: "binds", label: "Binds", type: "number", default: 1, span: 1 },
        { name: "tps", label: "Max TPS", type: "number", default: 50, span: 1 },
        { name: "window_size", label: "Window (unacked submits)", type: "number", default: 10, span: 1 },
        { name: "max_attempts", label: "Max attempts", type: "number", default: 2, span: 1 },
        { name: "source_ton", label: "Source TON", type: "number", default: 5, span: 1 },
        { name: "source_npi", label: "Source NPI", type: "number", default: 0, span: 1 },
        { name: "dest_ton", label: "Destination TON", type: "number", default: 1, span: 1 },
        { name: "dest_npi", label: "Destination NPI", type: "number", default: 1, span: 1 },
        { name: "dlr_id_format", label: "DLR message ID format", type: "select", default: "auto", options: [{ value: "auto", label: "Auto-detect (hex/decimal)" }, { value: "same", label: "Same as submit response" }] },
        { name: "failover_on_dlr", label: "Resend via the next vendor on these DLRs", type: "tags", placeholder: "UNDELIV, REJECTD:001", hint: "DLR statuses, or STATUS:error-code pairs. The client is charged once and only sees the final DLR." },
      ]}
    />
  );
}

function ImportRates({ target, onDone }: { target: "client" | "connection"; onDone: () => void }) {
  const { data: l } = useLookups();
  const [open, setOpen] = React.useState(false);
  const [owner, setOwner] = React.useState("");
  const [csv, setCsv] = React.useState("");
  const owners = target === "client" ? (l?.clients ?? []).map((c) => ({ id: c.id, name: c.name })) : (l?.connections ?? []).map((c) => ({ id: c.id, name: c.name }));
  const run = useMutation({
    mutationFn: () => api.postText<{ imported: number; skipped: string[] }>(`/api/rates/import?target=${target}&id=${owner}`, csv),
    onSuccess: (r) => {
      toast.success(`${r.imported} rates imported${r.skipped.length ? `, ${r.skipped.length} skipped` : ""}`);
      if (r.skipped.length) toast.warning(r.skipped.slice(0, 5).join("\n"));
      setOpen(false);
      setCsv("");
      onDone();
    },
    onError: (e: Error) => toast.error(e.message),
  });
  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}><Upload /> Import CSV</Button>
      <Dialog open={open} onOpenChange={setOpen} title={`Import ${target === "client" ? "client" : "vendor"} rates`}
        description="Columns: country_iso, mcc, mnc, price, effective_from (optional, YYYY-MM-DD). Leave mcc/mnc empty for a country-wide rate."
        footer={<><Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button><Button onClick={() => run.mutate()} disabled={!owner || !csv || run.isPending}>Import</Button></>}>
        <div className="grid gap-4">
          <Field label={target === "client" ? "Client" : "Connection"}>
            <Select value={owner} onChange={(e) => setOwner(e.target.value)}>
              <option value="">Choose…</option>
              {owners.map((o) => <option key={o.id} value={o.id}>{o.name}</option>)}
            </Select>
          </Field>
          <Field label="CSV">
            <Textarea rows={8} className="font-mono text-xs" value={csv} onChange={(e) => setCsv(e.target.value)} placeholder={"PK,410,01,0.0085\nPK,,,0.0090,2026-11-01"} />
            <input type="file" accept=".csv,text/csv" className="text-xs" onChange={async (e) => { const f = e.target.files?.[0]; if (f) setCsv(await f.text()); }} />
          </Field>
        </div>
      </Dialog>
    </>
  );
}

export function RatesPage() {
  const me = useMe();
  const qc = useQueryClient();
  const [tab, setTab] = React.useState("client");
  const write = can.manage(me);
  const common = (owner: string) => [
    { key: owner, header: owner === "client_name" ? "Client" : "Connection", render: (r: Row) => <span className="font-medium">{r[owner]}</span> },
    { key: "country_iso", header: "Country", render: (r: Row) => <Badge>{r.country_iso}</Badge> },
    { key: "network_name", header: "Network", render: (r: Row) => r.network_name ?? <span className="text-muted-foreground">All networks</span> },
    { key: "price", header: "Price", className: "text-right", render: (r: Row) => <span className="tabular font-medium">{rate(r.price)}</span> },
    { key: "effective_from", header: "Effective from", render: (r: Row) => <span className={new Date(r.effective_from) > new Date() ? "text-warning" : "text-muted-foreground"}>{date(r.effective_from)}</span> },
  ];
  const netField = { name: "network_id", label: "Network (optional)", hint: "Network ID from Networks; empty = whole country", type: "number" as const };
  return (
    <>
      <PageHeader title="Rates" description="Client selling prices and vendor costs per country or network. The newest rate already in effect is used." />
      {can.staff(me) && <Tabs value={tab} onValueChange={setTab} className="mb-4" items={[{ value: "client", label: "Client rates" }, { value: "vendor", label: "Vendor rates" }]} />}
      {tab === "client" ? (
        <ResourcePage embedded title="Client rates" path="client-rates" noun="Client rate" canWrite={write}
          headerActions={write && <ImportRates target="client" onDone={() => qc.invalidateQueries({ queryKey: ["client-rates"] })} />}
          columns={can.staff(me) ? common("client_name") : common("client_name").slice(1)}
          fields={[
            { name: "client_id", label: "Client", type: "select", required: true, options: (l) => l.clients.map((c) => ({ value: String(c.id), label: c.name })) },
            { name: "country_iso", label: "Country", type: "select", required: true, span: 1, options: (l) => l.countries.map((c) => ({ value: c.iso, label: c.name })) },
            { ...netField, span: 1 },
            { name: "price", label: "Price per part", type: "decimal", required: true, span: 1 },
            { name: "effective_from", label: "Effective from", type: "date", span: 1, hint: "Empty = now. A future date schedules a rate change." },
          ]}
        />
      ) : (
        <ResourcePage embedded title="Vendor rates" path="vendor-rates" noun="Vendor rate" canWrite={write}
          headerActions={write && <ImportRates target="connection" onDone={() => qc.invalidateQueries({ queryKey: ["vendor-rates"] })} />}
          columns={common("connection_name")}
          fields={[
            { name: "connection_id", label: "Connection", type: "select", required: true, options: (l) => l.connections.map((c) => ({ value: String(c.id), label: c.name })) },
            { name: "country_iso", label: "Country", type: "select", required: true, span: 1, options: (l) => l.countries.map((c) => ({ value: c.iso, label: c.name })) },
            { ...netField, span: 1 },
            { name: "price", label: "Cost per part", type: "decimal", required: true, span: 1 },
            { name: "effective_from", label: "Effective from", type: "date", span: 1 },
          ]}
        />
      )}
    </>
  );
}

type Target = { connection_id: number; weight: number };

function TargetsEditor({ values, set }: { values: Row; set: (v: Row) => void }) {
  const { data: l } = useLookups();
  const targets: Target[] = values.targets ?? [];
  const update = (t: Target[]) => set({ ...values, targets: t });
  const name = (id: number) => l?.connections.find((c) => c.id === id)?.name ?? `#${id}`;
  const unused = (l?.connections ?? []).filter((c) => !targets.some((t) => t.connection_id === c.id));
  const policy = values.policy ?? "priority";
  return (
    <div className="rounded-lg border p-4">
      <p className="text-sm font-medium">Vendor connections</p>
      <p className="mb-3 text-xs text-muted-foreground">
        {policy === "priority" && "Tried in this order; the next one is used when a vendor rejects the message."}
        {policy === "weighted" && "Traffic is split by weight; the others remain as failover."}
        {policy === "lcr" && "Cheapest vendor rate first (connections without a rate for the destination are skipped)."}
      </p>
      <div className="space-y-2">
        {targets.map((t, i) => (
          <div key={t.connection_id} className="flex items-center gap-2 rounded-md bg-muted/60 px-3 py-2">
            <span className="w-5 text-xs text-muted-foreground tabular">{i + 1}.</span>
            <span className="flex-1 text-sm font-medium">{name(t.connection_id)}</span>
            {policy === "weighted" && (
              <Input type="number" className="h-8 w-20" value={t.weight} onChange={(e) => update(targets.map((x, j) => (j === i ? { ...x, weight: Number(e.target.value) } : x)))} title="Weight" />
            )}
            <Button type="button" size="icon" variant="ghost" className="size-8" disabled={i === 0} onClick={() => { const n = [...targets]; [n[i - 1], n[i]] = [n[i], n[i - 1]]; update(n); }}><ArrowUp /></Button>
            <Button type="button" size="icon" variant="ghost" className="size-8" disabled={i === targets.length - 1} onClick={() => { const n = [...targets]; [n[i + 1], n[i]] = [n[i], n[i + 1]]; update(n); }}><ArrowDown /></Button>
            <Button type="button" size="icon" variant="ghost" className="size-8 text-danger" onClick={() => update(targets.filter((_, j) => j !== i))}><X /></Button>
          </div>
        ))}
        {unused.length > 0 && (
          <Select value="" onChange={(e) => e.target.value && update([...targets, { connection_id: Number(e.target.value), weight: 100 }])}>
            <option value="">+ Add connection…</option>
            {unused.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </Select>
        )}
      </div>
    </div>
  );
}

export function RoutesPage() {
  const me = useMe();
  return (
    <ResourcePage
      title="Routes"
      description="Which vendors carry which traffic. The first matching route wins: lower priority number first, then the most specific."
      path="routes"
      noun="Route"
      canWrite={can.manage(me)}
      columns={[
        { key: "priority", header: "Prio", className: "w-16 tabular" },
        { key: "name", header: "Route", render: (r) => <span className="font-medium">{r.name}</span> },
        {
          key: "match", header: "Matches", render: (r) => (
            <div className="flex flex-wrap gap-1">
              {r.country_iso ? <Badge>{r.country_iso}</Badge> : <Badge>All countries</Badge>}
              {r.network_id && <Badge>{r.network_name ?? `network #${r.network_id}`}</Badge>}
              {r.client_id && <Badge tone="primary">{r.client_name ?? `client #${r.client_id}`}</Badge>}
              {r.account_id && <Badge tone="primary">{r.account_name ?? `account #${r.account_id}`}</Badge>}
              {r.sender_match !== "any" && <Badge tone="warning">sender {r.sender_match} “{r.sender_pattern}”</Badge>}
            </div>
          ),
        },
        { key: "policy", header: "Policy", render: (r) => <Badge tone="primary">{r.policy === "lcr" ? "LCR" : r.policy}</Badge> },
        { key: "targets", header: "Vendors", render: (r) => <span className="text-sm">{(r.targets ?? []).map((t: Row) => t.name).join(" → ") || <span className="text-danger">none</span>}</span> },
        { key: "status", header: "Status", render: (r) => <StatusBadge status={r.status} /> },
      ]}
      extraEditor={(_row, values, set) => <TargetsEditor values={values} set={set} />}
      toBody={(v) => ({ ...v, targets: (v.targets ?? []).map((t: Row) => ({ connection_id: t.connection_id, weight: t.weight ?? 100 })) })}
      fields={[
        { name: "name", label: "Name", required: true, span: 1 },
        { name: "priority", label: "Priority", type: "number", default: 100, span: 1, hint: "Lower runs first" },
        { name: "country_iso", label: "Country", type: "select", span: 1, options: (l) => l.countries.map((c) => ({ value: c.iso, label: c.name })) },
        { name: "network_id", label: "Network ID", type: "number", span: 1 },
        { name: "client_id", label: "Only for client", type: "select", span: 1, options: (l) => l.clients.map((c) => ({ value: String(c.id), label: c.name })) },
        { name: "account_id", label: "Only for account", type: "select", span: 1, options: (l, v) => l.accounts.filter((a) => !v.client_id || String(a.client_id) === String(v.client_id)).map((a) => ({ value: String(a.id), label: a.username })) },
        { name: "sender_match", label: "Sender ID rule", type: "select", default: "any", span: 1, options: [{ value: "any", label: "Any sender" }, { value: "exact", label: "Equals" }, { value: "prefix", label: "Starts with" }, { value: "regex", label: "Regular expression" }] },
        { name: "sender_pattern", label: "Sender ID", span: 1, showWhen: (v) => v.sender_match && v.sender_match !== "any" },
        { name: "policy", label: "Policy", type: "select", required: true, default: "priority", span: 1, options: [{ value: "priority", label: "Priority / failover" }, { value: "weighted", label: "Weighted split" }, { value: "lcr", label: "Least cost (LCR)" }] },
        { name: "allow_loss", label: "Allow vendors costing more than the client price", type: "switch", span: 1 },
        { name: "status", label: "Status", type: "select", required: true, default: "active", options: active },
      ]}
    />
  );
}

export function ContentRulesPage() {
  const me = useMe();
  return (
    <ResourcePage
      title="Content rules"
      description="Change or block messages before routing, in priority order"
      path="content-rules"
      noun="Rule"
      canWrite={can.manage(me)}
      columns={[
        { key: "priority", header: "Prio", className: "w-16 tabular" },
        { key: "name", header: "Rule", render: (r) => <span className="font-medium">{r.name}</span> },
        { key: "action", header: "Action", render: (r) => <Badge tone={r.action === "block" ? "danger" : "primary"}>{r.action.replace("_", " ")}</Badge> },
        { key: "when", header: "When", render: (r) => <span className="text-sm text-muted-foreground">{[r.client_id && `client #${r.client_id}`, r.country_iso, r.sender_pattern && `sender ~ ${r.sender_pattern}`, r.text_pattern && `text ~ ${r.text_pattern}`].filter(Boolean).join(" · ") || "always"}</span> },
        { key: "status", header: "Status", render: (r) => <StatusBadge status={r.status} /> },
      ]}
      fields={[
        { name: "name", label: "Name", required: true, span: 1 },
        { name: "priority", label: "Priority", type: "number", default: 100, span: 1 },
        { name: "action", label: "Action", type: "select", required: true, default: "replace_text", options: [{ value: "replace_text", label: "Replace text (regex)" }, { value: "replace_sender", label: "Replace sender ID" }, { value: "prepend", label: "Add text before" }, { value: "append", label: "Add text after" }, { value: "block", label: "Block message" }] },
        { name: "find", label: "Find (regular expression)", showWhen: (v) => v.action === "replace_text", placeholder: "https?://\\S+" },
        { name: "replace_with", label: "Replace with / new value", showWhen: (v) => v.action !== "block" },
        { name: "client_id", label: "Only for client", type: "select", span: 1, options: (l) => l.clients.map((c) => ({ value: String(c.id), label: c.name })) },
        { name: "country_iso", label: "Only for country", type: "select", span: 1, options: (l) => l.countries.map((c) => ({ value: c.iso, label: c.name })) },
        { name: "sender_pattern", label: "When sender matches (regex)", span: 1 },
        { name: "text_pattern", label: "When text matches (regex)", span: 1 },
        { name: "status", label: "Status", type: "select", required: true, default: "active", options: active },
      ]}
    />
  );
}

type TestResult = {
  number: string; country_iso: string; network?: string; price?: string; error?: string;
  content: { sender: string; text: string; rules: string[] | null; blocked: boolean; parts: number };
  route?: { id: number; name: string; connections: { id: number; name: string; cost?: string; margin?: string }[] };
};

export function RouteTestPage() {
  const { data: l } = useLookups();
  const [v, setV] = React.useState({ client_id: "", sender: "", destination: "", text: "Test message" });
  const run = useMutation({ mutationFn: () => api.post<TestResult>("/api/routes/test", { ...v, client_id: Number(v.client_id) }), onError: (e: Error) => toast.error(e.message) });
  const r = run.data;
  return (
    <>
      <PageHeader title="Route tester" description="See exactly how a message would be priced and routed, without sending it" />
      <div className="grid gap-4 lg:grid-cols-5">
        <Card className="lg:col-span-2">
          <CardHeader title="Message" />
          <CardBody>
            <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); run.mutate(); }}>
              <Field label="Client"><Select value={v.client_id} onChange={(e) => setV({ ...v, client_id: e.target.value })} required><option value="">Choose…</option>{l?.clients.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</Select></Field>
              <Field label="Destination number"><Input value={v.destination} onChange={(e) => setV({ ...v, destination: e.target.value })} required placeholder="923001234567" /></Field>
              <Field label="Sender ID"><Input value={v.sender} onChange={(e) => setV({ ...v, sender: e.target.value })} /></Field>
              <Field label="Text"><Textarea value={v.text} onChange={(e) => setV({ ...v, text: e.target.value })} rows={3} /></Field>
              <Button type="submit" disabled={run.isPending}><FlaskConical /> Test route</Button>
            </form>
          </CardBody>
        </Card>
        <Card className="lg:col-span-3">
          <CardHeader title="Result" />
          <CardBody>
            {!r ? <Empty title="Run a test to see the routing decision" /> : (
              <div className="space-y-5">
                <div className="grid gap-3 sm:grid-cols-3">
                  <div><p className="text-xs text-muted-foreground">Number</p><p className="font-mono">{r.number}</p></div>
                  <div><p className="text-xs text-muted-foreground">Country / network</p><p>{r.country_iso || "unknown"} {r.network && `· ${r.network}`}</p></div>
                  <div><p className="text-xs text-muted-foreground">Client price</p><p className="font-semibold tabular">{r.price ? rate(r.price) : "—"}</p></div>
                </div>
                <div className="rounded-lg bg-muted p-3 text-sm">
                  <p><span className="text-muted-foreground">After content rules:</span> {r.content.blocked ? <Badge tone="danger">blocked</Badge> : <>“{r.content.text}” from <b>{r.content.sender || "—"}</b> · {r.content.parts} part(s)</>}</p>
                  {r.content.rules?.length ? <p className="mt-1 text-xs text-muted-foreground">Rules applied: {r.content.rules.join(", ")}</p> : null}
                </div>
                {r.error ? <p className="rounded-md bg-danger/10 px-3 py-2 text-sm text-danger">{r.error}</p> : r.route && (
                  <div>
                    <p className="text-sm">Route <b>{r.route.name}</b> · tried in this order:</p>
                    <ol className="mt-3 space-y-2">
                      {r.route.connections.map((c, i) => (
                        <li key={c.id} className="flex items-center justify-between rounded-md border px-3 py-2 text-sm">
                          <span><span className="mr-2 text-muted-foreground">{i + 1}.</span><b>{c.name}</b></span>
                          <span className="tabular text-muted-foreground">{c.cost ? <>cost {rate(c.cost)} · margin <span className={Number(c.margin) < 0 ? "text-danger" : "text-success"}>{rate(c.margin)}</span></> : "no vendor rate"}</span>
                        </li>
                      ))}
                    </ol>
                  </div>
                )}
              </div>
            )}
          </CardBody>
        </Card>
      </div>
    </>
  );
}

