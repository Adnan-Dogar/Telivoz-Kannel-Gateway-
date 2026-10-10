import { Alert, Pressable, Text, View } from "react-native";
import { useColorScheme } from "nativewind";
import Constants from "expo-constants";
import { LogOut, Moon, ShieldCheck, Sun } from "lucide-react-native";
import { useSession } from "@/lib/session";
import { Badge, Card, Screen, SectionTitle, useColors } from "@/components/ui";

const roleLabel: Record<string, string> = {
  admin: "Administrator",
  manager: "Manager",
  team_lead: "Team lead",
  sales: "Sales",
  finance: "Finance",
  noc: "NOC",
  client: "Client",
};

export default function Settings() {
  const { me, server, logout } = useSession();
  const { colorScheme, setColorScheme } = useColorScheme();
  const c = useColors();
  if (!me) return null;

  return (
    <Screen title="Account">
      <Card>
        <View className="flex-row items-center gap-3">
          <View className="h-12 w-12 items-center justify-center rounded-full bg-indigo-100 dark:bg-indigo-500/20">
            <Text className="text-lg font-bold text-indigo-700 dark:text-indigo-300">{me.name.slice(0, 1).toUpperCase()}</Text>
          </View>
          <View className="flex-1">
            <Text className="text-base font-semibold text-zinc-900 dark:text-zinc-50">{me.name}</Text>
            <Text className="text-sm text-zinc-500 dark:text-zinc-400">{me.email}</Text>
          </View>
          <Badge tone="blue">{roleLabel[me.role] ?? me.role}</Badge>
        </View>
        {me.client_name ? <Text className="mt-3 text-sm text-zinc-600 dark:text-zinc-300">Client: {me.client_name}</Text> : null}
      </Card>

      <SectionTitle>Security</SectionTitle>
      <Card className="flex-row items-center gap-3">
        <ShieldCheck color={me.totp_enabled ? c.green : c.amber} size={22} />
        <View className="flex-1">
          <Text className="font-semibold text-zinc-900 dark:text-zinc-50">Two-factor sign-in</Text>
          <Text className="text-xs text-zinc-500 dark:text-zinc-400">
            {me.totp_enabled ? "On: a code from your authenticator app is required." : "Off: turn it on in the web portal under Settings."}
          </Text>
        </View>
      </Card>

      <SectionTitle>Appearance</SectionTitle>
      <Pressable onPress={() => setColorScheme(colorScheme === "dark" ? "light" : "dark")}>
        <Card className="flex-row items-center gap-3">
          {colorScheme === "dark" ? <Moon color={c.text} size={20} /> : <Sun color={c.text} size={20} />}
          <Text className="flex-1 font-semibold text-zinc-900 dark:text-zinc-50">{colorScheme === "dark" ? "Dark" : "Light"} mode</Text>
          <Text className="text-sm text-indigo-600 dark:text-indigo-400">Switch</Text>
        </Card>
      </Pressable>

      <SectionTitle>Server</SectionTitle>
      <Card>
        <Text selectable className="text-sm text-zinc-700 dark:text-zinc-300">{server}</Text>
        <Text className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">App version {Constants.expoConfig?.version ?? "1.0.0"}</Text>
      </Card>

      <Pressable
        onPress={() =>
          Alert.alert("Sign out?", undefined, [
            { text: "Cancel", style: "cancel" },
            { text: "Sign out", style: "destructive", onPress: logout },
          ])
        }
        className="mt-6 flex-row items-center justify-center gap-2 rounded-xl border border-red-200 bg-white py-3.5 active:bg-red-50 dark:border-red-900 dark:bg-zinc-900"
      >
        <LogOut color={c.red} size={18} />
        <Text className="font-semibold text-red-600 dark:text-red-400">Sign out</Text>
      </Pressable>
    </Screen>
  );
}
