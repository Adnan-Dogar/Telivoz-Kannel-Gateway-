import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { duration, num, rate } from "@/lib/format";
import { PageHeader } from "@/components/page";
import { ResourcePage } from "@/components/resource";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Empty, Skeleton } from "@/components/ui/misc";
import { TBody, TD, TH, THead, TR, Table } from "@/components/ui/table";

interface QualityRow {
  connection_id: number;
  connection_name: string;
  country_iso: string;
  country: string;
  sent: number;
  delivered: number;
  final: number;
  failed: number;
  avg_dlr_ms: number;
  avg_cost: number;
  dlr_rate: number;
  score: number;
  trusted: boolean;
}

function ScoreBar({ score, trusted }: { score: number; trusted: boolean }) {
  const tone = !trusted ? "bg-muted-foreground/40" : score >= 90 ? "bg-success" : score >= 75 ? "bg-warning" : "bg-danger";
  return (
    <div className="flex items-center gap-2">
      <div className="h-2 w-24 overflow-hidden rounded-full bg-muted">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${Math.max(2, score)}%` }} />
      </div>
      <span className="tabular w-10 text-right font-medium">{score.toFixed(0)}</span>
    </div>
  );
}

export function VendorQualityPage() {
  const [filter, setFilter] = React.useState("");
  const q = useQuery({
    queryKey: ["vendor-quality"],
    queryFn: () => api.get<{ rows: QualityRow[]; min_samples: number; balanced_tolerance: number }>("/api/stats/vendor-quality"),
    refetchInterval: 60_000,
  });
  const term = filter.trim().toLowerCase();
  const rows = (q.data?.rows ?? []).filter(
    (r) => !term || r.connection_name.toLowerCase().includes(term) || r.country.toLowerCase().includes(term) || r.country_iso.toLowerCase() === term,
  );
  // Best trusted score per country, to highlight the leader.
  const best = new Map<string, number>();
  for (const r of rows) if (r.trusted) best.set(r.country_iso, Math.max(best.get(r.country_iso) ?? 0, r.score));

  return (
    <>
      <PageHeader
        title="Vendor quality"
        description={`Delivery performance per vendor connection and country, last 24 hours. Used by the "Best quality" and "Balanced" route policies.`}
        actions={<Input className="w-56" placeholder="Filter vendor or country…" value={filter} onChange={(e) => setFilter(e.target.value)} />}
      />
      <Card>
        {q.isLoading ? (
          <div className="p-4"><Skeleton className="h-40" /></div>
        ) : !rows.length ? (
          <Empty title="No vendor traffic in the last 24 hours" />
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>Country</TH><TH>Connection</TH><TH>Score</TH><TH className="text-right">Delivery rate</TH>
                <TH className="text-right">Sent</TH><TH className="text-right">Failed</TH><TH className="text-right">Avg DLR time</TH><TH className="text-right">Avg cost</TH>
              </tr>
            </THead>
            <TBody>
              {rows.map((r) => (
                <TR key={`${r.connection_id}-${r.country_iso}`}>
                  <TD><span className="font-medium">{r.country}</span> <span className="text-muted-foreground">{r.country_iso}</span></TD>
                  <TD>
                    <span className="font-medium">{r.connection_name}</span>
                    {r.trusted && best.get(r.country_iso) === r.score && <Badge tone="success" className="ml-2">Best</Badge>}
                    {!r.trusted && <Badge className="ml-2" title={`Fewer than ${q.data?.min_samples} delivery reports: neutral score`}>Learning</Badge>}
                  </TD>
                  <TD><ScoreBar score={r.score} trusted={r.trusted} /></TD>
                  <TD className="tabular text-right">{r.final ? `${(r.dlr_rate * 100).toFixed(1)}%` : "—"}</TD>
                  <TD className="tabular text-right">{num(r.sent)}</TD>
                  <TD className="tabular text-right">{num(r.failed)}</TD>
                  <TD className="tabular text-right">{duration(r.avg_dlr_ms)}</TD>
                  <TD className="tabular text-right">{rate(r.avg_cost)}</TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </Card>
      <p className="mt-3 text-xs text-muted-foreground">
        Score = delivery rate in percent, minus up to 10 points for slow delivery reports (1 point per 6 seconds). Connections with fewer than{" "}
        {q.data?.min_samples ?? 50} reports get a neutral 50 so new vendors still receive traffic. Scores refresh every minute.
      </p>
    </>
  );
}

export function BrandingPage() {
  return (
    <ResourcePage
      title="Branding"
      description="White-label the portal per domain: resellers get their own name, logo and colour on their own domain. The entry without a domain is the default."
      path="branding"
      noun="Brand"
      canWrite
      search="Search domain or name…"
      columns={[
        {
          key: "name",
          header: "Brand",
          render: (r) => (
            <div className="flex items-center gap-2">
              {r.logo ? <img src={String(r.logo)} alt="" className="size-7 rounded object-contain" /> : <span className="size-7 rounded" style={{ background: String(r.primary_color || "var(--primary)") }} />}
              <span className="font-medium">{String(r.name)}</span>
            </div>
          ),
        },
        { key: "domain", header: "Domain", render: (r) => (r.domain ? <span className="font-mono">{String(r.domain)}</span> : <Badge tone="primary">Default</Badge>) },
        { key: "primary_color", header: "Colour", render: (r) => (r.primary_color ? <span className="inline-flex items-center gap-2 font-mono text-xs"><span className="size-4 rounded" style={{ background: String(r.primary_color) }} />{String(r.primary_color)}</span> : "—") },
        { key: "support_email", header: "Support email", render: (r) => String(r.support_email ?? "") || "—" },
      ]}
      fields={[
        { name: "domain", label: "Domain", placeholder: "sms.reseller.com", hint: "Lower case, without https://. Leave empty for the default brand." },
        { name: "name", label: "Portal name", required: true },
        { name: "tagline", label: "Tagline", placeholder: "SMS Gateway" },
        { name: "primary_color", label: "Brand colour", type: "color" },
        { name: "logo", label: "Logo", type: "image", hint: "PNG, JPEG, WebP or SVG, under 280 KB. Square works best.", span: 2 },
        { name: "support_email", label: "Support email" },
      ]}
    />
  );
}
