import { createRootRoute, createRoute, createRouter } from "@tanstack/react-router";
import { Layout } from "@/components/layout";
import { DashboardPage } from "@/pages/dashboard";
import { LivePage } from "@/pages/live";
import { AnalyticsPage } from "@/pages/analytics";
import { MessagesPage } from "@/pages/messages";
import { SendPage } from "@/pages/send";
import { AccountsPage, BillingPage, ClientsPage } from "@/pages/customers";
import { ConnectionsPage, ContentRulesPage, RatesPage, RouteTestPage, RoutesPage, VendorsPage } from "@/pages/network";
import { AuditPage, SettingsPage, UsersPage } from "@/pages/admin";
import { ApiDocsPage } from "@/pages/docs";
import { BlacklistPage, MORoutesPage, StatementPage } from "@/pages/phase2";
import { Empty } from "@/components/ui/misc";

const root = createRootRoute({
  component: Layout,
  notFoundComponent: () => <Empty title="Page not found" />,
});

const page = <P extends string>(path: P, component: () => React.ReactNode) => createRoute({ getParentRoute: () => root, path, component });

const routes = [
  page("/", DashboardPage),
  page("/live", LivePage),
  page("/analytics", AnalyticsPage),
  createRoute({ getParentRoute: () => root, path: "/messages", component: MessagesPage, validateSearch: (s: Record<string, unknown>) => ({ q: s.q != null && s.q !== "" ? String(s.q) : undefined }) }),
  page("/send", SendPage),
  page("/clients", ClientsPage),
  page("/accounts", AccountsPage),
  page("/billing", BillingPage),
  page("/vendors", VendorsPage),
  page("/connections", ConnectionsPage),
  page("/rates", RatesPage),
  page("/routes", RoutesPage),
  page("/content-rules", ContentRulesPage),
  page("/route-test", RouteTestPage),
  page("/blacklist", BlacklistPage),
  page("/mo-routes", MORoutesPage),
  createRoute({ getParentRoute: () => root, path: "/statements", component: StatementPage, validateSearch: (s: Record<string, unknown>) => ({ client: s.client != null && s.client !== "" ? String(s.client) : undefined, month: s.month != null && s.month !== "" ? String(s.month) : undefined }) }),
  page("/users", UsersPage),
  page("/audit", AuditPage),
  page("/settings", SettingsPage),
  page("/api-docs", ApiDocsPage),
];

export const router = createRouter({ routeTree: root.addChildren(routes), defaultPreload: "intent" });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
