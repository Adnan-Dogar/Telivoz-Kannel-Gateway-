import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, KeyRound, Plus, Wallet } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { dateTime, money } from "@/lib/format";
import { can, useMe } from "@/lib/session";
import { ResourcePage, type Row } from "@/components/resource";
import { PageHeader } from "@/components/page";
import { Card, CardHeader } from "@/components/ui/card";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { Dialog, Sheet } from "@/components/ui/sheet";
import { Empty } from "@/components/ui/misc";
import { TBody, TD, TH, THead, TR, Table } from "@/components/ui/table";

const statusOptions = [{ value: "active", label: "Active" }, { value: "disabled", label: "Disabled" }];

function TopupDialog({ client, onClose }: { client: Row | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [amount, setAmount] = React.useState("");
  const [note, setNote] = React.useState("");
  const topup = useMutation({
    mutationFn: () => api.post<{ balance: string }>(`/api/clients/${client?.id}/topup`, { amount, note }),
    onSuccess: (r) => {
      toast.success(`New balance ${money(r.balance)}`);
      qc.invalidateQueries({ queryKey: ["clients"] });
      setAmount("");
      setNote("");
      onClose();
    },
    onError: (e: Error) => toast.error(e.message),
  });
  return (
    <Dialog open={!!client} onOpenChange={(o) => !o && onClose()} title={`Add credit · ${client?.name ?? ""}`} description="Use a negative amount to correct a balance."
      footer={<><Button variant="outline" onClick={onClose}>Cancel</Button><Button onClick={() => topup.mutate()} disabled={!amount || topup.isPending}>Apply</Button></>}>
      <div className="grid gap-4">
        <Field label={`Amount (${client?.currency ?? ""})`}><Input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="100.00" autoFocus /></Field>
        <Field label="Note"><Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="Bank transfer ref…" /></Field>
      </div>
    </Dialog>
  );
}

type LedgerRow = { id: number; kind: string; amount: number; balance_after: number; note: string; created_at: string; created_by_name: string | null };

function LedgerSheet({ client, onClose }: { client: Row | null; onClose: () => void }) {
  const [charges, setCharges] = React.useState(false);
  const q = useQuery({ queryKey: ["ledger", client?.id, charges], queryFn: () => api.get<LedgerRow[]>(`/api/clients/${client?.id}/ledger${charges ? "?charges=1" : ""}`), enabled: !!client });
  return (
    <Sheet open={!!client} onOpenChange={(o) => !o && onClose()} title={`Ledger · ${client?.name ?? ""}`} description={`Balance ${money(client?.balance)} ${client?.currency ?? ""}`} wide>
      <label className="mb-3 flex items-center gap-2 text-sm"><input type="checkbox" checked={charges} onChange={(e) => setCharges(e.target.checked)} /> Include message charges</label>
      <LedgerTable rows={q.data} />
    </Sheet>
  );
}

function LedgerTable({ rows }: { rows?: LedgerRow[] }) {
  if (!rows?.length) return <Empty title="No ledger entries" />;
  return (
    <Table>
      <THead><tr><TH>Date</TH><TH>Type</TH><TH>Note</TH><TH className="text-right">Amount</TH><TH className="text-right">Balance</TH></tr></THead>
      <TBody>
        {rows.map((l) => (
          <TR key={l.id}>
            <TD className="whitespace-nowrap text-muted-foreground">{dateTime(l.created_at)}</TD>
            <TD><Badge tone={l.kind === "topup" ? "success" : l.kind === "refund" ? "primary" : "neutral"}>{l.kind}</Badge></TD>
            <TD className="max-w-64 truncate text-muted-foreground">{l.note || l.created_by_name || "—"}</TD>
            <TD className={`text-right tabular ${l.amount >= 0 ? "text-success" : ""}`}>{money(l.amount, 4)}</TD>
            <TD className="text-right tabular">{money(l.balance_after, 4)}</TD>
          </TR>
        ))}
      </TBody>
    </Table>
  );
}

export function ClientsPage() {
  const me = useMe();
  const [topup, setTopup] = React.useState<Row | null>(null);
  const [ledger, setLedger] = React.useState<Row | null>(null);
  return (
    <>
      <ResourcePage
        title="Clients"
        description="Customers, their balances and account managers"
        path="clients"
        noun="Client"
        canWrite={can.manage(me)}
        columns={[
          { key: "name", header: "Client", render: (r) => <div><p className="font-medium">{r.name}</p><p className="text-xs text-muted-foreground">{r.email}</p></div> },
          { key: "owner_name", header: "Account manager", render: (r) => r.owner_name ?? <span className="text-muted-foreground">—</span> },
          { key: "billing_type", header: "Billing", render: (r) => <Badge>{r.billing_type}</Badge> },
          { key: "balance", header: "Balance", className: "text-right", render: (r) => <span className={`tabular font-medium ${Number(r.balance) <= 0 ? "text-danger" : ""}`}>{money(r.balance)} <span className="text-xs text-muted-foreground">{r.currency}</span></span> },
          { key: "status", header: "Status", render: (r) => <StatusBadge status={r.status} /> },
        ]}
        rowActions={(r) => (
          <div className="flex justify-end gap-1">
            {can.money(me) && <Button size="sm" variant="ghost" onClick={() => setTopup(r)}><Plus /> Credit</Button>}
            <Button size="sm" variant="ghost" onClick={() => setLedger(r)}><Wallet /> Ledger</Button>
          </div>
        )}
        fields={[
          { name: "name", label: "Company name", required: true },
          { name: "email", label: "Email", span: 1 },
          { name: "phone", label: "Phone", span: 1 },
          { name: "country_iso", label: "Country", type: "select", span: 1, options: (l) => l.countries.map((c) => ({ value: c.iso, label: c.name })) },
          { name: "currency", label: "Currency", default: "USD", span: 1 },
          { name: "billing_type", label: "Billing", type: "select", required: true, default: "prepaid", span: 1, options: [{ value: "prepaid", label: "Prepaid" }, { value: "postpaid", label: "Postpaid (credit limit)" }] },
          { name: "credit_limit", label: "Credit limit", type: "decimal", span: 1, default: "0", showWhen: (v) => v.billing_type === "postpaid" },
          { name: "owner_id", label: "Account manager", type: "select", options: (l) => l.users.map((u) => ({ value: String(u.id), label: `${u.name} (${u.role.replace("_", " ")})` })) },
          { name: "dlr_webhook_url", label: "DLR webhook URL", placeholder: "https://…", hint: "HTTP clients receive delivery reports here." },
          { name: "dlr_format", label: "Webhook format", type: "select", default: "v1", span: 1, options: [{ value: "v1", label: "New JSON (v1)" }, { value: "legacy", label: "Old system format" }] },
          { name: "status", label: "Status", type: "select", default: "active", span: 1, required: true, options: statusOptions },
        ]}
      />
      <TopupDialog client={topup} onClose={() => setTopup(null)} />
      <LedgerSheet client={ledger} onClose={() => setLedger(null)} />
    </>
  );
}

type ApiKey = { id: number; name: string; prefix: string; created_at: string; last_used_at: string | null; revoked_at: string | null };

function KeysSheet({ account, onClose }: { account: Row | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [created, setCreated] = React.useState<string | null>(null);
  const keys = useQuery({ queryKey: ["keys", account?.id], queryFn: () => api.get<ApiKey[]>(`/api/accounts/${account?.id}/api-keys`), enabled: !!account });
  const create = useMutation({
    mutationFn: () => api.post<{ key: string }>(`/api/accounts/${account?.id}/api-keys`, { name: "Portal" }),
    onSuccess: (r) => { setCreated(r.key); qc.invalidateQueries({ queryKey: ["keys"] }); },
    onError: (e: Error) => toast.error(e.message),
  });
  const revoke = useMutation({
    mutationFn: (id: number) => api.del(`/api/api-keys/${id}`),
    onSuccess: () => { toast.success("Key revoked"); qc.invalidateQueries({ queryKey: ["keys"] }); },
  });
  return (
    <Sheet open={!!account} onOpenChange={(o) => { if (!o) { onClose(); setCreated(null); } }} title={`API keys · ${account?.username ?? ""}`} wide
      description="Use a key as: Authorization: Bearer <key>. Keys are shown only once.">
      {created && (
        <div className="mb-5 rounded-lg border border-success/40 bg-success/10 p-4">
          <p className="text-sm font-medium">New key — copy it now</p>
          <div className="mt-2 flex gap-2">
            <Input readOnly value={created} className="font-mono text-xs" />
            <Button variant="outline" onClick={() => { navigator.clipboard.writeText(created); toast.success("Copied"); }}><Copy /></Button>
          </div>
        </div>
      )}
      <Button onClick={() => create.mutate()} disabled={create.isPending}><KeyRound /> Create API key</Button>
      <div className="mt-5">
        {!keys.data?.length ? <Empty title="No API keys" /> : (
          <Table>
            <THead><tr><TH>Key</TH><TH>Created</TH><TH>Last used</TH><TH /></tr></THead>
            <TBody>
              {keys.data.map((k) => (
                <TR key={k.id}>
                  <TD className="font-mono text-xs">{k.prefix}…</TD>
                  <TD className="text-muted-foreground">{dateTime(k.created_at)}</TD>
                  <TD className="text-muted-foreground">{dateTime(k.last_used_at)}</TD>
                  <TD className="text-right">{k.revoked_at ? <Badge>revoked</Badge> : <Button size="sm" variant="ghost" className="text-danger" onClick={() => revoke.mutate(k.id)}>Revoke</Button>}</TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </div>
      <Card className="mt-6">
        <CardHeader title="Quick start" description="Send a message with the HTTP API" />
        <pre className="overflow-x-auto p-4 text-xs leading-relaxed">{`curl -X POST ${location.origin}/api/v1/messages \\
  -H "Authorization: Bearer <key>" \\
  -H "Content-Type: application/json" \\
  -H "Idempotency-Key: order-123" \\
  -d '{"to":"923001234567","from":"MyBrand","text":"Hello"}'`}</pre>
      </Card>
    </Sheet>
  );
}

export function AccountsPage() {
  const me = useMe();
  const [keys, setKeys] = React.useState<Row | null>(null);
  return (
    <>
      <ResourcePage
        title="Accounts & API keys"
        description="SMPP binds and HTTP API logins"
        path="accounts"
        noun="Account"
        canWrite={can.manage(me)}
        columns={[
          { key: "username", header: "Username", render: (r) => <span className="font-mono text-sm font-medium">{r.username}</span> },
          { key: "kind", header: "Type", render: (r) => <Badge tone="primary">{r.kind.toUpperCase()}</Badge> },
          ...(can.staff(me) ? [{ key: "client_name", header: "Client" }] : []),
          { key: "tps", header: "TPS", className: "text-right" },
          { key: "allowed_ips", header: "Allowed IPs", render: (r: Row) => (r.allowed_ips?.length ? r.allowed_ips.join(", ") : <span className="text-muted-foreground">any</span>) },
          { key: "status", header: "Status", render: (r) => <StatusBadge status={r.status} /> },
        ]}
        rowActions={(r) => r.kind === "http" && <Button size="sm" variant="ghost" onClick={() => setKeys(r)}><KeyRound /> API keys</Button>}
        fields={[
          { name: "client_id", label: "Client", type: "select", required: true, createOnly: true, options: (l) => l.clients.map((c) => ({ value: String(c.id), label: c.name })) },
          { name: "kind", label: "Type", type: "select", required: true, default: "smpp", createOnly: true, span: 1, options: [{ value: "smpp", label: "SMPP" }, { value: "http", label: "HTTP API" }] },
          { name: "username", label: "Username / system ID", required: true, span: 1 },
          { name: "password", label: "Password", type: "password", hint: "Leave empty to keep the current password. SMPP passwords are limited to 8 characters by the protocol." },
          { name: "tps", label: "Max TPS", type: "number", default: 50, span: 1 },
          { name: "max_binds", label: "Max binds", type: "number", default: 4, span: 1, showWhen: (v) => v.kind !== "http" },
          { name: "allowed_ips", label: "Allowed IPs", type: "tags", placeholder: "203.0.113.10, 198.51.100.0/24", hint: "Empty allows any IP." },
          { name: "status", label: "Status", type: "select", required: true, default: "active", options: statusOptions },
        ]}
      />
      <KeysSheet account={keys} onClose={() => setKeys(null)} />
    </>
  );
}

export function BillingPage() {
  const me = useMe();
  const client = useQuery({ queryKey: ["clients", "self"], queryFn: () => api.get<{ items: Row[] }>("/api/clients") });
  const c = client.data?.items[0];
  const ledger = useQuery({ queryKey: ["ledger", me.client_id], queryFn: () => api.get<LedgerRow[]>(`/api/clients/${me.client_id}/ledger`), enabled: !!me.client_id });
  return (
    <>
      <PageHeader title="Billing" description="Balance and payment history" />
      <div className="mb-4 grid gap-4 sm:grid-cols-3">
        <Card className="p-5"><p className="text-sm text-muted-foreground">Balance</p><p className="mt-1 text-3xl font-semibold tabular">{money(c?.balance)} <span className="text-base text-muted-foreground">{c?.currency}</span></p></Card>
        <Card className="p-5"><p className="text-sm text-muted-foreground">Billing</p><p className="mt-1 text-xl font-semibold capitalize">{c?.billing_type ?? "—"}</p></Card>
        <Card className="p-5"><p className="text-sm text-muted-foreground">Credit limit</p><p className="mt-1 text-xl font-semibold tabular">{money(c?.credit_limit)}</p></Card>
      </div>
      <Card><CardHeader title="Payments and adjustments" /><LedgerTable rows={ledger.data} /></Card>
    </>
  );
}

