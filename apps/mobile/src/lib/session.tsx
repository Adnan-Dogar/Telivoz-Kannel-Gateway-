import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, loadCredentials, setUnauthorizedHandler, signIn, signOut, type Me } from "./api";

interface Session {
  ready: boolean;
  me: Me | null;
  server: string;
  login: (server: string, email: string, password: string, code?: string) => Promise<void>;
  logout: () => Promise<void>;
}

const Ctx = createContext<Session | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const [ready, setReady] = useState(false);
  const [me, setMe] = useState<Me | null>(null);
  const [server, setServer] = useState("");

  useEffect(() => {
    setUnauthorizedHandler(() => setMe(null));
    (async () => {
      const c = await loadCredentials();
      setServer(c.server);
      if (c.signedIn) {
        try {
          setMe(await api.get<Me>("/api/auth/me"));
        } catch {
          setMe(null);
        }
      }
      setReady(true);
    })();
  }, []);

  const login = useCallback(async (address: string, email: string, password: string, code?: string) => {
    const user = await signIn(address, email, password, code);
    const c = await loadCredentials();
    setServer(c.server);
    setMe(user);
  }, []);

  const logout = useCallback(async () => {
    await signOut();
    qc.clear();
    setMe(null);
  }, [qc]);

  return <Ctx.Provider value={{ ready, me, server, login, logout }}>{children}</Ctx.Provider>;
}

export function useSession() {
  const s = useContext(Ctx);
  if (!s) throw new Error("useSession outside SessionProvider");
  return s;
}

export const isStaff = (me: Me | null) => !!me && me.role !== "client";
