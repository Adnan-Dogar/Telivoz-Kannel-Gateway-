import * as React from "react";
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Command } from "cmdk";
import {
  Activity, BarChart3, Building2, Cable, ChevronsLeft, ChevronsRight, CreditCard, FileClock, FlaskConical, KeyRound,
  LayoutDashboard, LogOut, Menu as MenuIcon, MessageSquareText, Moon, Route as RouteIcon, Search, Send, Settings, Sun, Tags,
  Truck, Users, Wand2,
} from "lucide-react";
import { api } from "@/lib/api";
import { useMe, useTheme } from "@/lib/session";
import type { Me } from "@/lib/types";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from "@/components/ui/misc";

type NavItem = { to: string; label: string; icon: React.ElementType; show?: (me: Me) => boolean };
const staff = (me: Me) => me.role !== "client";
const clientOnly = (me: Me) => me.role === "client";

export const nav: { group: string; items: NavItem[] }[] = [
  {
    group: "Overview",
    items: [
      { to: "/", label: "Dashboard", icon: LayoutDashboard },
      { to: "/live", label: "Live traffic", icon: Activity, show: staff },
      { to: "/analytics", label: "Analytics", icon: BarChart3 },
      { to: "/messages", label: "Messages", icon: MessageSquareText },
      { to: "/send", label: "Send & campaigns", icon: Send },
    ],
  },
  {
    group: "Customers",
    items: [
      { to: "/clients", label: "Clients", icon: Building2, show: staff },
      { to: "/accounts", label: "Accounts & API keys", icon: KeyRound },
      { to: "/billing", label: "Billing", icon: CreditCard, show: clientOnly },
    ],
  },
  {
    group: "Vendors & routing",
    items: [
      { to: "/vendors", label: "Vendors", icon: Truck, show: staff },
      { to: "/connections", label: "Connections", icon: Cable, show: staff },
      { to: "/rates", label: "Rates", icon: Tags },
      { to: "/routes", label: "Routes", icon: RouteIcon, show: staff },
      { to: "/content-rules", label: "Content rules", icon: Wand2, show: staff },
      { to: "/route-test", label: "Route tester", icon: FlaskConical, show: staff },
    ],
  },
  {
    group: "Administration",
    items: [
      { to: "/users", label: "Users & team", icon: Users, show: staff },
      { to: "/audit", label: "Audit log", icon: FileClock, show: (me) => me.role === "admin" },
      { to: "/settings", label: "Settings", icon: Settings },
    ],
  },
];

function visible(me: Me) {
  return nav.map((g) => ({ ...g, items: g.items.filter((i) => !i.show || i.show(me)) })).filter((g) => g.items.length);
}

function Sidebar({ collapsed, onNavigate }: { collapsed: boolean; onNavigate?: () => void }) {
  const me = useMe();
  const path = useRouterState({ select: (s) => s.location.pathname });
  return (
    <nav className="flex flex-col gap-5 px-3 py-4">
      {visible(me).map((g) => (
        <div key={g.group}>
          {!collapsed && <p className="mb-1.5 px-2.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{g.group}</p>}
          <div className="grid gap-0.5">
            {g.items.map((i) => {
              const active = i.to === "/" ? path === "/" : path.startsWith(i.to);
              return (
                <Link
                  key={i.to}
                  to={i.to}
                  onClick={onNavigate}
                  title={collapsed ? i.label : undefined}
                  className={cn(
                    "flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
                    active && "bg-accent text-accent-foreground hover:bg-accent hover:text-accent-foreground",
                    collapsed && "justify-center px-0",
                  )}
                >
                  <i.icon className="size-4 shrink-0" />
                  {!collapsed && <span className="truncate">{i.label}</span>}
                </Link>
              );
            })}
          </div>
        </div>
      ))}
    </nav>
  );
}

function Brand({ collapsed }: { collapsed: boolean }) {
  return (
    <div className={cn("flex h-14 items-center gap-2.5 border-b px-4", collapsed && "justify-center px-0")}>
      <div className="grid size-8 place-items-center rounded-lg bg-primary text-primary-foreground shadow-sm">
        <MessageSquareText className="size-4" />
      </div>
      {!collapsed && (
        <div className="leading-tight">
          <p className="font-semibold">Telivoz</p>
          <p className="text-[11px] text-muted-foreground">SMS Gateway</p>
        </div>
      )}
    </div>
  );
}

function CommandPalette({ open, setOpen }: { open: boolean; setOpen: (o: boolean) => void }) {
  const me = useMe();
  const navigate = useNavigate();
  const [term, setTerm] = React.useState("");
  const go = (to: string, search?: Record<string, string>) => {
    setOpen(false);
    setTerm("");
    navigate({ to, search: search as never });
  };
  if (!open) return null;
  const looksLikeLookup = term.trim().length >= 5;
  return (
    <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-[2px]" onClick={() => setOpen(false)}>
      <Command
        className="mx-auto mt-[12vh] w-[calc(100%-2rem)] max-w-xl overflow-hidden rounded-xl border bg-card shadow-2xl"
        onClick={(e) => e.stopPropagation()}
        label="Command palette"
      >
        <div className="flex items-center gap-2 border-b px-3">
          <Search className="size-4 text-muted-foreground" />
          <Command.Input
            autoFocus
            value={term}
            onValueChange={setTerm}
            placeholder="Go to a page, or search a number / message ID…"
            className="h-12 w-full bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          />
          <kbd className="rounded border px-1.5 text-[10px] text-muted-foreground">ESC</kbd>
        </div>
        <Command.List className="max-h-80 overflow-y-auto p-2">
          <Command.Empty className="px-3 py-6 text-center text-sm text-muted-foreground">No results.</Command.Empty>
          {looksLikeLookup && (
            <Command.Group heading="Search" className="text-xs text-muted-foreground [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5">
              <Command.Item
                value={`search messages ${term}`}
                onSelect={() => go("/messages", { q: term.trim() })}
                className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm text-foreground data-[selected=true]:bg-muted"
              >
                <MessageSquareText className="size-4" /> Find messages for “{term.trim()}”
              </Command.Item>
            </Command.Group>
          )}
          {visible(me).map((g) => (
            <Command.Group key={g.group} heading={g.group} className="text-xs text-muted-foreground [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5">
              {g.items.map((i) => (
                <Command.Item
                  key={i.to}
                  value={i.label}
                  onSelect={() => go(i.to)}
                  className="flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm text-foreground data-[selected=true]:bg-muted"
                >
                  <i.icon className="size-4" /> {i.label}
                </Command.Item>
              ))}
            </Command.Group>
          ))}
        </Command.List>
      </Command>
    </div>
  );
}

export function Layout() {
  const me = useMe();
  const qc = useQueryClient();
  const { dark, toggle } = useTheme();
  const [collapsed, setCollapsed] = React.useState(() => {
    try {
      return localStorage.getItem("sidebar") === "collapsed";
    } catch {
      return false;
    }
  });
  const [mobileOpen, setMobileOpen] = React.useState(false);
  const [palette, setPalette] = React.useState(false);

  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette((p) => !p);
      }
      if (e.key === "Escape") setPalette(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const toggleCollapsed = () => {
    setCollapsed((c) => {
      try {
        localStorage.setItem("sidebar", c ? "open" : "collapsed");
      } catch {
        /* ignore */
      }
      return !c;
    });
  };

  const logout = async () => {
    await api.post("/api/auth/logout").catch(() => undefined);
    qc.setQueryData(["me"], null);
    qc.clear();
  };

  const initials = (me.name || me.email).split(/[\s@.]/).filter(Boolean).slice(0, 2).map((s) => s[0]?.toUpperCase()).join("");

  return (
    <div className="flex h-full">
      <aside className={cn("hidden shrink-0 flex-col border-r bg-sidebar transition-[width] md:flex", collapsed ? "w-16" : "w-64")}>
        <Brand collapsed={collapsed} />
        <div className="flex-1 overflow-y-auto">
          <Sidebar collapsed={collapsed} />
        </div>
        <button onClick={toggleCollapsed} className="flex h-10 items-center justify-center border-t text-muted-foreground hover:text-foreground" aria-label="Toggle sidebar">
          {collapsed ? <ChevronsRight className="size-4" /> : <ChevronsLeft className="size-4" />}
        </button>
      </aside>

      {mobileOpen && (
        <div className="fixed inset-0 z-40 bg-black/40 md:hidden" onClick={() => setMobileOpen(false)}>
          <aside className="h-full w-72 overflow-y-auto border-r bg-sidebar" onClick={(e) => e.stopPropagation()}>
            <Brand collapsed={false} />
            <Sidebar collapsed={false} onNavigate={() => setMobileOpen(false)} />
          </aside>
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b bg-background/80 px-4 backdrop-blur md:px-6">
          <Button variant="ghost" size="icon" className="md:hidden" onClick={() => setMobileOpen(true)} aria-label="Open menu">
            <MenuIcon />
          </Button>
          <button
            onClick={() => setPalette(true)}
            className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-md border bg-card px-3 text-sm text-muted-foreground shadow-xs hover:bg-muted md:max-w-md"
          >
            <Search className="size-4" />
            <span className="flex-1 truncate text-left">Search pages, numbers, message IDs…</span>
            <kbd className="hidden rounded border px-1.5 text-[10px] sm:inline">⌘K</kbd>
          </button>
          <div className="ml-auto flex shrink-0 items-center gap-1">
            <Button variant="ghost" size="icon" onClick={toggle} aria-label="Toggle dark mode">
              {dark ? <Sun /> : <Moon />}
            </Button>
            <Menu>
              <MenuTrigger asChild>
                <button className="flex items-center gap-2 rounded-md px-1.5 py-1 hover:bg-muted">
                  <span className="grid size-8 place-items-center rounded-full bg-accent text-xs font-semibold text-accent-foreground">{initials}</span>
                  <span className="hidden text-left leading-tight lg:block">
                    <span className="block text-sm font-medium">{me.name || me.email}</span>
                    <span className="block text-xs capitalize text-muted-foreground">{me.client_name ?? me.role.replace("_", " ")}</span>
                  </span>
                </button>
              </MenuTrigger>
              <MenuContent>
                <MenuLabel>{me.email}</MenuLabel>
                <MenuSeparator />
                <Link to="/settings">
                  <MenuItem>
                    <Settings /> Settings
                  </MenuItem>
                </Link>
                <MenuItem onSelect={logout} danger>
                  <LogOut /> Sign out
                </MenuItem>
              </MenuContent>
            </Menu>
          </div>
        </header>
        <main className="flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-[1440px] px-4 py-6 md:px-6 lg:px-8">
            <Outlet />
          </div>
        </main>
      </div>
      <CommandPalette open={palette} setOpen={setPalette} />
    </div>
  );
}
