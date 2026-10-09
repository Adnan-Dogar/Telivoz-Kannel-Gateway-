import * as React from "react";
import { useQueryClient } from "@tanstack/react-query";
import { BarChart3, MessageSquareText, Route, ShieldCheck } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import type { Me } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";

export function LoginPage() {
  const qc = useQueryClient();
  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [code, setCode] = React.useState("");
  const [needCode, setNeedCode] = React.useState(false);
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const me = await api.post<Me>("/api/auth/login", { email, password, code });
      qc.setQueryData(["me"], me);
    } catch (err) {
      if (err instanceof ApiError && err.code === "totp_required") {
        setNeedCode(true);
        setError("");
        return;
      }
      setError(err instanceof ApiError ? err.message : "Cannot reach the server");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="grid min-h-full lg:grid-cols-2">
      <div className="relative hidden overflow-hidden bg-gradient-to-br from-[oklch(0.35_0.2_277)] via-[oklch(0.42_0.22_285)] to-[oklch(0.5_0.2_300)] p-12 text-white lg:flex lg:flex-col">
        <div className="absolute -right-24 -top-24 size-96 rounded-full bg-white/10 blur-3xl" />
        <div className="absolute -bottom-32 left-10 size-96 rounded-full bg-fuchsia-400/20 blur-3xl" />
        <div className="relative flex items-center gap-3">
          <div className="grid size-10 place-items-center rounded-xl bg-white/15 backdrop-blur">
            <MessageSquareText className="size-5" />
          </div>
          <span className="text-lg font-semibold">Telivoz Gateway</span>
        </div>
        <div className="relative mt-auto max-w-md">
          <h2 className="text-4xl font-semibold leading-tight tracking-tight">Every message routed, billed and tracked in real time.</h2>
          <ul className="mt-8 space-y-4 text-white/85">
            {[
              [Route, "Smart routing: LCR, failover and sender-ID rules"],
              [BarChart3, "Live traffic, delivery rates and margins"],
              [ShieldCheck, "Exactly-once billing and reliable DLRs"],
            ].map(([Icon, text], i) => (
              <li key={i} className="flex items-center gap-3">
                <span className="grid size-8 place-items-center rounded-lg bg-white/15">
                  {React.createElement(Icon as React.ElementType, { className: "size-4" })}
                </span>
                {text as string}
              </li>
            ))}
          </ul>
        </div>
      </div>
      <div className="flex items-center justify-center p-6">
        <form onSubmit={submit} className="w-full max-w-sm space-y-6">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">Sign in</h1>
            <p className="mt-1 text-sm text-muted-foreground">Use your Telivoz portal account.</p>
          </div>
          <div className="space-y-4">
            <Field label="Email">
              <Input type="email" autoComplete="username" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
            </Field>
            <Field label="Password">
              <Input type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
            </Field>
            {needCode && (
              <Field label="Authenticator code" hint="Two-factor login is on for this account.">
                <Input inputMode="numeric" autoComplete="one-time-code" autoFocus maxLength={6} required value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} placeholder="123456" className="tracking-[0.4em]" />
              </Field>
            )}
          </div>
          {error && <p className="rounded-md bg-danger/10 px-3 py-2 text-sm text-danger">{error}</p>}
          <Button type="submit" className="w-full" size="lg" disabled={busy}>
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </div>
    </div>
  );
}
