import * as React from "react";
import * as D from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";

// Side drawer (forms, details).
export function Sheet({ open, onOpenChange, title, description, children, footer, wide }: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: React.ReactNode;
  description?: React.ReactNode;
  children: React.ReactNode;
  footer?: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <D.Root open={open} onOpenChange={onOpenChange}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-40 bg-black/40 backdrop-blur-[2px] data-[state=open]:animate-in" />
        <D.Content
          className={cn(
            "fixed inset-y-0 right-0 z-50 flex w-full flex-col border-l bg-card shadow-2xl outline-none",
            wide ? "sm:max-w-2xl" : "sm:max-w-lg",
          )}
        >
          <div className="flex items-start justify-between gap-4 border-b px-6 py-4">
            <div>
              <D.Title className="text-lg font-semibold">{title}</D.Title>
              {description ? <D.Description className="mt-1 text-sm text-muted-foreground">{description}</D.Description> : <D.Description className="sr-only">{String(title)}</D.Description>}
            </div>
            <D.Close className="rounded-md p-1.5 text-muted-foreground hover:bg-muted" aria-label="Close">
              <X className="size-4" />
            </D.Close>
          </div>
          <div className="flex-1 overflow-y-auto px-6 py-5">{children}</div>
          {footer && <div className="flex justify-end gap-2 border-t px-6 py-4">{footer}</div>}
        </D.Content>
      </D.Portal>
    </D.Root>
  );
}

// Centered dialog (confirmations, small forms).
export function Dialog({ open, onOpenChange, title, description, children, footer }: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: React.ReactNode;
  description?: React.ReactNode;
  children?: React.ReactNode;
  footer?: React.ReactNode;
}) {
  return (
    <D.Root open={open} onOpenChange={onOpenChange}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-40 bg-black/40 backdrop-blur-[2px]" />
        <D.Content className="fixed left-1/2 top-1/2 z-50 w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg border bg-card p-6 shadow-2xl outline-none">
          <D.Title className="text-lg font-semibold">{title}</D.Title>
          {description ? <D.Description className="mt-1.5 text-sm text-muted-foreground">{description}</D.Description> : <D.Description className="sr-only">{String(title)}</D.Description>}
          {children && <div className="mt-4">{children}</div>}
          {footer && <div className="mt-6 flex justify-end gap-2">{footer}</div>}
        </D.Content>
      </D.Portal>
    </D.Root>
  );
}
