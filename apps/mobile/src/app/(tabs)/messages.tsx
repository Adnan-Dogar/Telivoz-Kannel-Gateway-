import { useState } from "react";
import { Pressable, Text, TextInput, View } from "react-native";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react-native";
import { api, qs, type Message } from "@/lib/api";
import { ago, duration, money } from "@/lib/format";
import { Badge, Card, Empty, ErrorBox, Loading, Screen, statusTone, useColors } from "@/components/ui";

const filters = ["", "delivered", "undelivered", "failed", "queued"] as const;

export default function Messages() {
  const c = useColors();
  const [text, setText] = useState("");
  const [term, setTerm] = useState("");
  const [status, setStatus] = useState<(typeof filters)[number]>("");
  const [open, setOpen] = useState<string | null>(null);
  const q = useQuery({
    queryKey: ["messages", term, status],
    queryFn: () => api.get<Message[]>(`/api/messages${qs({ q: term, status, limit: 50 })}`),
  });

  return (
    <Screen title="Messages" subtitle="Last 7 days, newest first" refreshing={q.isRefetching} onRefresh={() => q.refetch()}>
      <View className="flex-row items-center rounded-xl border border-zinc-300 bg-white px-3 dark:border-zinc-700 dark:bg-zinc-900">
        <Search color={c.muted} size={18} />
        <TextInput
          value={text}
          onChangeText={setText}
          onSubmitEditing={() => setTerm(text.trim())}
          returnKeyType="search"
          placeholder="Number, sender, text or message ID"
          placeholderTextColor={c.muted}
          autoCapitalize="none"
          autoCorrect={false}
          className="flex-1 px-2 py-3 text-base text-zinc-900 dark:text-zinc-50"
        />
      </View>
      <View className="mt-3 flex-row flex-wrap gap-2">
        {filters.map((f) => (
          <Pressable
            key={f || "all"}
            onPress={() => setStatus(f)}
            className={`rounded-full border px-3 py-1.5 ${status === f ? "border-indigo-600 bg-indigo-600" : "border-zinc-300 bg-white dark:border-zinc-700 dark:bg-zinc-900"}`}
          >
            <Text className={`text-xs font-semibold capitalize ${status === f ? "text-white" : "text-zinc-600 dark:text-zinc-300"}`}>{f || "All"}</Text>
          </Pressable>
        ))}
      </View>

      <View className="mt-4 gap-2">
        {q.isLoading ? <Loading /> : null}
        {q.error ? <ErrorBox error={q.error} onRetry={() => q.refetch()} /> : null}
        {q.data?.length === 0 ? <Empty>No messages match.</Empty> : null}
        {q.data?.map((m) => (
          <Pressable key={m.id} onPress={() => setOpen(open === m.id ? null : m.id)}>
            <Card className="p-3.5">
              <View className="flex-row items-center justify-between">
                <Text className="font-mono text-[15px] font-semibold text-zinc-900 dark:text-zinc-50">
                  {m.direction === "mo" ? `← ${m.source}` : m.destination}
                </Text>
                <Badge tone={statusTone(m.status)}>{m.status}</Badge>
              </View>
              <Text className="mt-1 text-sm text-zinc-600 dark:text-zinc-300" numberOfLines={open === m.id ? undefined : 2}>
                {m.body || "(binary content)"}
              </Text>
              <Text className="mt-1.5 text-xs text-zinc-500 dark:text-zinc-400">
                {[m.direction === "mo" ? `to ${m.destination}` : `from ${m.source}`, m.client_name, ago(m.created_at)].filter(Boolean).join(" · ")}
              </Text>
              {open === m.id ? (
                <View className="mt-3 gap-1 border-t border-zinc-100 pt-3 dark:border-zinc-800">
                  <Row k="Message ID" v={m.id} />
                  <Row k="Network" v={[m.country_iso, m.network_name].filter(Boolean).join(" · ") || "–"} />
                  <Row k="Vendor" v={m.connection_name ?? "–"} />
                  <Row k="Parts / price" v={`${m.parts} · ${money(m.price, 4)}`} />
                  <Row k="DLR" v={m.dlr_status ? `${m.dlr_status}${m.dlr_error && m.dlr_status !== "DELIVRD" ? ` (err ${m.dlr_error})` : ""} in ${duration(m.dlr_ms)}` : "–"} />
                  {m.error ? <Row k="Error" v={m.error} bad /> : null}
                </View>
              ) : null}
            </Card>
          </Pressable>
        ))}
      </View>
    </Screen>
  );
}

function Row({ k, v, bad }: { k: string; v: string; bad?: boolean }) {
  return (
    <View className="flex-row justify-between gap-4">
      <Text className="text-xs text-zinc-500 dark:text-zinc-400">{k}</Text>
      <Text selectable className={`flex-1 text-right text-xs ${bad ? "text-red-600 dark:text-red-400" : "text-zinc-800 dark:text-zinc-200"}`}>
        {v}
      </Text>
    </View>
  );
}
