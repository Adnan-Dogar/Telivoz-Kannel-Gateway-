import { useState } from "react";
import { Text, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { api, qs, type Overview } from "@/lib/api";
import { compact, delta, duration, money, pct } from "@/lib/format";
import { useSession } from "@/lib/session";
import { AreaChart, Card, ErrorBox, Loading, Screen, SectionTitle, Segmented, Stat, useColors } from "@/components/ui";

type Range = "24h" | "7d" | "30d";
const hours: Record<Range, number> = { "24h": 24, "7d": 24 * 7, "30d": 24 * 30 };

export default function Dashboard() {
  const { me } = useSession();
  const c = useColors();
  const [range, setRange] = useState<Range>("24h");
  const q = useQuery({
    queryKey: ["overview", range],
    queryFn: () => {
      const to = new Date();
      const from = new Date(to.getTime() - hours[range] * 3600_000);
      return api.get<Overview>(`/api/stats/overview${qs({ from: from.toISOString(), to: to.toISOString() })}`);
    },
    refetchInterval: 30_000,
  });
  const t = q.data?.totals;
  const p = q.data?.previous;
  const finals = t ? t.delivered + t.undelivered : 0;
  const margin = t ? t.revenue - t.cost : 0;
  const staff = me?.role !== "client";
  const failed = t ? t.failed + t.undelivered : 0;
  const failedDelta = t && p ? delta(failed, p.failed + p.undelivered) : null;
  const failedTrend = failedDelta ? { ...failedDelta, good: !failedDelta.up } : null; // fewer failures is good

  return (
    <Screen
      title="Dashboard"
      subtitle={`Hello ${me?.name?.split(" ")[0] ?? ""}${me?.client_name ? ` · ${me.client_name}` : ""}`}
      refreshing={q.isRefetching}
      onRefresh={() => q.refetch()}
    >
      <Segmented<Range>
        value={range}
        onChange={setRange}
        options={[
          { value: "24h", label: "24 hours" },
          { value: "7d", label: "7 days" },
          { value: "30d", label: "30 days" },
        ]}
      />
      {q.isLoading ? <Loading /> : null}
      {q.error ? <View className="mt-4"><ErrorBox error={q.error} onRetry={() => q.refetch()} /></View> : null}
      {t && p ? (
        <>
          <View className="mt-4 flex-row gap-3">
            <Stat label="Messages" value={compact(t.submitted)} trend={delta(t.submitted, p.submitted)} hint={p.submitted ? "vs previous" : undefined} />
            <Stat
              label="Delivery rate"
              value={pct(t.delivered, finals)}
              tone={finals && t.delivered / finals < 0.8 ? "warn" : "good"}
              hint={`${compact(t.delivered)} delivered`}
            />
          </View>
          <View className="mt-3 flex-row gap-3">
            <Stat label={staff ? "Revenue" : "Spent"} value={money(t.revenue)} trend={delta(t.revenue, p.revenue)} />
            {staff ? (
              <Stat label="Margin" value={money(margin)} tone={margin < 0 ? "bad" : "default"} hint={pct(margin, t.revenue)} />
            ) : (
              <Stat label="Avg. DLR time" value={duration(t.avg_dlr_ms)} />
            )}
          </View>
          <View className="mt-3 flex-row gap-3">
            <Stat label="Failed" value={compact(failed)} tone={failed > 0 ? "bad" : "default"} trend={failedTrend} />
            <Stat label="Rejected" value={compact(t.rejected)} tone={t.rejected > 0 ? "warn" : "default"} hint="at submit" />
          </View>

          <SectionTitle>Traffic</SectionTitle>
          <Card>
            <AreaChart
              series={[
                { values: q.data!.series.map((s) => s.submitted), color: c.brand },
                { values: q.data!.series.map((s) => s.delivered), color: c.green },
              ]}
            />
            <View className="mt-3 flex-row gap-4">
              <Legend color={c.brand} label="Submitted" />
              <Legend color={c.green} label="Delivered" />
            </View>
          </Card>

          {staff ? (
            <>
              <SectionTitle>Revenue</SectionTitle>
              <Card>
                <AreaChart height={90} series={[{ values: q.data!.series.map((s) => s.revenue), color: c.amber }]} />
                <Text className="mt-2 text-xs text-zinc-500 dark:text-zinc-400">
                  Cost {money(t.cost)} · {compact(t.parts)} parts · avg. DLR {duration(t.avg_dlr_ms)}
                </Text>
              </Card>
            </>
          ) : null}
        </>
      ) : null}
    </Screen>
  );
}

function Legend({ color, label }: { color: string; label: string }) {
  return (
    <View className="flex-row items-center gap-1.5">
      <View style={{ backgroundColor: color }} className="h-2 w-2 rounded-full" />
      <Text className="text-xs text-zinc-500 dark:text-zinc-400">{label}</Text>
    </View>
  );
}
