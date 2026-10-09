// Secure storage on phones (Keychain / Keystore); localStorage when the app runs in a browser (`npm run web`).
import { Platform } from "react-native";
import * as SecureStore from "expo-secure-store";

const web = Platform.OS === "web";

export const storage = {
  get: async (key: string) => (web ? globalThis.localStorage?.getItem(key) ?? null : SecureStore.getItemAsync(key)),
  set: async (key: string, value: string) => (web ? globalThis.localStorage?.setItem(key, value) : SecureStore.setItemAsync(key, value)),
  remove: async (key: string) => (web ? globalThis.localStorage?.removeItem(key) : SecureStore.deleteItemAsync(key)),
};
