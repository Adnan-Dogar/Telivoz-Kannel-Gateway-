import { useState } from "react";
import { ActivityIndicator, KeyboardAvoidingView, Platform, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { MessageSquareText } from "lucide-react-native";
import { ApiError } from "@/lib/api";
import { useSession } from "@/lib/session";
import { useColors } from "@/components/ui";

function Field(props: React.ComponentProps<typeof TextInput> & { label: string }) {
  const c = useColors();
  const { label, ...rest } = props;
  return (
    <View className="mb-3">
      <Text className="mb-1.5 text-sm font-medium text-zinc-700 dark:text-zinc-300">{label}</Text>
      <TextInput
        placeholderTextColor={c.muted}
        autoCapitalize="none"
        autoCorrect={false}
        className="rounded-xl border border-zinc-300 bg-white px-3.5 py-3 text-base text-zinc-900 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-50"
        {...rest}
      />
    </View>
  );
}

export default function Login() {
  const { login, server: savedServer } = useSession();
  const [server, setServer] = useState(savedServer);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async () => {
    setError("");
    if (!server || !email || !password) {
      setError("Enter the server address, email and password.");
      return;
    }
    setBusy(true);
    try {
      await login(server, email, password, needCode ? code : undefined);
    } catch (e) {
      if (e instanceof ApiError && e.code === "totp_required") {
        setNeedCode(true);
      } else {
        setError(e instanceof Error ? e.message : "Sign-in failed");
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <SafeAreaView className="flex-1 bg-zinc-100 dark:bg-zinc-950">
      <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} className="flex-1">
        <ScrollView contentContainerClassName="flex-grow justify-center px-6 py-10" keyboardShouldPersistTaps="handled">
          <View className="mb-8 items-center">
            <View className="mb-4 h-16 w-16 items-center justify-center rounded-2xl bg-indigo-600">
              <MessageSquareText color="white" size={30} />
            </View>
            <Text className="text-3xl font-bold tracking-tight text-zinc-900 dark:text-zinc-50">Welcome back</Text>
            <Text className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">Sign in to the Telivoz SMS gateway</Text>
          </View>

          <View className="rounded-3xl border border-zinc-200 bg-white/70 p-5 dark:border-zinc-800 dark:bg-zinc-900/60">
            {!needCode ? (
              <>
                <Field label="Server" value={server} onChangeText={setServer} placeholder="portal.example.com" keyboardType="url" />
                <Field label="Email" value={email} onChangeText={setEmail} placeholder="you@company.com" keyboardType="email-address" textContentType="username" />
                <Field label="Password" value={password} onChangeText={setPassword} placeholder="••••••••" secureTextEntry textContentType="password" onSubmitEditing={submit} />
              </>
            ) : (
              <>
                <Text className="mb-3 text-sm text-zinc-600 dark:text-zinc-300">
                  Two-factor sign-in is on for this account. Enter the 6-digit code from your authenticator app.
                </Text>
                <Field label="Code" value={code} onChangeText={setCode} placeholder="123 456" keyboardType="number-pad" textContentType="oneTimeCode" maxLength={7} autoFocus onSubmitEditing={submit} />
              </>
            )}
            {error ? <Text className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</Text> : null}
            <Pressable onPress={submit} disabled={busy} className="items-center rounded-xl bg-indigo-600 py-3.5 active:bg-indigo-700 disabled:opacity-60">
              {busy ? <ActivityIndicator color="white" /> : <Text className="text-base font-semibold text-white">{needCode ? "Verify" : "Sign in"}</Text>}
            </Pressable>
            {needCode ? (
              <Pressable onPress={() => { setNeedCode(false); setCode(""); }} className="mt-3 items-center py-2">
                <Text className="text-sm font-medium text-indigo-600 dark:text-indigo-400">Back</Text>
              </Pressable>
            ) : null}
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
