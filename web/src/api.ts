export interface Device {
  id: string;
  name: string;
  hostname: string;
  os: string;
  arch: string;
  lanIPs: string[];
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
  token?: string;
}

export interface AuditEntry {
  id: number;
  actor: string;
  action: string;
  target: string;
  detail: string;
  createdAt: string;
}

const TOKEN_KEY = "device-relay-token";

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || "";
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${getToken()}`);
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(path, { ...init, headers });
  if (!res.ok) {
    throw new Error(`request failed: ${res.status}`);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return res.json() as Promise<T>;
}

export function listDevices(): Promise<Device[]> {
  return api<Device[]>("/api/devices");
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
  });
  if (!res.ok) {
    throw new Error("invalid credentials");
  }
  const data = (await res.json()) as { token: string };
  setToken(data.token);
}

export function getStats(): Promise<Stats> {
  return api<Stats>("/api/stats");
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
