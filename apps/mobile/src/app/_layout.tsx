import "../../global.css";
import { useState } from "react";
import { ActivityIndicator, View } from "react-native";
import { Stack } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { SessionProvider, useSession } from "@/lib/session";

function RootStack() {
  const { ready, me } = useSession();
  if (!ready) {
    return (
      <View className="flex-1 items-center justify-center bg-zinc-100 dark:bg-zinc-950">
        <ActivityIndicator />
      </View>
    );
  }
  return (
    <Stack screenOptions={{ headerShown: false }}>
      <Stack.Protected guard={!!me}>
        <Stack.Screen name="(tabs)" />
      </Stack.Protected>
      <Stack.Protected guard={!me}>
        <Stack.Screen name="login" />
      </Stack.Protected>
    </Stack>
  );
}

export default function RootLayout() {
  const [qc] = useState(
    () =>
      new QueryClient({
        defaultOptions: { queries: { retry: 1, staleTime: 5_000 } },
      }),
  );
  return (
    <SafeAreaProvider>
      <QueryClientProvider client={qc}>
        <SessionProvider>
          <StatusBar style="auto" />
          <RootStack />
        </SessionProvider>
      </QueryClientProvider>
    </SafeAreaProvider>
  );
}
