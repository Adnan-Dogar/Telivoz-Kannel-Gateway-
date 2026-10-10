import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { Toaster } from "sonner";
import { MeProvider, useSession } from "@/lib/session";
import { LoginPage } from "@/pages/login";
import { Spinner } from "@/components/ui/misc";
import { router } from "@/router";
import { BrandingEffect } from "@/lib/branding";
import "./index.css";

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 10_000 } },
});

function App() {
  const session = useSession();
  if (session.isLoading) {
    return (
      <div className="grid h-full place-items-center text-muted-foreground">
        <Spinner className="size-6" />
      </div>
    );
  }
  if (!session.data) return <LoginPage />;
  return (
    <MeProvider me={session.data}>
      <RouterProvider router={router} />
    </MeProvider>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrandingEffect />
      <App />
      <Toaster richColors position="top-right" closeButton />
    </QueryClientProvider>
  </React.StrictMode>,
);
