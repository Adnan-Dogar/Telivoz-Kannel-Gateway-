import { Tabs } from "expo-router";
import { Activity, LayoutDashboard, MessagesSquare, Network, Settings } from "lucide-react-native";
import { isStaff, useSession } from "@/lib/session";
import { useColors } from "@/components/ui";

export default function TabsLayout() {
  const { me } = useSession();
  const c = useColors();
  return (
    <Tabs
      screenOptions={{
        headerShown: false,
        tabBarActiveTintColor: c.brand,
        tabBarInactiveTintColor: c.muted,
        tabBarStyle: { backgroundColor: c.card, borderTopColor: c.border },
        tabBarLabelStyle: { fontSize: 11, fontWeight: "600" },
      }}
    >
      <Tabs.Screen name="index" options={{ title: "Dashboard", tabBarIcon: ({ color, size }) => <LayoutDashboard color={color} size={size} /> }} />
      <Tabs.Screen name="live" options={{ title: "Live", tabBarIcon: ({ color, size }) => <Activity color={color} size={size} /> }} />
      <Tabs.Screen
        name="connections"
        options={{
          title: "Connections",
          href: isStaff(me) ? undefined : null,
          tabBarIcon: ({ color, size }) => <Network color={color} size={size} />,
        }}
      />
      <Tabs.Screen name="messages" options={{ title: "Messages", tabBarIcon: ({ color, size }) => <MessagesSquare color={color} size={size} /> }} />
      <Tabs.Screen name="settings" options={{ title: "Account", tabBarIcon: ({ color, size }) => <Settings color={color} size={size} /> }} />
    </Tabs>
  );
}
