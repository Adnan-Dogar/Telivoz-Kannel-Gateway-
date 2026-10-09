import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import QRCode from "qrcode";
import { ShieldCheck, ShieldOff } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { ago, dateTime } from "@/lib/format";
import { can, useMe, useTheme } from "@/lib/session";
import type { Role } from "@/lib/types";
import { ResourcePage } from "@/components/resource";
import { PageHeader } from "@/components/page";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { Badge, StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { Empty, Switch } from "@/components/ui/misc";
import { TBody, TD, TH, THead, TR, Table } from "@/components/ui/table";

const roles: { value: Role; label: string }[] = [
  { value: "admin", label: "Administrator" },
  { value: "manager", label: "Manager" },
  { value: "team_lead", label: "Team lead" },
  { value: "sales", label: "Sales / account manager" },
  { value: "finance", label: "Finance" },
  { value: "noc", label: "NOC / operations" },
  { value: "client", label: "Client portal user" },
];

type TeamMember = { id: number; name: string; email: string; role: Role; depth: number; clients: number };

function TeamTree() {
  const team = useQuery({ queryKey: ["team"], queryFn: () => api.get<TeamMember[]>("/api/team") });
  return (
    <Card className="mb-4">
      <CardHeader title="My team" description="Managers and team leads see their own clients and vendors plus everyone's below them." />
      <CardBody className="space-y-1">
        {(team.data ?? []).map((m) => (
          <div key={m.id} className="flex items-center gap-3 rounded-md px-2 py-1.5 hover:bg-muted" style={{ paddingLeft: 8 + m.depth * 24 }}>
            <span className="grid size-7 place-items-center rounded-full bg-accent text-xs font-semibold text-accent-foreground">{(m.name || m.email)[0]?.toUpperCase()}</span>
            <span className="text-sm font-medium">{m.name || m.email}</span>
            <Badge>{m.role.replace("_", " ")}</Badge>
            <span className="ml-auto text-xs text-muted-foreground">{m.clients} clients</span>
          </div>
        ))}
      </CardBody>
    </Card>
  );
}

export function UsersPage() {
  const me = useMe();
  return (
    <>
      <PageHeader title="Users & team" description="Portal users, roles and reporting lines" />
      {me.role !== "admin" && <TeamTree />}
      <ResourcePage
        embedded
        title="Users"
        path="users"
        noun="User"
        canWrite={can.admin(me)}
        rowActions={(r) => can.admin(me) && r.totp_enabled && (
          <Button size="sm" variant="ghost" onClick={() => api.post(`/api/users/${r.id}/reset-2fa`).then(() => toast.success("Two-factor login reset; the user signs in with the password only")).catch((e: Error) => toast.error(e.message))}>
            <ShieldOff /> Reset 2FA
          </Button>
        )}
        columns={[
          { key: "name", header: "User", render: (r) => <div><p className="font-medium">{r.name || "—"}</p><p className="text-xs text-muted-foreground">{r.email}</p></div> },
          { key: "role", header: "Role", render: (r) => <Badge tone={r.role === "admin" ? "primary" : "neutral"}>{roles.find((x) => x.value === r.role)?.label ?? r.role}</Badge> },
          { key: "manager_name", header: "Reports to", render: (r) => r.manager_name ?? <span className="text-muted-foreground">—</span> },
          { key: "client_name", header: "Client", render: (r) => r.client_name ?? <span className="text-muted-foreground">—</span> },
          { key: "totp_enabled", header: "2FA", render: (r) => (r.totp_enabled ? <Badge tone="success">on</Badge> : <Badge>off</Badge>) },
          { key: "last_login_at", header: "Last login", render: (r) => <span className="text-muted-foreground">{ago(r.last_login_at)}</span> },
          { key: "status", header: "Status", render: (r) => <StatusBadge status={r.status} /> },
        ]}
        fields={[
          { name: "name", label: "Full name", span: 1 },
          { name: "email", label: "Email", required: true, span: 1 },
          { name: "password", label: "Password", type: "password", hint: "Leave empty to keep. At least 6 characters." },
          { name: "role", label: "Role", type: "select", required: true, default: "sales", span: 1, options: roles },
          { name: "manager_id", label: "Reports to", type: "select", span: 1, showWhen: (v) => v.role !== "client", options: (l) => l.users.map((u) => ({ value: String(u.id), label: u.name })) },
          { name: "client_id", label: "Client", type: "select", required: true, span: 1, showWhen: (v) => v.role === "client", options: (l) => l.clients.map((c) => ({ value: String(c.id), label: c.name })) },
          { name: "status", label: "Status", type: "select", required: true, default: "active", span: 1, options: [{ value: "active", label: "Active" }, { value: "disabled", label: "Disabled" }] },
        ]}
      />
    </>
  );
}

type Audit = { id: number; created_at: string; user_name: string | null; user_email: string | null; action: string; entity: string; entity_id: string; details: Record<string, unknown> };

export function AuditPage() {
  const q = useQuery({ queryKey: ["audit"], queryFn: () => api.get<Audit[]>("/api/audit") });
  return (
    <>
      <PageHeader title="Audit log" description="Every change made in the portal, newest first" />
      <Card>
        {!q.data?.length ? <Empty title="Nothing logged yet" /> : (
          <Table>
            <THead><tr><TH>When</TH><TH>User</TH><TH>Action</TH><TH>Item</TH><TH>Details</TH></tr></THead>
            <TBody>
              {q.data.map((a) => (
                <TR key={a.id}>
                  <TD className="whitespace-nowrap text-muted-foreground">{dateTime(a.created_at)}</TD>
                  <TD>{a.user_name || a.user_email || "system"}</TD>
                  <TD><Badge tone={a.action === "delete" ? "danger" : a.action === "create" ? "success" : "primary"}>{a.action}</Badge></TD>
                  <TD className="whitespace-nowrap">{a.entity.replace("_", " ")} {a.entity_id && <span className="text-muted-foreground">#{a.entity_id}</span>}</TD>
                  <TD className="max-w-md truncate font-mono text-xs text-muted-foreground">{Object.keys(a.details ?? {}).length ? JSON.stringify(a.details) : ""}</TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </Card>
    </>
  );
}

function TwoFactorCard() {
  const me = useMe();
  const qc = useQueryClient();
  const [setup, setSetup] = React.useState<{ secret: string; qr: string } | null>(null);
  const [code, setCode] = React.useState("");
  const start = useMutation({
    mutationFn: () => api.post<{ secret: string; otpauth_url: string }>("/api/auth/2fa/setup"),
    onSuccess: async (r) => setSetup({ secret: r.secret, qr: await QRCode.toDataURL(r.otpauth_url, { margin: 1, width: 200 }) }),
    onError: (e: Error) => toast.error(e.message),
  });
  const enable = useMutation({
    mutationFn: () => api.post("/api/auth/2fa/enable", { code }),
    onSuccess: () => { toast.success("Two-factor login is on"); setSetup(null); setCode(""); qc.invalidateQueries({ queryKey: ["me"] }); },
    onError: (e: Error) => toast.error(e.message),
  });
  const disable = useMutation({
    mutationFn: () => api.post("/api/auth/2fa/disable", { code }),
    onSuccess: () => { toast.success("Two-factor login is off"); setCode(""); qc.invalidateQueries({ queryKey: ["me"] }); },
    onError: (e: Error) => toast.error(e.message),
  });
  return (
    <Card>
      <CardHeader title="Two-factor login" description="Ask for a code from an authenticator app (Google Authenticator, Microsoft Authenticator, 1Password…) at sign-in." />
      <CardBody>
        {me.totp_enabled ? (
          <div className="grid gap-4">
            <p className="flex items-center gap-2 text-sm font-medium text-success"><ShieldCheck className="size-4" /> On for your account</p>
            <Field label="Code from your app, to turn it off"><Input inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} /></Field>
            <div><Button variant="outline" onClick={() => disable.mutate()} disabled={code.length !== 6}><ShieldOff /> Turn off</Button></div>
          </div>
        ) : setup ? (
          <div className="grid gap-4 sm:grid-cols-[auto_1fr]">
            <img src={setup.qr} alt="QR code for your authenticator app" className="size-48 rounded-lg border bg-white p-2" />
            <div className="grid content-start gap-3">
              <p className="text-sm">Scan the code with your app, or enter this key:</p>
              <code className="break-all rounded-md bg-muted px-2 py-1 text-xs">{setup.secret}</code>
              <Field label="Then enter the 6-digit code"><Input inputMode="numeric" maxLength={6} autoFocus value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} /></Field>
              <div><Button onClick={() => enable.mutate()} disabled={code.length !== 6 || enable.isPending}><ShieldCheck /> Confirm and turn on</Button></div>
            </div>
          </div>
        ) : (
          <div className="grid gap-3">
            <p className="text-sm text-muted-foreground">Off. Strongly recommended for administrators and finance users.</p>
            <div><Button onClick={() => start.mutate()} disabled={start.isPending}><ShieldCheck /> Set up</Button></div>
          </div>
        )}
      </CardBody>
    </Card>
  );
}

export function SettingsPage() {
  const me = useMe();
  const { dark, toggle } = useTheme();
  const [current, setCurrent] = React.useState("");
  const [next, setNext] = React.useState("");
  const change = useMutation({
    mutationFn: () => api.post("/api/auth/password", { current, new: next }),
    onSuccess: () => { toast.success("Password changed"); setCurrent(""); setNext(""); },
    onError: (e: Error) => toast.error(e.message),
  });
  return (
    <>
      <PageHeader title="Settings" />
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader title="Profile" />
          <CardBody className="space-y-3 text-sm">
            <div className="flex justify-between"><span className="text-muted-foreground">Name</span><span>{me.name || "—"}</span></div>
            <div className="flex justify-between"><span className="text-muted-foreground">Email</span><span>{me.email}</span></div>
            <div className="flex justify-between"><span className="text-muted-foreground">Role</span><span className="capitalize">{me.role.replace("_", " ")}</span></div>
            {me.client_name && <div className="flex justify-between"><span className="text-muted-foreground">Company</span><span>{me.client_name}</span></div>}
            <div className="flex items-center justify-between border-t pt-3"><span>Dark mode</span><Switch checked={dark} onCheckedChange={toggle} /></div>
          </CardBody>
        </Card>
        <TwoFactorCard />
        <Card>
          <CardHeader title="Change password" />
          <CardBody>
            <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); change.mutate(); }}>
              <Field label="Current password"><Input type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required /></Field>
              <Field label="New password" hint="At least 8 characters"><Input type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} required minLength={8} /></Field>
              <div><Button type="submit" disabled={change.isPending}>Update password</Button></div>
            </form>
          </CardBody>
        </Card>
      </div>
    </>
  );
}
