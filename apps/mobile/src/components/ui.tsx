import type { ReactNode } from "react";
import { ActivityIndicator, Pressable, RefreshControl, ScrollView, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useColorScheme } from "nativewind";
import Svg, { Defs, LinearGradient, Path, Stop } from "react-native-svg";

export function useColors() {
  const { colorScheme } = useColorScheme();
  const dark = colorScheme === "dark";
  return {
    dark,
    text: dark ? "#F4F4F5" : "#18181B",
    muted: dark ? "#A1A1AA" : "#71717A",
    brand: dark ? "#818CF8" : "#4F46E5",
    green: dark ? "#34D399" : "#059669",
    red: dark ? "#F87171" : "#DC2626",
    amber: dark ? "#FBBF24" : "#D97706",
    card: dark ? "#18181B" : "#FFFFFF",
    border: dark ? "#27272A" : "#E4E4E7",
    bg: dark ? "#09090B" : "#F4F4F5",
  };
}

/** Page with title, pull-to-refresh and safe-area padding. */
export function Screen({
  title,
  subtitle,
  right,
  children,
  refreshing,
  onRefresh,
}: {
  title: string;
  subtitle?: string;
  right?: ReactNode;
  children: ReactNode;
  refreshing?: boolean;
  onRefresh?: () => void;
}) {
  const c = useColors();
  return (
    <SafeAreaView edges={["top"]} className="flex-1 bg-zinc-100 dark:bg-zinc-950">
      <ScrollView
        contentContainerClassName="px-4 pb-10"
        refreshControl={
          onRefresh ? <RefreshControl refreshing={!!refreshing} onRefresh={onRefresh} tintColor={c.brand} /> : undefined
        }
      >
        <View className="flex-row items-end justify-between pb-4 pt-3">
          <View className="flex-1">
            <Text className="text-3xl font-bold tracking-tight text-zinc-900 dark:text-zinc-50">{title}</Text>
            {subtitle ? <Text className="mt-0.5 text-sm text-zinc-500 dark:text-zinc-400">{subtitle}</Text> : null}
          </View>
          {right}
        </View>
        {children}
      </ScrollView>
    </SafeAreaView>
  );
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return (
    <View className={`rounded-2xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900 ${className}`}>
      {children}
    </View>
  );
}

export function SectionTitle({ children, right }: { children: ReactNode; right?: ReactNode }) {
  return (
    <View className="mb-2 mt-5 flex-row items-center justify-between">
      <Text className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{children}</Text>
      {right}
    </View>
  );
}

export function Stat({
  label,
  value,
  hint,
  trend,
  tone = "default",
}: {
  label: string;
  value: string;
  hint?: string;
  trend?: { text: string; up: boolean; good?: boolean } | null;
  tone?: "default" | "good" | "bad" | "warn";
}) {
  const toneClass = {
    default: "text-zinc-900 dark:text-zinc-50",
    good: "text-emerald-600 dark:text-emerald-400",
    bad: "text-red-600 dark:text-red-400",
    warn: "text-amber-600 dark:text-amber-400",
  }[tone];
  const good = trend ? (trend.good ?? trend.up) : true;
  return (
    <Card className="flex-1">
      <Text className="text-xs font-medium text-zinc-500 dark:text-zinc-400">{label}</Text>
      <Text className={`mt-1 text-2xl font-bold ${toneClass}`} numberOfLines={1} adjustsFontSizeToFit>
        {value}
      </Text>
      <View className="mt-1 flex-row items-center gap-1.5">
        {trend ? (
          <Text className={`text-xs font-semibold ${good ? "text-emerald-600 dark:text-emerald-400" : "text-red-600 dark:text-red-400"}`}>
            {trend.text}
          </Text>
        ) : null}
        {hint ? <Text className="text-xs text-zinc-500 dark:text-zinc-400">{hint}</Text> : null}
      </View>
    </Card>
  );
}

const badgeTones = {
  green: ["bg-emerald-100 dark:bg-emerald-500/15", "text-emerald-700 dark:text-emerald-300"],
  red: ["bg-red-100 dark:bg-red-500/15", "text-red-700 dark:text-red-300"],
  amber: ["bg-amber-100 dark:bg-amber-500/15", "text-amber-700 dark:text-amber-300"],
  blue: ["bg-indigo-100 dark:bg-indigo-500/15", "text-indigo-700 dark:text-indigo-300"],
  gray: ["bg-zinc-100 dark:bg-zinc-800", "text-zinc-600 dark:text-zinc-300"],
} as const;

export type Tone = keyof typeof badgeTones;

export function Badge({ children, tone = "gray" }: { children: ReactNode; tone?: Tone }) {
  const [bg, fg] = badgeTones[tone];
  return (
    <View className={`self-start rounded-full px-2 py-0.5 ${bg}`}>
      <Text className={`text-[11px] font-semibold capitalize ${fg}`}>{children}</Text>
    </View>
  );
}

const statusTones: Record<string, Tone> = {
  delivered: "green",
  received: "green",
  sent: "blue",
  queued: "amber",
  unknown: "amber",
  unrouted: "amber",
  undelivered: "red",
  failed: "red",
  rejected: "red",
  expired: "red",
};

export const statusTone = (s: string): Tone => statusTones[s] ?? "gray";

/** Area chart of one or more series, drawn with SVG. */
export function AreaChart({
  series,
  height = 120,
  width = 320,
}: {
  series: { values: number[]; color: string }[];
  height?: number;
  width?: number;
}) {
  const max = Math.max(1, ...series.flatMap((s) => s.values));
  const path = (values: number[], close: boolean) => {
    if (values.length === 0) return "";
    const step = values.length > 1 ? width / (values.length - 1) : width;
    const pts = values.map((v, i) => `${(i * step).toFixed(1)},${(height - 4 - (v / max) * (height - 8)).toFixed(1)}`);
    const line = `M${pts.join(" L")}`;
    return close ? `${line} L${width},${height} L0,${height} Z` : line;
  };
  return (
    <Svg width="100%" height={height} viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none">
      <Defs>
        {series.map((s, i) => (
          <LinearGradient key={i} id={`g${i}`} x1="0" y1="0" x2="0" y2="1">
            <Stop offset="0" stopColor={s.color} stopOpacity={0.28} />
            <Stop offset="1" stopColor={s.color} stopOpacity={0} />
          </LinearGradient>
        ))}
      </Defs>
      {series.map((s, i) => (
        <Path key={`a${i}`} d={path(s.values, true)} fill={`url(#g${i})`} />
      ))}
      {series.map((s, i) => (
        <Path key={`l${i}`} d={path(s.values, false)} stroke={s.color} strokeWidth={2} fill="none" strokeLinejoin="round" />
      ))}
    </Svg>
  );
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
}) {
  return (
    <View className="flex-row rounded-xl bg-zinc-200/70 p-1 dark:bg-zinc-800">
      {options.map((o) => {
        const active = o.value === value;
        return (
          <Pressable
            key={o.value}
            onPress={() => onChange(o.value)}
            className={`flex-1 items-center rounded-lg py-1.5 ${active ? "bg-white shadow-sm dark:bg-zinc-950" : ""}`}
          >
            <Text className={`text-sm font-semibold ${active ? "text-zinc-900 dark:text-zinc-50" : "text-zinc-500 dark:text-zinc-400"}`}>
              {o.label}
            </Text>
          </Pressable>
        );
      })}
    </View>
  );
}

export function Loading() {
  const c = useColors();
  return (
    <View className="items-center py-16">
      <ActivityIndicator color={c.brand} />
    </View>
  );
}

export function ErrorBox({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <Card className="items-center border-red-200 dark:border-red-900">
      <Text className="text-center text-sm text-red-600 dark:text-red-400">
        {error instanceof Error ? error.message : "Something went wrong"}
      </Text>
      {onRetry ? (
        <Pressable onPress={onRetry} className="mt-3 rounded-lg bg-zinc-900 px-4 py-2 dark:bg-zinc-100">
          <Text className="text-sm font-semibold text-white dark:text-zinc-900">Try again</Text>
        </Pressable>
      ) : null}
    </Card>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <Text className="py-10 text-center text-sm text-zinc-500 dark:text-zinc-400">{children}</Text>;
}
