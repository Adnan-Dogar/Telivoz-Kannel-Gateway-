export type Role = "admin" | "manager" | "team_lead" | "sales" | "finance" | "noc" | "client";

export interface Me {
  id: number;
  email: string;
  name: string;
  role: Role;
  client_id: number | null;
  client_name: string | null;
  totp_enabled?: boolean;
}

export interface Totals {
  submitted: number;
  rejected: number;
  sent: number;
  failed: number;
  delivered: number;
  undelivered: number;
  parts: number;
  revenue: number;
  cost: number;
  avg_dlr_ms: number;
}

export interface SeriesPoint {
  t: string;
  submitted: number;
  sent: number;
  delivered: number;
  undelivered: number;
  failed: number;
  revenue: number;
  cost: number;
}

export interface Overview {
  from: string;
  to: string;
  bucket: "hour" | "day" | "month";
  totals: Totals;
  previous: Totals;
  series: SeriesPoint[];
}

export interface BreakdownRow {
  key: string;
  label: string;
  submitted: number;
  sent: number;
  delivered: number;
  undelivered: number;
  failed: number;
  revenue: number;
  cost: number;
  margin: number;
  dlr_rate: number;
  avg_dlr_ms: number;
}

export interface BindStatus {
  index: number;
  state: "connecting" | "bound" | "down";
  since: string;
  error?: string;
}

export interface ConnectionStatus {
  id: number;
  name: string;
  enabled: boolean;
  binds: BindStatus[];
  sent: number;
  errors: number;
  in_flight: number;
}

export interface Live {
  series: { t: number; in: number; out: number; dlr: number }[];
  queue?: number;
  dlr_outbox?: number;
  connections?: Record<string, ConnectionStatus>;
  client_binds?: { account_id: number; system_id: string; mode: string; remote: string }[];
}

export interface Message {
  id: string;
  created_at: string;
  client_id: number;
  client_name: string;
  account_id: number | null;
  direction: string;
  source: string;
  destination: string;
  body: string;
  parts: number;
  country_iso: string | null;
  network_name: string | null;
  connection_name: string | null;
  route_id: number | null;
  route_name?: string;
  status: string;
  attempts: number;
  price: number;
  cost: number;
  client_ref: string;
  error: string;
  dlr_status: string;
  dlr_error: string;
  sent_at: string | null;
  dlr_at: string | null;
  dlr_sent_at: string | null;
  dlr_ms: number | null;
  ledger?: { id: number; kind: string; amount: number; created_at: string }[];
}

export interface Lookups {
  countries: { iso: string; name: string; dial_code: string }[];
  clients: { id: number; name: string }[];
  accounts: { id: number; username: string; kind: string; client_id: number }[];
  connections: { id: number; name: string; vendor_id: number }[];
  vendors: { id: number; name: string }[];
  users: { id: number; name: string; role: Role }[];
}

export interface List<T> {
  total: number;
  items: T[];
}
