import * as React from "react";
import { Check, Copy } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Card, CardBody } from "@/components/ui/card";

function Code({ children }: { children: string }) {
  const [copied, setCopied] = React.useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(children);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error("Copy failed");
    }
  };
  return (
    <div className="group relative">
      <pre className="overflow-x-auto rounded-lg bg-zinc-950 p-4 text-xs leading-relaxed text-zinc-100 dark:bg-black/60">{children}</pre>
      <button
        onClick={copy}
        className="absolute right-2 top-2 rounded-md bg-white/10 p-1.5 text-zinc-300 opacity-0 transition hover:bg-white/20 group-hover:opacity-100 focus:opacity-100"
        aria-label="Copy"
      >
        {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  );
}

function Endpoint({ method, path }: { method: string; path: string }) {
  return (
    <div className="flex items-center gap-2 font-mono text-sm">
      <Badge tone={method === "GET" ? "success" : "primary"}>{method}</Badge>
      <span>{path}</span>
    </div>
  );
}

function Section({ id, title, children }: { id: string; title: string; children: React.ReactNode }) {
  return (
    <Card id={id} className="scroll-mt-20">
      <CardBody className="space-y-3 text-sm leading-relaxed">
        <h2 className="text-lg font-semibold">{title}</h2>
        {children}
      </CardBody>
    </Card>
  );
}

const sections = [
  ["auth", "Authentication"],
  ["send", "Send a message"],
  ["batch", "Send in bulk"],
  ["status", "Message status"],
  ["balance", "Balance"],
  ["dlr", "Delivery reports"],
  ["mo", "Incoming SMS"],
  ["errors", "Errors"],
  ["smpp", "SMPP"],
  ["legacy", "Old HTTP API"],
] as const;

export function ApiDocsPage() {
  const base = window.location.origin;
  const smppHost = window.location.hostname;
  return (
    <>
      <PageHeader title="API documentation" description="Send SMS and receive delivery reports over HTTP or SMPP" />
      <div className="grid gap-6 lg:grid-cols-[200px_1fr]">
        <nav className="hidden lg:block">
          <div className="sticky top-20 space-y-1 text-sm">
            {sections.map(([id, label]) => (
              <a key={id} href={`#${id}`} className="block rounded-md px-3 py-1.5 text-muted-foreground hover:bg-muted hover:text-foreground">
                {label}
              </a>
            ))}
          </div>
        </nav>
        <div className="min-w-0 space-y-6">
          <Section id="auth" title="Authentication">
            <p>
              Create an API key under <b>Accounts &amp; API keys</b> for an HTTP account, and send it in the <code>Authorization</code> header.
              Keys are shown once; revoke a key at any time. If the account has an IP allowlist, requests from other addresses are refused.
            </p>
            <Code>{`Authorization: Bearer YOUR_API_KEY`}</Code>
            <p>Base URL: <code>{base}/api/v1</code>. Requests and responses are JSON.</p>
          </Section>

          <Section id="send" title="Send a message">
            <Endpoint method="POST" path="/api/v1/messages" />
            <p>
              <code>to</code> is the number in international format. <code>from</code> is your sender ID. Long and Unicode texts are split
              automatically and charged per part. Send an <code>Idempotency-Key</code> header (or <code>client_ref</code>), and a retried request
              returns the first message instead of sending it twice.
            </p>
            <Code>{`curl ${base}/api/v1/messages \\
  -H "Authorization: Bearer YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -H "Idempotency-Key: order-1042" \\
  -d '{"to": "923001234567", "from": "MyBrand", "text": "Your code is 4821"}'`}</Code>
            <p>Response <code>202 Accepted</code>:</p>
            <Code>{`{"id": "0192f3c4-…", "status": "queued", "parts": 1, "price": "0.0085"}`}</Code>
          </Section>

          <Section id="batch" title="Send in bulk">
            <p>Up to 1,000 messages per request. Each result has the <code>index</code> of its message; one invalid message does not stop the others.</p>
            <Code>{`{"messages": [
  {"to": "923001234567", "from": "MyBrand", "text": "Hello Ali", "client_ref": "c-1"},
  {"to": "971501234567", "from": "MyBrand", "text": "Hello Sara", "client_ref": "c-2"}
]}`}</Code>
            <Code>{`{"results": [
  {"index": 0, "id": "0192f3c4-…", "status": "queued", "parts": 1, "price": "0.0085"},
  {"index": 1, "error": "no_rate", "message": "no rate configured for this destination"}
]}`}</Code>
            <p>For larger lists, upload a CSV or Excel file under <b>Send &amp; campaigns</b>.</p>
          </Section>

          <Section id="status" title="Message status">
            <Endpoint method="GET" path="/api/v1/messages/{id}" />
            <Code>{`{"id": "0192f3c4-…", "to": "923001234567", "from": "MyBrand", "status": "delivered",
 "dlr_status": "DELIVRD", "error": "000", "parts": 1, "price": 0.0085, "client_ref": "c-1",
 "created_at": "…", "sent_at": "…", "done_at": "…"}`}</Code>
            <p>
              Status values: <code>queued</code>, <code>sent</code>, <code>delivered</code>, <code>undelivered</code>, <code>failed</code>
              (not sent; the charge is refunded), <code>expired</code>, <code>unknown</code>.
            </p>
          </Section>

          <Section id="balance" title="Balance">
            <Endpoint method="GET" path="/api/v1/balance" />
            <Code>{`{"balance": 904.34, "currency": "USD", "credit_limit": 0, "billing_type": "prepaid"}`}</Code>
          </Section>

          <Section id="dlr" title="Delivery reports (webhook)">
            <p>
              Set a DLR webhook URL on your client profile. Each final status is sent as a JSON <code>POST</code> as soon as it arrives. Answer
              with any <code>2xx</code>; other answers and timeouts are retried with back-off, so handle duplicates by <code>id</code>.
            </p>
            <Code>{`{"id": "0192f3c4-…", "client_ref": "c-1", "to": "923001234567", "from": "MyBrand",
 "status": "delivered", "dlr_status": "DELIVRD", "error": "000", "parts": 1,
 "done_at": "2026-10-10T09:15:02Z"}`}</Code>
          </Section>

          <Section id="mo" title="Incoming SMS (webhook)">
            <p>Replies to your numbers or keywords are forwarded to your MO webhook URL (or to your SMPP receiver bind):</p>
            <Code>{`{"type": "mo", "id": "0192f3d1-…", "from": "923001234567", "to": "8282",
 "text": "YES", "received_at": "2026-10-10T09:16:40Z"}`}</Code>
            <p>Replies starting with STOP, UNSUBSCRIBE, END or CANCEL also add the sender to your blacklist.</p>
          </Section>

          <Section id="errors" title="Errors">
            <p>Errors return a JSON body <code>{`{"error": "code", "message": "…"}`}</code>:</p>
            <div className="overflow-x-auto">
              <table className="w-full text-left text-xs">
                <thead className="text-muted-foreground"><tr><th className="py-1.5 pr-4">HTTP</th><th className="pr-4">error</th><th>Meaning</th></tr></thead>
                <tbody className="divide-y">
                  {[
                    ["401", "unauthorized", "Missing, wrong or revoked API key, or IP not allowed"],
                    ["402", "insufficient_balance", "Top up or ask for a credit limit"],
                    ["429", "throttled", "Above your messages-per-second limit; retry after a short pause"],
                    ["422", "invalid_destination", "Not a valid international number"],
                    ["422", "no_rate / no_route", "Destination not enabled for your account"],
                    ["422", "blacklisted", "The number is on your or the global blacklist"],
                    ["422", "blocked", "Blocked by a content rule"],
                    ["422", "empty_message", "Text is empty"],
                    ["400", "invalid / too_many", "Malformed JSON, or more than 1,000 messages"],
                  ].map(([h, c, m]) => (
                    <tr key={c}><td className="py-1.5 pr-4 font-mono">{h}</td><td className="pr-4 font-mono">{c}</td><td>{m}</td></tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Section>

          <Section id="smpp" title="SMPP">
            <p>
              SMPP 3.4 on <code>{smppHost}</code> port <code>2775</code>, as transmitter, receiver or transceiver, with the system ID and
              password of an SMPP account. <code>submit_sm_resp</code> returns the message ID used in delivery receipts. Delivery receipts
              come as <code>deliver_sm</code> in the standard format (<code>id:… stat:DELIVRD err:000</code>) and are kept until you bind
              again if you are offline. Above your TPS limit you get <code>ESME_RTHROTTLED</code> (0x58): slow down and retry.
            </p>
          </Section>

          <Section id="legacy" title="Old HTTP API">
            <p>
              Systems built for the previous gateway keep working: send the same parameters (<code>username</code>, <code>password</code>,{" "}
              <code>to</code>, <code>from</code>, <code>message</code>, <code>messageid</code>, or <code>action=balance</code>) by GET or POST to:
            </p>
            <Code>{`${base}/api/legacy`}</Code>
            <p>New integrations should use API v1 above.</p>
          </Section>
        </div>
      </div>
    </>
  );
}
