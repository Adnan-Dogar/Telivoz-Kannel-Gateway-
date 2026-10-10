import { Alert, Pressable, Text, View } from "react-native";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RotateCw } from "lucide-react-native";
import * as Haptics from "expo-haptics";
import { api, type ConnectionStatus, type Live } from "@/lib/api";
import { ago, num } from "@/lib/format";
import { Badge, Card, Empty, ErrorBox, Loading, Screen, useColors, type Tone } from "@/components/ui";

function state(c: ConnectionStatus): { label: string; tone: Tone } {
  if (!c.enabled) return { label: "disabled", tone: "gray" };
  const up = c.binds.filter((b) => b.state === "bound").length;
  if (up === c.binds.length && up > 0) return { label: "online", tone: "green" };
  if (up > 0) return { label: "degraded", tone: "amber" };
  return { label: "down", tone: "red" };
}

export default function Connections() {
  const c = useColors();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["live"], queryFn: () => api.get<Live>("/api/stats/live"), refetchInterval: 3_000 });
  const restart = useMutation({
    mutationFn: (id: number) => api.post(`/api/connections/${id}/restart`),
    onSuccess: () => {
      Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      qc.invalidateQueries({ queryKey: ["live"] });
    },
    onError: (e) => Alert.alert("Restart failed", e instanceof Error ? e.message : String(e)),
  });
  const conns = Object.values(q.data?.connections ?? {}).sort((a, b) => a.name.localeCompare(b.name));

  const confirmRestart = (cn: ConnectionStatus) =>
    Alert.alert(`Restart ${cn.name}?`, "Open binds are closed and reconnected. Messages in flight are kept.", [
      { text: "Cancel", style: "cancel" },
      { text: "Restart", style: "destructive", onPress: () => restart.mutate(cn.id) },
    ]);

  return (
    <Screen title="Connections" subtitle="Vendor SMPP binds" refreshing={q.isRefetching} onRefresh={() => q.refetch()}>
      {q.isLoading ? <Loading /> : null}
      {q.error ? <ErrorBox error={q.error} onRetry={() => q.refetch()} /> : null}
      {q.data && conns.length === 0 ? <Empty>No vendor connections configured.</Empty> : null}
      <View className="gap-3">
        {conns.map((cn) => {
          const st = state(cn);
          return (
            <Card key={cn.id}>
              <View className="flex-row items-start justify-between">
                <View className="flex-1 pr-3">
                  <Text className="text-base font-semibold text-zinc-900 dark:text-zinc-50">{cn.name}</Text>
                  <View className="mt-1.5 flex-row gap-2">
                    <Badge tone={st.tone}>{st.label}</Badge>
                    {cn.in_flight > 0 ? <Badge tone="blue">{`${cn.in_flight} in flight`}</Badge> : null}
                  </View>
                </View>
                <Pressable
                  onPress={() => confirmRestart(cn)}
                  disabled={restart.isPending}
                  className="h-9 w-9 items-center justify-center rounded-full bg-zinc-100 active:bg-zinc-200 dark:bg-zinc-800"
                  accessibilityLabel={`Restart ${cn.name}`}
                >
                  <RotateCw color={c.text} size={16} />
                </Pressable>
              </View>
              <View className="mt-3 flex-row gap-6">
                <Metric label="Sent" value={num(cn.sent)} />
                <Metric label="Errors" value={num(cn.errors)} bad={cn.errors > 0} />
                <Metric label="Binds" value={`${cn.binds.filter((b) => b.state === "bound").length}/${cn.binds.length}`} />
              </View>
              {cn.binds.length ? (
                <View className="mt-3 gap-1.5 border-t border-zinc-100 pt-3 dark:border-zinc-800">
                  {cn.binds.map((b) => (
                    <View key={b.index} className="flex-row items-center gap-2">
                      <View className={`h-2 w-2 rounded-full ${b.state === "bound" ? "bg-emerald-500" : b.state === "connecting" ? "bg-amber-500" : "bg-red-500"}`} />
                      <Text className="text-xs text-zinc-600 dark:text-zinc-300">
                        Bind {b.index + 1} · {b.state} {b.since ? `· ${ago(b.since)}` : ""}
                      </Text>
                      {b.error ? (
                        <Text className="flex-1 text-xs text-red-600 dark:text-red-400" numberOfLines={1}>
                          {b.error}
                        </Text>
                      ) : null}
                    </View>
                  ))}
                </View>
              ) : null}
            </Card>
          );
        })}
      </View>
    </Screen>
  );
}

function Metric({ label, value, bad }: { label: string; value: string; bad?: boolean }) {
  return (
    <View>
      <Text className="text-[11px] uppercase tracking-wide text-zinc-500 dark:text-zinc-400">{label}</Text>
      <Text className={`text-base font-semibold ${bad ? "text-red-600 dark:text-red-400" : "text-zinc-900 dark:text-zinc-100"}`}>{value}</Text>
    </View>
  );
}
