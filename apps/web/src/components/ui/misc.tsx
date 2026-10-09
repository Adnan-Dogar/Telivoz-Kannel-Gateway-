import * as React from "react";
import * as T from "@radix-ui/react-tabs";
import * as S from "@radix-ui/react-switch";
import * as Tip from "@radix-ui/react-tooltip";
import * as DM from "@radix-ui/react-dropdown-menu";
import { Inbox, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded-md bg-muted", className)} />;
}

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={cn("size-4 animate-spin", className)} />;
}

export function Empty({ title, children, icon }: { title: string; children?: React.ReactNode; icon?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-6 py-14 text-center">
      <div className="rounded-full bg-muted p-3 text-muted-foreground">{icon ?? <Inbox className="size-5" />}</div>
      <p className="font-medium">{title}</p>
      {children && <div className="max-w-sm text-sm text-muted-foreground">{children}</div>}
    </div>
  );
}

export function Tabs({ value, onValueChange, items, className }: {
  value: string;
  onValueChange: (v: string) => void;
  items: { value: string; label: React.ReactNode }[];
  className?: string;
}) {
  return (
    <T.Root value={value} onValueChange={onValueChange}>
      <T.List className={cn("inline-flex flex-wrap gap-1 rounded-lg bg-muted p-1", className)}>
        {items.map((i) => (
          <T.Trigger
            key={i.value}
            value={i.value}
            className="cursor-pointer rounded-md px-3 py-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground data-[state=active]:bg-card data-[state=active]:text-foreground data-[state=active]:shadow-sm"
          >
            {i.label}
          </T.Trigger>
        ))}
      </T.List>
    </T.Root>
  );
}

export function Switch({ checked, onCheckedChange, id }: { checked: boolean; onCheckedChange: (v: boolean) => void; id?: string }) {
  return (
    <S.Root
      id={id}
      checked={checked}
      onCheckedChange={onCheckedChange}
      className="relative h-5 w-9 cursor-pointer rounded-full bg-input transition-colors data-[state=checked]:bg-primary"
    >
      <S.Thumb className="block size-4 translate-x-0.5 rounded-full bg-white shadow transition-transform data-[state=checked]:translate-x-[18px]" />
    </S.Root>
  );
}

export function Tooltip({ content, children }: { content: React.ReactNode; children: React.ReactNode }) {
  return (
    <Tip.Provider delayDuration={200}>
      <Tip.Root>
        <Tip.Trigger asChild>{children}</Tip.Trigger>
        <Tip.Portal>
          <Tip.Content sideOffset={6} className="z-50 max-w-xs rounded-md bg-foreground px-2.5 py-1.5 text-xs text-background shadow-lg">
            {content}
          </Tip.Content>
        </Tip.Portal>
      </Tip.Root>
    </Tip.Provider>
  );
}

export const Menu = DM.Root;
export const MenuTrigger = DM.Trigger;
export function MenuContent({ children, align = "end" }: { children: React.ReactNode; align?: "start" | "end" }) {
  return (
    <DM.Portal>
      <DM.Content align={align} sideOffset={6} className="z-50 min-w-48 rounded-lg border bg-card p-1 shadow-xl">
        {children}
      </DM.Content>
    </DM.Portal>
  );
}
export function MenuItem({ children, onSelect, danger }: { children: React.ReactNode; onSelect?: () => void; danger?: boolean }) {
  return (
    <DM.Item
      onSelect={onSelect}
      className={cn("flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-sm outline-none data-[highlighted]:bg-muted [&_svg]:size-4", danger && "text-danger")}
    >
      {children}
    </DM.Item>
  );
}
export function MenuLabel({ children }: { children: React.ReactNode }) {
  return <DM.Label className="px-2.5 py-1.5 text-xs text-muted-foreground">{children}</DM.Label>;
}
export const MenuSeparator = () => <DM.Separator className="my-1 h-px bg-border" />;
