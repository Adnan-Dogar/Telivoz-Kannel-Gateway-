import { Text, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { api, type Live } from "@/lib/api";
import { num } from "@/lib/format";
import { isStaff, useSession } from "@/lib/session";
import { AreaChart, Card, ErrorBox, Loading, Screen, SectionTitle, Stat, useColors } from "@/components/ui";

const avg = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : 0);

export default function LiveScreen() {
  const { me } = useSession();
  const c = useColors();
  const q = useQuery({ queryKey: ["live"], queryFn: () => api.get<Live>("/api/stats/live"), refetchInterval: 2_000 });
  const series = q.data?.series ?? [];
  const last10 = series.slice(-10);
  const conns = Object.values(q.data?.connections ?? {});
  const bound = conns.filter((cn) => cn.binds.some((b) => b.state === "bound")).length;

  return (
    <Screen title="Live traffic" subtitle="Messages per second, refreshed every 2 s" refreshing={q.isRefetching} onRefresh={() => q.refetch()}>
      {q.isLoading ? <Loading /> : null}
      {q.error ? <ErrorBox error={q.error} onRetry={() => q.refetch()} /> : null}
      {q.data ? (
        <>
          <View className="flex-row gap-3">
            <Stat label="In / s" value={avg(last10.map((s) => s.in)).toFixed(1)} hint="last 10 s" />
            <Stat label="Out / s" value={avg(last10.map((s) => s.out)).toFixed(1)} hint="to vendors" />
            <Stat label="DLR / s" value={avg(last10.map((s) => s.dlr)).toFixed(1)} />
          </View>
          <SectionTitle>Last 2 minutes</SectionTitle>
          <Card>
            <AreaChart
              height={150}
              series={[
                { values: series.map((s) => s.in), color: c.brand },
                { values: series.map((s) => s.out), color: c.amber },
                { values: series.map((s) => s.dlr), color: c.green },
              ]}
            />
            <View className="mt-3 flex-row gap-4">
              <Dot color={c.brand} label="Received" />
              <Dot color={c.amber} label="Sent" />
              <Dot color={c.green} label="DLRs" />
            </View>
          </Card>
          {isStaff(me) ? (
            <>
              <SectionTitle>System</SectionTitle>
              <View className="flex-row gap-3">
                <Stat label="Send queue" value={num(q.data.queue)} tone={(q.data.queue ?? 0) > 5000 ? "warn" : "default"} />
                <Stat label="DLRs waiting" value={num(q.data.dlr_outbox)} tone={(q.data.dlr_outbox ?? 0) > 1000 ? "warn" : "default"} hint="for clients" />
              </View>
              <View className="mt-3 flex-row gap-3">
                <Stat label="Vendors up" value={`${bound}/${conns.length}`} tone={bound < conns.filter((x) => x.enabled).length ? "bad" : "good"} />
                <Stat label="Client binds" value={num(q.data.client_binds?.length)} />
              </View>
              {q.data.client_binds?.length ? (
                <>
                  <SectionTitle>Bound clients</SectionTitle>
                  <Card className="py-1">
                    {q.data.client_binds.map((b, i) => (
                      <View key={i} className={`flex-row items-center justify-between py-2.5 ${i ? "border-t border-zinc-100 dark:border-zinc-800" : ""}`}>
                        <Text className="font-semibold text-zinc-900 dark:text-zinc-100">{b.system_id}</Text>
                        <Text className="text-xs text-zinc-500 dark:text-zinc-400">
                          {b.mode} · {b.remote}
                        </Text>
                      </View>
                    ))}
                  </Card>
                </>
              ) : null}
            </>
          ) : null}
        </>
      ) : null}
    </Screen>
  );
}

function Dot({ color, label }: { color: string; label: string }) {
  return (
    <View className="flex-row items-center gap-1.5">
      <View style={{ backgroundColor: color }} className="h-2 w-2 rounded-full" />
      <Text className="text-xs text-zinc-500 dark:text-zinc-400">{label}</Text>
    </View>
  );
}
