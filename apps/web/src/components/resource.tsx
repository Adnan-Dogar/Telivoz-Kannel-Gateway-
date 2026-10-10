import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Search, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, qs } from "@/lib/api";
import type { List, Lookups } from "@/lib/types";
import { useLookups } from "@/lib/session";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Field, Input, Select, Textarea } from "@/components/ui/input";
import { Dialog, Sheet } from "@/components/ui/sheet";
import { Empty, Skeleton, Switch } from "@/components/ui/misc";
import { TBody, TD, TH, THead, TR, Table } from "@/components/ui/table";
import { PageHeader } from "@/components/page";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type Row = Record<string, any>;

export interface Column {
  key: string;
  header: string;
  render?: (row: Row) => React.ReactNode;
  className?: string;
}

export type Option = { value: string; label: string };

export interface FieldDef {
  name: string;
  label: string;
  type?: "text" | "number" | "decimal" | "password" | "select" | "textarea" | "switch" | "tags" | "date" | "color" | "image";
  options?: Option[] | ((l: Lookups, values: Row) => Option[]);
  required?: boolean;
  hint?: string;
  placeholder?: string;
  default?: unknown;
  showWhen?: (values: Row) => boolean;
  createOnly?: boolean;
  span?: 1 | 2;
}

export interface ResourceProps {
  title: string;
  description?: string;
  path: string; // API path under /api, e.g. "clients"
  columns: Column[];
  fields: FieldDef[];
  canWrite: boolean;
  noun: string;
  search?: string;
  filters?: Record<string, string | number | undefined>;
  headerActions?: React.ReactNode;
  rowActions?: (row: Row) => React.ReactNode;
  extraEditor?: (row: Row | null, values: Row, set: (v: Row) => void) => React.ReactNode;
  toBody?: (values: Row, editing: Row | null) => Row;
  embedded?: boolean;
}

export function optionsFor(f: FieldDef, l: Lookups | undefined, values: Row): Option[] {
  if (!f.options) return [];
  if (typeof f.options === "function") return l ? f.options(l, values) : [];
  return f.options;
}

export function FieldInput({ f, values, set, lookups }: { f: FieldDef; values: Row; set: (v: Row) => void; lookups?: Lookups }) {
  const v = values[f.name];
  const onChange = (val: unknown) => set({ ...values, [f.name]: val });
  switch (f.type) {
    case "select":
      return (
        <Select value={v ?? ""} onChange={(e) => onChange(e.target.value)} required={f.required}>
          {!f.required && <option value="">—</option>}
          {f.required && (v === undefined || v === "") && <option value="">Choose…</option>}
          {optionsFor(f, lookups, values).map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </Select>
      );
    case "textarea":
      return <Textarea value={v ?? ""} placeholder={f.placeholder} onChange={(e) => onChange(e.target.value)} />;
    case "switch":
      return <Switch checked={!!v} onCheckedChange={onChange} />;
    case "tags":
      return (
        <Input
          value={Array.isArray(v) ? v.join(", ") : (v ?? "")}
          placeholder={f.placeholder}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    case "number":
      return <Input type="number" value={v ?? ""} placeholder={f.placeholder} required={f.required} onChange={(e) => onChange(e.target.value === "" ? "" : Number(e.target.value))} />;
    case "decimal":
      return <Input inputMode="decimal" value={v ?? ""} placeholder={f.placeholder ?? "0.000000"} required={f.required} onChange={(e) => onChange(e.target.value)} />;
    case "password":
      return <Input type="password" autoComplete="new-password" value={v ?? ""} placeholder={f.placeholder} onChange={(e) => onChange(e.target.value)} />;
    case "date":
      return <Input type="datetime-local" value={v ?? ""} onChange={(e) => onChange(e.target.value)} />;
    case "color":
      return (
        <div className="flex items-center gap-2">
          <input type="color" className="h-9 w-12 cursor-pointer rounded-md border bg-transparent" value={v || "#4f46e5"} onChange={(e) => onChange(e.target.value)} />
          <Input value={v ?? ""} placeholder="#4f46e5" onChange={(e) => onChange(e.target.value)} />
        </div>
      );
    case "image":
      return (
        <div className="flex items-center gap-3">
          {v ? <img src={String(v)} alt="" className="size-12 rounded-md border object-contain" /> : <div className="size-12 rounded-md border border-dashed" />}
          <input
            type="file"
            accept="image/png,image/jpeg,image/webp,image/svg+xml"
            className="text-xs"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (!file) return;
              if (file.size > 280_000) {
                toast.error("Image too large: use one under 280 KB");
                return;
              }
              const reader = new FileReader();
              reader.onload = () => onChange(String(reader.result));
              reader.readAsDataURL(file);
            }}
          />
          {v ? <Button type="button" variant="ghost" size="sm" onClick={() => onChange("")}>Remove</Button> : null}
        </div>
      );
    default:
      return <Input value={v ?? ""} placeholder={f.placeholder} required={f.required} onChange={(e) => onChange(e.target.value)} />;
  }
}

function initialValues(fields: FieldDef[], row: Row | null): Row {
  const v: Row = {};
  for (const f of fields) {
    if (row) {
      if (f.type === "password") v[f.name] = "";
      else if (f.type === "date" && row[f.name]) v[f.name] = String(row[f.name]).slice(0, 16);
      else v[f.name] = row[f.name] ?? (f.type === "switch" ? false : "");
    } else v[f.name] = f.default ?? (f.type === "switch" ? false : "");
  }
  return v;
}

export function ResourcePage(props: ResourceProps) {
  const { title, description, path, columns, fields, canWrite, noun, filters, embedded } = props;
  const qc = useQueryClient();
  const { data: lookups } = useLookups();
  const [q, setQ] = React.useState("");
  const [editing, setEditing] = React.useState<Row | null>(null);
  const [open, setOpen] = React.useState(false);
  const [confirm, setConfirm] = React.useState(false);
  const [values, setValues] = React.useState<Row>({});

  const list = useQuery({
    queryKey: [path, q, filters],
    queryFn: () => api.get<List<Row>>(`/api/${path}${qs({ q, ...filters })}`),
  });

  const openForm = (row: Row | null) => {
    setEditing(row);
    setValues(initialValues(fields, row));
    setOpen(true);
  };

  const save = useMutation({
    mutationFn: async () => {
      const body: Row = {};
      for (const f of fields) {
        if (f.showWhen && !f.showWhen(values)) continue;
        if (editing && f.createOnly) continue;
        let val = values[f.name];
        if (f.type === "password" && !val) continue;
        if (f.type === "date" && val) val = new Date(val).toISOString();
        if ((f.type === "select" || f.type === "number") && val === "" && !f.required) val = null;
        body[f.name] = val;
      }
      const final = props.toBody ? props.toBody({ ...values, ...body }, editing) : body;
      return editing ? api.patch<Row>(`/api/${path}/${editing.id}`, final) : api.post<Row>(`/api/${path}`, final);
    },
    onSuccess: () => {
      toast.success(editing ? `${noun} updated` : `${noun} created`);
      setOpen(false);
      qc.invalidateQueries({ queryKey: [path] });
      qc.invalidateQueries({ queryKey: ["lookups"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const remove = useMutation({
    mutationFn: () => api.del(`/api/${path}/${editing?.id}`),
    onSuccess: () => {
      toast.success(`${noun} deleted`);
      setConfirm(false);
      setOpen(false);
      qc.invalidateQueries({ queryKey: [path] });
      qc.invalidateQueries({ queryKey: ["lookups"] });
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const items = list.data?.items ?? [];
  const content = (
    <Card>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b p-3">
        <div className="relative w-full max-w-xs">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={props.search ?? `Search ${title.toLowerCase()}…`} className="pl-8" />
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted-foreground tabular">{list.data ? `${list.data.total} total` : ""}</span>
          {embedded && props.headerActions}
          {canWrite && (
            <Button size="sm" onClick={() => openForm(null)}>
              <Plus /> New {noun.toLowerCase()}
            </Button>
          )}
        </div>
      </div>
      {list.isLoading ? (
        <div className="space-y-2 p-4">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <Empty title={q ? "Nothing matches your search" : `No ${title.toLowerCase()} yet`}>
          {canWrite && !q && "Create the first one with the button above."}
        </Empty>
      ) : (
        <Table>
          <THead>
            <tr>
              {columns.map((c) => (
                <TH key={c.key} className={c.className}>
                  {c.header}
                </TH>
              ))}
              {props.rowActions && <TH className="w-1" />}
            </tr>
          </THead>
          <TBody>
            {items.map((row) => (
              <TR key={row.id} className={canWrite ? "cursor-pointer" : undefined} onClick={() => canWrite && openForm(row)}>
                {columns.map((c) => (
                  <TD key={c.key} className={c.className}>
                    {c.render ? c.render(row) : (row[c.key] ?? "—")}
                  </TD>
                ))}
                {props.rowActions && (
                  <TD onClick={(e) => e.stopPropagation()} className="whitespace-nowrap text-right">
                    {props.rowActions(row)}
                  </TD>
                )}
              </TR>
            ))}
          </TBody>
        </Table>
      )}
    </Card>
  );

  return (
    <>
      {!embedded && <PageHeader title={title} description={description} actions={props.headerActions} />}
      {content}
      <Sheet
        open={open}
        onOpenChange={setOpen}
        title={editing ? `Edit ${noun.toLowerCase()}` : `New ${noun.toLowerCase()}`}
        wide={fields.length > 10}
        footer={
          <>
            {editing && (
              <Button variant="ghost" className="mr-auto text-danger" onClick={() => setConfirm(true)}>
                <Trash2 /> Delete
              </Button>
            )}
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button onClick={() => save.mutate()} disabled={save.isPending}>
              {save.isPending ? "Saving…" : "Save"}
            </Button>
          </>
        }
      >
        <form
          className="grid grid-cols-2 gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          {fields
            .filter((f) => (!f.showWhen || f.showWhen(values)) && !(editing && f.createOnly))
            .map((f) => (
              <Field key={f.name} label={f.label + (f.required ? " *" : "")} hint={f.hint} className={f.span === 1 ? "col-span-2 sm:col-span-1" : "col-span-2"}>
                <FieldInput f={f} values={values} set={setValues} lookups={lookups} />
              </Field>
            ))}
          {props.extraEditor && <div className="col-span-2">{props.extraEditor(editing, values, setValues)}</div>}
          <button type="submit" className="hidden" />
        </form>
      </Sheet>
      <Dialog
        open={confirm}
        onOpenChange={setConfirm}
        title={`Delete this ${noun.toLowerCase()}?`}
        description="This cannot be undone."
        footer={
          <>
            <Button variant="outline" onClick={() => setConfirm(false)}>
              Cancel
            </Button>
            <Button variant="danger" onClick={() => remove.mutate()} disabled={remove.isPending}>
              Delete
            </Button>
          </>
        }
      />
    </>
  );
}
