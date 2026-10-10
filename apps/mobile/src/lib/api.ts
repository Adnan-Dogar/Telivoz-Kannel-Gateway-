// API client for the gateway. The app signs in with { token: true } and sends the session token as a Bearer
// header; the token and the server address live in the device's secure storage.
import { storage } from "./storage";

const SERVER_KEY = "telivoz.server";
const TOKEN_KEY = "telivoz.token";

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

let server = "";
let token = "";
let onUnauthorized: (() => void) | null = null;

export async function loadCredentials() {
  server = (await storage.get(SERVER_KEY)) ?? "";
  token = (await storage.get(TOKEN_KEY)) ?? "";
  return { server, signedIn: token !== "" };
}

export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

export function normalizeServer(input: string) {
  let s = input.trim().replace(/\/+$/, "");
  if (s && !/^https?:\/\//i.test(s)) s = `https://${s}`;
  return s;
}

async function request<T>(method: string, path: string, body?: unknown, base = server): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json", "X-Requested-With": "telivoz" };
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  let res: Response;
  try {
    res = await fetch(base + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch {
    throw new ApiError(0, "network", "Cannot reach the server. Check the address and your connection.");
  }
  if (res.status === 401 && !path.startsWith("/api/auth/login")) onUnauthorized?.();
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    /* not JSON */
  }
  if (!res.ok) throw new ApiError(res.status, data?.error ?? "error", data?.message ?? `Request failed (${res.status})`);
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body: unknown = {}) => request<T>("POST", path, body),
};

export async function signIn(address: string, email: string, password: string, code?: string) {
  const base = normalizeServer(address);
  const out = await request<{ token: string; user: Me }>(
    "POST",
    "/api/auth/login",
    { email, password, code: code || undefined, token: true },
    base,
  );
  server = base;
  token = out.token;
  await storage.set(SERVER_KEY, base);
  await storage.set(TOKEN_KEY, out.token);
  return out.user;
}

export async function signOut() {
  try {
    if (token) await request("POST", "/api/auth/logout", {});
  } catch {
    /* already gone */
  }
  token = "";
  await storage.remove(TOKEN_KEY);
}

export function qs(params: Record<string, string | number | undefined | null>) {
  const p = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== null && v !== "")
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
  return p.length ? `?${p.join("&")}` : "";
}

export function currentServer() {
  return server;
}

// ---- API types (subset of the portal's) -------------------------------------------------------------

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

export interface Overview {
  bucket: "hour" | "day" | "month";
  totals: Totals;
  previous: Totals;
  series: { t: string; submitted: number; delivered: number; failed: number; revenue: number }[];
}

export interface ConnectionStatus {
  id: number;
  name: string;
  enabled: boolean;
  binds: { index: number; state: "connecting" | "bound" | "down"; since: string; error?: string }[];
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
  client_name: string;
  direction: string;
  source: string;
  destination: string;
  body: string;
  parts: number;
  country_iso: string | null;
  network_name: string | null;
  connection_name: string | null;
  status: string;
  price: number;
  error: string;
  dlr_status: string;
  dlr_error: string;
  dlr_ms: number | null;
}
