import * as React from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, setUnauthorizedHandler } from "./api";
import type { Lookups, Me } from "./types";

const MeContext = React.createContext<Me | null>(null);

export function useMe(): Me {
  const me = React.useContext(MeContext);
  if (!me) throw new Error("useMe outside session");
  return me;
}

export function MeProvider({ me, children }: { me: Me; children: React.ReactNode }) {
  return <MeContext.Provider value={me}>{children}</MeContext.Provider>;
}

export function useSession() {
  const qc = useQueryClient();
  React.useEffect(() => {
    setUnauthorizedHandler(() => qc.setQueryData(["me"], null));
  }, [qc]);
  return useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      try {
        return await api.get<Me>("/api/auth/me");
      } catch {
        return null;
      }
    },
    staleTime: 5 * 60_000,
  });
}

export function useLookups() {
  return useQuery({ queryKey: ["lookups"], queryFn: () => api.get<Lookups>("/api/lookups"), staleTime: 60_000 });
}

export const can = {
  manage: (me: Me) => ["admin", "manager", "noc"].includes(me.role),
  admin: (me: Me) => me.role === "admin",
  staff: (me: Me) => me.role !== "client",
  money: (me: Me) => ["admin", "manager", "finance"].includes(me.role),
};

export function useTheme() {
  const [dark, setDark] = React.useState(() => document.documentElement.classList.contains("dark"));
  const toggle = React.useCallback(() => {
    setDark((d) => {
      const next = !d;
      document.documentElement.classList.toggle("dark", next);
      try {
        localStorage.setItem("theme", next ? "dark" : "light");
      } catch {
        /* storage blocked */
      }
      window.dispatchEvent(new Event("themechange"));
      return next;
    });
  }, []);
  return { dark, toggle };
}
