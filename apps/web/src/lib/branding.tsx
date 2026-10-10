// White-label branding: the portal name, logo and colour for the domain it is opened on.
import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { MessageSquareText } from "lucide-react";
import { cn } from "@/lib/utils";

export interface Branding {
  name: string;
  tagline: string;
  logo: string;
  primary_color: string;
  support_email: string;
}

const fallback: Branding = { name: "Telivoz", tagline: "SMS Gateway", logo: "", primary_color: "", support_email: "" };

export function useBranding(): Branding {
  const q = useQuery({
    queryKey: ["branding"],
    queryFn: async () => {
      const res = await fetch("/api/public/branding");
      if (!res.ok) return fallback;
      return { ...fallback, ...(await res.json()) } as Branding;
    },
    staleTime: 5 * 60_000,
  });
  return q.data ?? fallback;
}

/** Applies the brand colour and page title. Rendered once at the app root. */
export function BrandingEffect() {
  const b = useBranding();
  React.useEffect(() => {
    document.title = b.tagline ? `${b.name} · ${b.tagline}` : b.name;
    const root = document.documentElement.style;
    if (/^#[0-9a-f]{6}$/i.test(b.primary_color)) {
      root.setProperty("--primary", b.primary_color);
      root.setProperty("--ring", `color-mix(in oklch, ${b.primary_color} 45%, transparent)`);
      root.setProperty("--accent", `color-mix(in oklch, ${b.primary_color} 10%, var(--background))`);
      root.setProperty("--accent-foreground", `color-mix(in oklch, ${b.primary_color} 80%, var(--foreground))`);
    } else {
      for (const v of ["--primary", "--ring", "--accent", "--accent-foreground"]) root.removeProperty(v);
    }
  }, [b.name, b.tagline, b.primary_color]);
  return null;
}

/** The brand mark: the uploaded logo, or the default icon on the brand colour. */
export function BrandMark({ className, iconClassName }: { className?: string; iconClassName?: string }) {
  const b = useBranding();
  if (b.logo) return <img src={b.logo} alt={b.name} className={cn("object-contain", className)} />;
  return (
    <div className={cn("grid place-items-center bg-primary text-primary-foreground", className)}>
      <MessageSquareText className={iconClassName} />
    </div>
  );
}
