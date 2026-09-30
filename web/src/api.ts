export interface Device {
  id: string;
  name: string;
  hostname: string;
  os: string;
  arch: string;
  lanIPs: string[];
  group?: string;
  /** 'any' (default) or 'restricted'. Always serialized by the server. */
  forwardPolicy: string;
  createdAt: string;
  lastSeen: string;
  online: boolean;
}

export interface Stats {
  total: number;
  online: number;
  groups: string[];
  byOS: Record<string, number>;
  recentActivity: AuditEntry[];
}

export interface DeviceKey {
  deviceId: string;
  name: string;
  createdAt: string;
  expiresAt?: string;
  token?: string;
}

export interface Invite {
  code: string;
  name: string;
  expiresAt: string;
  createdAt: string;
}

export interface UserAccount {
  id: number;
  username: string;
  role: string;
  deviceIds: string[];
  expiresAt?: string;
  createdAt: string;
}

export interface AuditEntry {
  id: number;
  actor: string;
  action: string;
  target: string;
  detail: string;
  createdAt: string;
}

export interface AnalyticsBucket {
  startedAt: string;
  bytesToDevice: number;
  bytesFromDevice: number;
}

export interface ConnectionRecord {
  id: string;
  actor: string;
  deviceId: string;
  deviceName: string;
  peerDeviceId?: string;
  peerDeviceName?: string;
  service: string;
  transport: string;
  state: string;
  clientIp?: string;
  bytesToDevice: number;
  bytesFromDevice: number;
  startedAt: string;
  endedAt?: string;
}

export interface AnalyticsData {
  period: "24h" | "7d";
  sessions: number;
  active: number;
  directSessions: number;
  relaySessions: number;
  directTunnels: number;
  relayTunnels: number;
  relayBytes: number;
  buckets: AnalyticsBucket[];
  recent: ConnectionRecord[];
}

const TOKEN_KEY = "warpmesh-token";

/**
 * Legacy session token kept for CLI-style header auth.
 *
 * The browser session now lives in an HttpOnly cookie set by /api/login, so
 * this value is normally empty and is never placed in a URL.
 */
export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || "";
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

/** Whether a session is present. The cookie is HttpOnly, so the console keeps
 *  a non-secret marker rather than the credential itself. */
const SESSION_FLAG = "warpmesh-session";
export function hasSession(): boolean {
  return localStorage.getItem(SESSION_FLAG) === "1";
}
export function setSessionFlag(): void {
  localStorage.setItem(SESSION_FLAG, "1");
}
export function clearSessionFlag(): void {
  localStorage.removeItem(SESSION_FLAG);
}


/**
 * Normalise a server timestamp that means "no value".
 *
 * Go's time.Time zero value is a struct and is never omitted by `omitempty`,
 * so it arrives as "0001-01-01T00:00:00Z" and would render as "1/1/1".
 */
export function optionalDate(value?: string | null): string | undefined {
  if (!value) return undefined;
  if (value.startsWith("0001-01-01")) return undefined;
  return value;
}

/** Error carrying the HTTP status so callers can distinguish 401 from a blip. */
export class ApiError extends Error {
  readonly status: number;

  constructor(status: number) {
    super(`request failed: ${status}`);
    this.name = "ApiError";
    this.status = status;
  }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  // The browser session travels in an HttpOnly cookie; a token is only present
  // for non-browser clients that still use header auth.
  const token = getToken();
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(path, { ...init, headers });
  if (!res.ok) {
    throw new ApiError(res.status);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return res.json() as Promise<T>;
}

export function listDevices(): Promise<Device[]> {
  return api<Device[]>("/api/devices");
}

/**
 * Change a device's forwarding policy.
 *
 * "any" (the default for a newly enrolled device) lets it reach any other
 * device on any port, so a personal fleet needs no per-device configuration.
 * "restricted" limits it to explicit grants.
 */
export function setForwardPolicy(id: string, policy: "any" | "restricted"): Promise<Device> {
  return api<Device>(`/api/devices/${id}`, { method: "PATCH", body: JSON.stringify({ forwardPolicy: policy }) });
}

export function renameDevice(id: string, name: string): Promise<Device> {
  return api<Device>(`/api/devices/${id}`, {
    method: "PATCH",
    body: JSON.stringify({ name }),
  });
}

export function deleteDevice(id: string): Promise<void> {
  return api<void>(`/api/devices/${id}`, { method: "DELETE" });
}

export async function login(username: string, password: string): Promise<void> {
  const res = await fetch("/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
    // The server sets an HttpOnly session cookie on this response.
    credentials: "same-origin",
  });
  if (!res.ok) {
    throw new Error("invalid credentials");
  }
  // Drop any token from an older build so it cannot leak into a URL later.
  clearToken();
  setSessionFlag();
}

export async function logout(): Promise<void> {
  try {
    await api<void>("/api/logout", { method: "POST" });
  } finally {
    clearToken();
    clearSessionFlag();
  }
}

export function getStats(): Promise<Stats> {
  return api<Stats>("/api/stats");
}

export function getAnalytics(range: "24h" | "7d"): Promise<AnalyticsData> {
  return api<AnalyticsData>(`/api/analytics?range=${range}`);
}

export function listDeviceKeys(): Promise<DeviceKey[]> {
  return api<DeviceKey[]>("/api/device-keys");
}

export function createDeviceKey(name: string): Promise<DeviceKey> {
  return api<DeviceKey>("/api/device-keys", {
    method: "POST",
    body: JSON.stringify({ name }),
  });
}

export function revokeDeviceKey(deviceId: string): Promise<void> {
  return api<void>(`/api/device-keys/${deviceId}`, { method: "DELETE" });
}

export function rotateDeviceKey(deviceId: string): Promise<DeviceKey> {
  return api<DeviceKey>(`/api/device-keys/${deviceId}/rotate`, { method: "POST" });
}

export function listInvites(): Promise<Invite[]> {
  return api<Invite[]>("/api/invites");
}

export function createInvite(name: string): Promise<Invite> {
  return api<Invite>("/api/invites", { method: "POST", body: JSON.stringify({ name }) });
}

export function deleteInvite(code: string): Promise<void> {
  return api<void>(`/api/invites/${code}`, { method: "DELETE" });
}

export function listUsers(): Promise<UserAccount[]> {
  return api<UserAccount[]>("/api/users");
}

export function createUser(input: {
  username: string;
  password: string;
  role: string;
  deviceIds: string[];
  expiresAt?: string;
}): Promise<void> {
  return api<void>("/api/users", { method: "POST", body: JSON.stringify(input) });
}

export function updateUser(username: string, patch: {
  password?: string;
  role?: string;
  deviceIds?: string[];
  /** null clears the expiry; the server parses this as *time.Time. */
  expiresAt?: string | null;
}): Promise<void> {
  return api<void>(`/api/users/${username}`, { method: "PATCH", body: JSON.stringify(patch) });
}

export function deleteUser(username: string): Promise<void> {
  return api<void>(`/api/users/${username}`, { method: "DELETE" });
}

export function listAudit(limit = 50): Promise<AuditEntry[]> {
  return api<AuditEntry[]>(`/api/audit?limit=${limit}`);
}

/**
 * Build a WebSocket URL.
 *
 * Credentials are deliberately absent: the upgrade is authorized by a
 * single-use ticket carried in the subprotocol header, so nothing secret ends
 * up in proxy logs, browser history or Referer headers.
 */
export function wsUrl(path: string, params: Record<string, string | number>): string {
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const query = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    query.set(k, String(v));
  }
  return `${proto}://${location.host}${path}?${query.toString()}`;
}

export function lastSeenLabel(iso: string): string {
  if (!iso) return "never";
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 60_000) return "just now";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return `${Math.floor(diff / 86_400_000)}d ago`;
}
