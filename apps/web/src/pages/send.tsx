import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FileUp, Send } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { dateTime, num } from "@/lib/format";
import { useLookups } from "@/lib/session";
import { PageHeader } from "@/components/page";
import { Card, CardBody, CardHeader } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, Select, Textarea } from "@/components/ui/input";
import { Empty, Tabs } from "@/components/ui/misc";
import { TBody, TD, TH, THead, TR, Table } from "@/components/ui/table";

// Mirrors the gateway's GSM-7 detection and part limits.
const gsm = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà";
const ext = "\f^{}\\[~]|€";
function segments(text: string) {
  const chars = [...text];
  const isGsm = chars.every((c) => gsm.includes(c) || ext.includes(c));
  if (isGsm) {
    const len = chars.reduce((a, c) => a + (ext.includes(c) ? 2 : 1), 0);
    return { encoding: "GSM-7", len, parts: len <= 160 ? 1 : Math.ceil(len / 153), max: len <= 160 ? 160 : 153 };
  }
  const len = [...text].reduce((a, c) => a + (c.codePointAt(0)! > 0xffff ? 2 : 1), 0);
  return { encoding: "Unicode", len, parts: len <= 70 ? 1 : Math.ceil(len / 67), max: len <= 70 ? 70 : 67 };
}

function useHttpAccounts() {
  const { data } = useLookups();
  return (data?.accounts ?? []).filter((a) => a.kind === "http");
}

function Counter({ text }: { text: string }) {
  const s = segments(text);
  return (
    <p className="text-xs text-muted-foreground tabular">
      {s.encoding} · {s.len} characters · {s.parts} part{s.parts === 1 ? "" : "s"}
    </p>
  );
}

function QuickSend() {
  const accounts = useHttpAccounts();
  const [account, setAccount] = React.useState("");
  const [from, setFrom] = React.useState("");
  const [to, setTo] = React.useState("");
  const [text, setText] = React.useState("");
  const send = useMutation({
    mutationFn: () => api.post<{ id: string; parts: number; price: string }>("/api/messages/send", { account_id: Number(account || accounts[0]?.id), from, to, text }),
    onSuccess: (r) => {
      toast.success(`Message accepted (${r.parts} part${r.parts === 1 ? "" : "s"}, ${r.price})`);
      setTo("");
    },
    onError: (e: Error) => toast.error(e.message),
  });
  if (!accounts.length) return <Empty title="No HTTP account">Create an HTTP account to send from the portal.</Empty>;
  return (
    <form className="grid gap-4 md:grid-cols-2" onSubmit={(e) => { e.preventDefault(); send.mutate(); }}>
      <Field label="Send with account">
        <Select value={account || String(accounts[0]?.id ?? "")} onChange={(e) => setAccount(e.target.value)}>
          {accounts.map((a) => <option key={a.id} value={a.id}>{a.username}</option>)}
        </Select>
      </Field>
      <Field label="Sender ID"><Input value={from} onChange={(e) => setFrom(e.target.value)} placeholder="e.g. TELIVOZ" maxLength={15} /></Field>
      <Field label="To" className="md:col-span-2" hint="International format, e.g. 923001234567"><Input value={to} onChange={(e) => setTo(e.target.value)} required inputMode="tel" /></Field>
      <Field label="Message" className="md:col-span-2">
        <Textarea value={text} onChange={(e) => setText(e.target.value)} required rows={5} />
        <Counter text={text} />
      </Field>
      <div className="md:col-span-2"><Button type="submit" disabled={send.isPending}><Send /> {send.isPending ? "Sending…" : "Send message"}</Button></div>
    </form>
  );
}

type Campaign = { id: number; name: string; client_name: string; status: string; total: number; processed: number; accepted: number; rejected: number; created_at: string; last_error: string };

function Campaigns() {
  const qc = useQueryClient();
  const accounts = useHttpAccounts();
  const [account, setAccount] = React.useState("");
  const [name, setName] = React.useState("");
  const [from, setFrom] = React.useState("");
  const [text, setText] = React.useState("");
  const [file, setFile] = React.useState<File | null>(null);
  const list = useQuery({ queryKey: ["campaigns"], queryFn: () => api.get<Campaign[]>("/api/campaigns"), refetchInterval: 3000 });
  const create = useMutation({
    mutationFn: () => {
      const f = new FormData();
      f.set("account_id", account || String(accounts[0]?.id ?? ""));
      f.set("name", name);
      f.set("sender", from);
      f.set("text", text);
      f.set("numbers", file!);
      return api.upload<{ id: number; total: number }>("/api/campaigns", f);
    },
    onSuccess: (r) => {
      toast.success(`Campaign started with ${num(r.total)} numbers`);
      setFile(null);
      setName("");
      qc.invalidateQueries({ queryKey: ["campaigns"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });
  return (
    <div className="grid gap-6 xl:grid-cols-5">
      <form className="grid content-start gap-4 xl:col-span-2" onSubmit={(e) => { e.preventDefault(); if (file) create.mutate(); }}>
        <Field label="Campaign name"><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="October promo" /></Field>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Account">
            <Select value={account || String(accounts[0]?.id ?? "")} onChange={(e) => setAccount(e.target.value)}>
              {accounts.map((a) => <option key={a.id} value={a.id}>{a.username}</option>)}
            </Select>
          </Field>
          <Field label="Sender ID"><Input value={from} onChange={(e) => setFrom(e.target.value)} maxLength={15} /></Field>
        </div>
        <Field label="Message"><Textarea value={text} onChange={(e) => setText(e.target.value)} rows={4} required /><Counter text={text} /></Field>
        <label className="flex cursor-pointer flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed p-6 text-center hover:bg-muted/50">
          <FileUp className="size-6 text-muted-foreground" />
          <span className="text-sm font-medium">{file ? file.name : "Upload numbers (CSV or TXT)"}</span>
          <span className="text-xs text-muted-foreground">One number per line or the first numeric column. Up to 2 million numbers; duplicates are removed.</span>
          <input type="file" accept=".csv,.txt,text/csv,text/plain" className="hidden" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        </label>
        <Button type="submit" disabled={!file || create.isPending}>{create.isPending ? "Uploading…" : "Start campaign"}</Button>
      </form>
      <div className="xl:col-span-3">
        {!list.data?.length ? <Empty title="No campaigns yet" /> : (
          <Table>
            <THead><tr><TH>Campaign</TH><TH>Status</TH><TH>Progress</TH><TH className="text-right">Accepted</TH><TH className="text-right">Rejected</TH></tr></THead>
            <TBody>
              {list.data.map((c) => {
                const p = c.total ? (c.processed / c.total) * 100 : 0;
                return (
                  <TR key={c.id}>
                    <TD><p className="font-medium">{c.name || `Campaign #${c.id}`}</p><p className="text-xs text-muted-foreground">{c.client_name} · {dateTime(c.created_at)}</p>{c.last_error && <p className="text-xs text-danger">{c.last_error}</p>}</TD>
                    <TD><StatusBadge status={c.status} /></TD>
                    <TD className="w-48"><div className="h-2 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${p}%` }} /></div><p className="mt-1 text-xs text-muted-foreground tabular">{num(c.processed)} / {num(c.total)}</p></TD>
                    <TD className="text-right tabular">{num(c.accepted)}</TD>
                    <TD className="text-right tabular">{num(c.rejected)}</TD>
                  </TR>
                );
              })}
            </TBody>
          </Table>
        )}
      </div>
    </div>
  );
}

export function SendPage() {
  const [tab, setTab] = React.useState("single");
  return (
    <>
      <PageHeader title="Send & campaigns" description="Send a single message or upload a list for a bulk campaign" />
      <Tabs value={tab} onValueChange={setTab} items={[{ value: "single", label: "Single message" }, { value: "bulk", label: "Bulk campaign" }]} className="mb-4" />
      <Card>
        <CardHeader title={tab === "single" ? "New message" : "Bulk campaigns"} description={tab === "bulk" ? "Large lists are processed in the background at the account's speed limit, and resume automatically after a restart." : undefined} />
        <CardBody>{tab === "single" ? <QuickSend /> : <Campaigns />}</CardBody>
      </Card>
    </>
  );
}
