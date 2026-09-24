import { useCallback, useEffect, useRef, useState } from "react";
import {
  Activity,
  ArrowLeft,
  Download,
  FileText,
  Folder,
  FolderPlus,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Monitor,
  MonitorSmartphone,
  Pencil,
  RefreshCw,
  ScrollText,
  Server,
  ShieldCheck,
  TerminalSquare,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import {
  clearToken,
  createDeviceKey,
  createInvite,
  createUser,
  deleteInvite,
  deleteDevice,
  deleteUser,
  getAnalytics,
  getToken,
  lastSeenLabel,
  listAudit,
  listDeviceKeys,
  listDevices,
  listInvites,
  listUsers,
  login,
  logout,
  renameDevice,
  revokeDeviceKey,
  rotateDeviceKey,
  updateUser,
  type AuditEntry,
  type AnalyticsData,
  type ConnectionRecord,
  type Device,
  type DeviceKey,
  type Invite,
  type UserAccount,
  wsUrl,
} from "./api";

type Page = "dashboard" | "devices" | "access" | "activity";

const PAGE_PATHS: Record<Page, string> = {
  dashboard: "/",
  devices: "/devices",
  access: "/access",
  activity: "/activity",
};

const PAGES_BY_PATH: Record<string, Page> = {
  "/": "dashboard",
  "/devices": "devices",
  "/access": "access",
  "/activity": "activity",
};

type DeviceRouteKind = "terminal" | "desktop" | "files";

function deviceView(kind: DeviceRouteKind, device: Device): Exclude<View, { name: "page" }> {
  switch (kind) {
    case "terminal":
      return { name: "terminal", device };
    case "desktop":
      return { name: "desktop", device };
    case "files":
      return { name: "files", device };
  }
}

function normalizedPath(path: string): string {
  return path.split("?")[0].replace(/\/+$/, "") || "/";
}

function pageFromPath(path: string): Page {
  return PAGES_BY_PATH[normalizedPath(path)] ?? "dashboard";
}

type View =
  | { name: "page"; page: Page }
  | { name: "terminal"; device: Device }
  | { name: "desktop"; device: Device }
  | { name: "files"; device: Device };

export default function App() {
  const [token, setTokenState] = useState(getToken());
  const [view, setView] = useState<View>(() => ({ name: "page", page: pageFromPath(location.pathname) }));
  const routeSeq = useRef(0);

  const applyLocation = useCallback(async () => {
    const seq = ++routeSeq.current;
    const deviceMatch =
      location.pathname.match(/^\/devices\/([^/]+)\/(terminal|desktop|files)$/) ||
      location.hash.match(/^#\/(desktop|terminal|files)\/(.+)$/);
    if (!deviceMatch) {
      if (routeSeq.current === seq) {
        setView({ name: "page", page: pageFromPath(location.pathname) });
      }
      return;
    }
    const kind = deviceMatch[1] as DeviceRouteKind;
    const id = decodeURIComponent(deviceMatch[2]);
    try {
      const devices = await listDevices();
      const device = devices.find((d) => d.id === id);
      if (device && routeSeq.current === seq) {
        setView(deviceView(kind, device));
      }
    } catch {
      // deep links resolve lazily; the console stays usable if the lookup fails
    }
  }, []);

  useEffect(() => {
    if (!token) {
      return;
    }
    void applyLocation();
    window.addEventListener("popstate", applyLocation);
    return () => window.removeEventListener("popstate", applyLocation);
  }, [token, applyLocation]);

  const goToPath = useCallback((path: string) => {
    if (normalizedPath(location.pathname) !== normalizedPath(path)) {
      history.pushState({}, "", path);
    }
  }, []);

  const goToPage = useCallback(
    (page: Page) => {
      goToPath(PAGE_PATHS[page]);
      setView({ name: "page", page });
    },
    [goToPath],
  );

  const goToDevices = useCallback(() => {
    history.replaceState({}, "", PAGE_PATHS.devices);
    setView({ name: "page", page: "devices" });
  }, []);

  const openDeviceView = useCallback(
    (kind: DeviceRouteKind, device: Device) => {
      goToPath(`/devices/${encodeURIComponent(device.id)}/${kind}`);
      setView(deviceView(kind, device));
    },
    [goToPath],
  );

  if (!token) {
    return <Login onLogin={() => setTokenState(getToken())} />;
  }

  if (view.name === "terminal") {
    return <TerminalView device={view.device} onBack={goToDevices} />;
  }

  if (view.name === "desktop") {
    return <DesktopView device={view.device} onBack={goToDevices} />;
  }

  if (view.name === "files") {
    return <FileManagerView device={view.device} onBack={goToDevices} />;
  }

  return (
    <Console
      page={view.page}
      onNavigate={goToPage}
      onTerminal={(d) => openDeviceView("terminal", d)}
      onDesktop={(d) => openDeviceView("desktop", d)}
      onFiles={(d) => openDeviceView("files", d)}
      onLogout={async () => {
        try {
          await logout();
        } catch {
          // local logout must still work when the server is unreachable
        }
        history.replaceState({}, "", "/");
        clearToken();
        setTokenState("");
      }}
    />
  );
}

function Login({ onLogin }: { onLogin: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      await login(username.trim(), password);
      onLogin();
    } catch (err) {
      setError(err instanceof Error ? err.message : "login failed");
    }
  };

  return (
    <div className="login-wrap">
      <div className="login-panel">
        <div className="brand-mark">
          <Server size={22} />
        </div>
        <h1>WarpMesh</h1>
        <p>Centralized control plane for your devices.</p>
        <form onSubmit={submit}>
          <label htmlFor="username">Username</label>
          <input
            id="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="admin"
            autoFocus
          />
          <label htmlFor="password">Password</label>
          <input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="••••••••"
          />
          {error && <div className="form-error">{error}</div>}
          <button type="submit" className="primary-btn">
            Enter console
          </button>
        </form>
      </div>
    </div>
  );
}

function Console({
  page,
  onNavigate,
  onTerminal,
  onDesktop,
  onFiles,
  onLogout,
}: {
  page: Page;
  onNavigate: (page: Page) => void;
  onTerminal: (d: Device) => void;
  onDesktop: (d: Device) => void;
  onFiles: (d: Device) => void;
  onLogout: () => void;
}) {
  const [devices, setDevices] = useState<Device[]>([]);
  const [analytics, setAnalytics] = useState<AnalyticsData | null>(null);
  const [analyticsRange, setAnalyticsRange] = useState<"24h" | "7d">("24h");
  const [analyticsLoading, setAnalyticsLoading] = useState(true);
  const [keys, setKeys] = useState<DeviceKey[]>([]);
  const [invites, setInvites] = useState<Invite[]>([]);
  const [users, setUsers] = useState<UserAccount[]>([]);
  const [canManageUsers, setCanManageUsers] = useState(false);
  const [audit, setAudit] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const deviceList = await listDevices();
      setDevices(deviceList);
    } catch {
      onLogout();
    } finally {
      setLoading(false);
    }
  }, [onLogout]);

  const loadAnalytics = useCallback(async () => {
    setAnalyticsLoading(true);
    try {
      setAnalytics(await getAnalytics(analyticsRange));
    } catch {
      setAnalytics(null);
    } finally {
      setAnalyticsLoading(false);
    }
  }, [analyticsRange]);

  const loadKeys = useCallback(async () => {
    try {
      setKeys(await listDeviceKeys());
    } catch {
      // secondary panel; dashboard keeps working
    }
  }, []);

  const loadAudit = useCallback(async () => {
    try {
      setAudit(await listAudit(100));
    } catch {
      // secondary panel; dashboard keeps working
    }
  }, []);

  const loadInvites = useCallback(async () => {
    try {
      setInvites(await listInvites());
    } catch {
      // secondary panel; dashboard keeps working
    }
  }, []);

  const loadUsers = useCallback(async () => {
    try {
      setUsers(await listUsers());
      setCanManageUsers(true);
    } catch {
      setCanManageUsers(false);
    }
  }, []);

  useEffect(() => {
    refresh();
    loadKeys();
    loadAudit();
    loadInvites();
    loadUsers();
    const timer = setInterval(refresh, 5000);
    return () => clearInterval(timer);
  }, [refresh, loadKeys, loadAudit, loadInvites, loadUsers]);

  useEffect(() => {
    void loadAnalytics();
    const timer = setInterval(loadAnalytics, 15000);
    return () => clearInterval(timer);
  }, [loadAnalytics]);

  const handleCreateKey = async () => {
    const name = prompt("Device name for the new key");
    if (!name?.trim()) return;
    try {
      const key = await createDeviceKey(name.trim());
      alert(`Device key created.\n\nDevice ID: ${key.deviceId}\nToken: ${key.token}\n\nUse both in the agent.`);
      loadKeys();
    } catch (err) {
      alert(err instanceof Error ? err.message : "failed to create key");
    }
  };

  const handleRevokeKey = async (key: DeviceKey) => {
    if (confirm(`Revoke device key for ${key.name}?`)) {
      await revokeDeviceKey(key.deviceId);
      loadKeys();
    }
  };

  const handleRotateKey = async (key: DeviceKey) => {
    if (confirm(`Rotate the device key for ${key.name}? The old token stops working.`)) {
      try {
        const rotated = await rotateDeviceKey(key.deviceId);
        alert(`New device token:\n\n${rotated.token}\n\nIt is shown once.`);
        loadKeys();
      } catch (err) {
        alert(err instanceof Error ? err.message : "rotation failed");
      }
    }
  };

  const handleCreateInvite = async () => {
    const name = prompt("Device name for the invite");
    if (!name?.trim()) return;
    try {
      const inv = await createInvite(name.trim());
      alert(`Invite code:\n\n${inv.code}\n\nRun on the device:\nwarpmesh-agent enroll -server https://warpmesh.ddns.net -code ${inv.code} -name ${name.trim()}`);
      loadInvites();
    } catch (err) {
      alert(err instanceof Error ? err.message : "invite creation failed");
    }
  };

  const handleDeleteInvite = async (inv: Invite) => {
    if (confirm(`Delete invite ${inv.code}?`)) {
      await deleteInvite(inv.code);
      loadInvites();
    }
  };

  const title = page === "dashboard" ? "Dashboard" : page === "devices" ? "Devices" : page === "access" ? "Access" : "Activity";

  return (
    <div className="console">
      <aside className="sidebar">
        <div className="sidebar-brand">
          <div className="brand-mark small">
            <Server size={18} />
          </div>
          <div>
            <strong>WarpMesh</strong>
            <span>control plane</span>
          </div>
        </div>
        <nav className="sidebar-nav">
          <a
            className={page === "dashboard" ? "active" : ""}
            href={PAGE_PATHS.dashboard}
            onClick={(e) => {
              e.preventDefault();
              onNavigate("dashboard");
            }}
          >
            <LayoutDashboard size={17} />
            Dashboard
          </a>
          <a
            className={page === "devices" ? "active" : ""}
            href={PAGE_PATHS.devices}
            onClick={(e) => {
              e.preventDefault();
              onNavigate("devices");
            }}
          >
            <MonitorSmartphone size={17} />
            Devices
          </a>
          <a
            className={page === "access" ? "active" : ""}
            href={PAGE_PATHS.access}
            onClick={(e) => {
              e.preventDefault();
              onNavigate("access");
            }}
          >
            <KeyRound size={17} />
            Access
          </a>
          <a
            className={page === "activity" ? "active" : ""}
            href={PAGE_PATHS.activity}
            onClick={(e) => {
              e.preventDefault();
              onNavigate("activity");
            }}
          >
            <ScrollText size={17} />
            Activity
          </a>
        </nav>
        <div className="sidebar-foot">
          <div className="stat-pill">
            <Activity size={14} />
            {devices.filter((device) => device.online).length}/{devices.length} online
          </div>
          <button className="icon-btn" onClick={onLogout} title="Log out">
            <LogOut size={15} />
          </button>
        </div>
      </aside>

      <section className="main">
        <header className="topbar">
          <div className="page-title">
            <h1>{title}</h1>
            <p>{page === "dashboard" ? "Connections and traffic" : page === "devices" ? "Managed devices" : page === "access" ? "Device credentials" : "Security events"}</p>
          </div>
          <button className="icon-btn" onClick={() => { void refresh(); void loadAnalytics(); }} title="Refresh" disabled={loading || analyticsLoading}>
            <RefreshCw size={16} className={loading ? "spin" : ""} />
          </button>
        </header>

        <div className="content">
          {page === "dashboard" && (
            <DashboardPage
              analytics={analytics}
              loading={analyticsLoading}
              range={analyticsRange}
              onRangeChange={setAnalyticsRange}
            />
          )}
          {page === "devices" && (
            <DevicesPage devices={devices} onTerminal={onTerminal} onDesktop={onDesktop} onFiles={onFiles} />
          )}
          {page === "access" && (
            <AccessPage
              keys={keys}
              invites={invites}
              users={users}
              devices={devices}
              canManageUsers={canManageUsers}
              onUsersChanged={loadUsers}
              onCreate={handleCreateKey}
              onRevoke={handleRevokeKey}
              onRotate={handleRotateKey}
              onCreateInvite={handleCreateInvite}
              onDeleteInvite={handleDeleteInvite}
            />
          )}
          {page === "activity" && <ActivityPage audit={audit} />}
        </div>
      </section>
    </div>
  );
}

function DashboardPage({
  analytics,
  loading,
  range,
  onRangeChange,
}: {
  analytics: AnalyticsData | null;
  loading: boolean;
  range: "24h" | "7d";
  onRangeChange: (range: "24h" | "7d") => void;
}) {
  const buckets = analytics?.buckets ?? [];
  const maxTraffic = Math.max(1, ...buckets.map((bucket) => bucket.bytesToDevice + bucket.bytesFromDevice));
  const knownRoutes = (analytics?.directTunnels ?? 0) + (analytics?.relayTunnels ?? 0);
  const directPercent = knownRoutes ? Math.round(((analytics?.directTunnels ?? 0) / knownRoutes) * 100) : 0;
  return (
    <div className="page-grid dashboard-page">
      <div className="dashboard-heading">
        <div>
          <h2>Connection overview</h2>
          <p>Session history and traffic observed by this WarpMesh server.</p>
        </div>
        <div className="range-switch" aria-label="Analytics time range">
          <button className={range === "24h" ? "active" : ""} onClick={() => onRangeChange("24h")}>24 hours</button>
          <button className={range === "7d" ? "active" : ""} onClick={() => onRangeChange("7d")}>7 days</button>
        </div>
      </div>

      <section className="stat-grid dashboard-stat-grid">
        <div className="stat-card">
          <Activity size={18} />
          <strong>{analytics?.sessions ?? 0}</strong>
          <span>Connections</span>
        </div>
        <div className="stat-card accent">
          <MonitorSmartphone size={18} />
          <strong>{analytics?.active ?? 0}</strong>
          <span>Active sessions</span>
        </div>
        <div className="stat-card">
          <ShieldCheck size={18} />
          <strong>{analytics?.directSessions ?? 0}</strong>
          <span>Direct tunnels</span>
        </div>
        <div className="stat-card">
          <Download size={18} />
          <strong>{formatTrafficBytes(analytics?.relayBytes ?? 0)}</strong>
          <span>Server relay traffic</span>
        </div>
      </section>

      <section className="panel traffic-panel">
        <div className="panel-head">
          <div>
            <h3>Relayed traffic</h3>
            <p>Bytes passing through the VPS, grouped by {range === "24h" ? "hour" : "day"}</p>
          </div>
          <span className="range-caption">{range === "24h" ? "Last 24 hours" : "Last 7 days"}</span>
        </div>
        {loading && !analytics ? (
          <div className="inline-empty">Loading connection data…</div>
        ) : buckets.length === 0 ? (
          <div className="inline-empty">No traffic data for this period.</div>
        ) : (
          <>
            <div className={`traffic-chart ${range === "7d" ? "weekly" : ""}`} role="img" aria-label="Server-relayed bytes by time bucket">
              {buckets.map((bucket, index) => {
                const toHeight = (bucket.bytesToDevice / maxTraffic) * 100;
                const fromHeight = (bucket.bytesFromDevice / maxTraffic) * 100;
                const showLabel = range === "24h" ? index % 6 === 0 || index === buckets.length - 1 : true;
                return (
                  <div className="traffic-column" key={bucket.startedAt}>
                    <div
                      className="traffic-bar"
                      title={`${new Date(bucket.startedAt).toLocaleString()} · to device ${formatTrafficBytes(bucket.bytesToDevice)} · from device ${formatTrafficBytes(bucket.bytesFromDevice)}`}
                    >
                      <span className="traffic-from" style={{ height: `${fromHeight}%` }} />
                      <span className="traffic-to" style={{ height: `${toHeight}%` }} />
                    </div>
                    <small>{showLabel ? chartLabel(bucket.startedAt, range) : ""}</small>
                  </div>
                );
              })}
            </div>
            <div className="traffic-legend">
              <span><i className="legend-to" />To device</span>
              <span><i className="legend-from" />From device</span>
            </div>
          </>
        )}
        <p className="chart-note">Terminal, desktop, file and relayed tunnel traffic is measured here. Direct tunnels bypass the VPS, so their payload bytes are not included.</p>
      </section>

      <div className="dashboard-lower-grid">
        <section className="panel route-panel">
          <div className="panel-head">
            <div>
              <h3>Connection path</h3>
              <p>How tunnel sessions reached their destination</p>
            </div>
          </div>
          {knownRoutes === 0 ? (
            <div className="inline-empty">No direct or relayed tunnel sessions in this period.</div>
          ) : (
            <>
              <div className="route-split-bar" aria-label={`${directPercent}% direct, ${100 - directPercent}% relay`}>
                <span className="route-direct" style={{ width: `${directPercent}%` }} />
                <span className="route-relay" style={{ width: `${100 - directPercent}%` }} />
              </div>
              <div className="route-counts">
                <span><i className="route-direct-dot" />Direct <strong>{analytics?.directTunnels ?? 0}</strong></span>
                <span><i className="route-relay-dot" />Server relay <strong>{analytics?.relayTunnels ?? 0}</strong></span>
              </div>
            </>
          )}
          <p className="chart-note">Path is reported for device-to-device TCP forwarding. Browser terminal, desktop and file sessions always use the server relay.</p>
        </section>

        <section className="panel recent-connections-panel">
          <div className="panel-head">
            <div>
              <h3>Recent connections</h3>
              <p>Most recent sessions in the selected period</p>
            </div>
            <span className="range-caption">{analytics?.recent.length ?? 0} shown</span>
          </div>
          {!analytics?.recent.length ? (
            <div className="inline-empty">{loading ? "Loading connection data…" : "No connections recorded for this period."}</div>
          ) : (
            <div className="connection-table-wrap">
              <table className="connection-table">
                <thead>
                  <tr><th>Started</th><th>Connection</th><th>Path</th><th>Status</th><th>Traffic</th><th>Source</th></tr>
                </thead>
                <tbody>
                  {analytics.recent.map((entry) => (
                    <tr key={entry.id}>
                      <td><span>{new Date(entry.startedAt).toLocaleString()}</span><small>{connectionDuration(entry)}</small></td>
                      <td><strong>{serviceLabel(entry.service)}</strong><small>{connectionEndpoints(entry)}</small></td>
                      <td><span className={`path-badge ${entry.transport}`}>{transportLabel(entry.transport)}</span></td>
                      <td><span className={`state-badge ${entry.state}`}>{stateLabel(entry.state)}</span></td>
                      <td><span>From device {formatTrafficBytes(entry.bytesFromDevice)}</span><small>To device {formatTrafficBytes(entry.bytesToDevice)}</small></td>
                      <td><span>{entry.actor}</span><small>{entry.clientIp || "IP unavailable"}</small></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>
      {!analytics && !loading && <div className="analytics-error" role="status">Connection analytics could not be loaded. Refresh to retry.</div>}
    </div>
  );
}

function formatTrafficBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
}

function chartLabel(value: string, range: "24h" | "7d"): string {
  const date = new Date(value);
  return range === "24h"
    ? date.toLocaleTimeString([], { hour: "2-digit", hour12: false })
    : date.toLocaleDateString([], { month: "numeric", day: "numeric" });
}

function connectionDuration(entry: ConnectionRecord): string {
  const end = entry.endedAt ? new Date(entry.endedAt).getTime() : Date.now();
  const seconds = Math.max(0, Math.floor((end - new Date(entry.startedAt).getTime()) / 1000));
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}

function serviceLabel(service: string): string {
  if (service === "terminal") return "Remote terminal";
  if (service === "desktop") return "Remote desktop";
  if (service === "files-upload") return "File upload";
  if (service === "files-download") return "File download";
  if (service.startsWith("tcp:")) return `TCP ${service.slice(4)}`;
  return service;
}

function connectionEndpoints(entry: ConnectionRecord): string {
  return entry.peerDeviceName ? `${entry.deviceName} ↔ ${entry.peerDeviceName}` : entry.deviceName;
}

function transportLabel(transport: string): string {
  if (transport === "direct") return "Direct";
  if (transport === "relay") return "Server relay";
  return "Negotiating";
}

function stateLabel(state: string): string {
  if (state === "active" || state === "pending") return "Active";
  if (state === "failed") return "Failed";
  if (state === "interrupted") return "Interrupted";
  return "Closed";
}

function DevicesPage({
  devices,
  onTerminal,
  onDesktop,
  onFiles,
}: {
  devices: Device[];
  onTerminal: (d: Device) => void;
  onDesktop: (d: Device) => void;
  onFiles: (d: Device) => void;
}) {
  const [group, setGroup] = useState("");
  const groups = Array.from(new Set(devices.map((d) => d.group).filter((value): value is string => Boolean(value))));
  const filtered = group ? devices.filter((d) => d.group === group) : devices;

  const handleRename = async (device: Device) => {
    const name = prompt("New device name", device.name);
    if (name && name.trim()) {
      await renameDevice(device.id, name.trim());
      location.reload();
    }
  };

  const handleDelete = async (device: Device) => {
    if (confirm(`Remove ${device.name} from the catalog?`)) {
      await deleteDevice(device.id);
      location.reload();
    }
  };

  return (
    <>
      {groups.length > 0 && (
        <div className="filter-chips">
          <button className={group === "" ? "active" : ""} onClick={() => setGroup("")}>
            All
          </button>
          {groups.map((g) => (
            <button key={g} className={group === g ? "active" : ""} onClick={() => setGroup(g)}>
              {g}
            </button>
          ))}
        </div>
      )}
      <section className="panel">
        {filtered.length === 0 ? (
          <div className="inline-empty">No devices yet. Run an agent with a device key and it will appear here.</div>
        ) : (
          <div className="device-table">
            <div className="device-row device-row-head">
              <span>Status</span>
              <span>Name</span>
              <span>System</span>
              <span>Network</span>
              <span>Last seen</span>
              <span>Actions</span>
            </div>
            {filtered.map((device) => (
              <div className="device-row" key={device.id}>
                <span>
                  <span className={`status-dot ${device.online ? "online" : "offline"}`} />
                  {device.online ? "Online" : "Offline"}
                </span>
                <span>
                  <strong>{device.name}</strong>
                  <small>{device.hostname || device.id}</small>
                  {device.group && <em>{device.group}</em>}
                </span>
                <span>
                  {device.os} / {device.arch}
                </span>
                <span>{device.lanIPs?.join(", ") || "no LAN IP"}</span>
                <span>{lastSeenLabel(device.lastSeen)}</span>
                <span className="row-actions">
                  <button className="icon-btn" title="Remote desktop" disabled={!device.online} onClick={() => onDesktop(device)}>
                    <Monitor size={15} />
                  </button>
                  <button className="icon-btn" title="Remote terminal" disabled={!device.online} onClick={() => onTerminal(device)}>
                    <TerminalSquare size={15} />
                  </button>
                  <button className="icon-btn" title="File transfer" disabled={!device.online} onClick={() => onFiles(device)}>
                    <Download size={15} />
                  </button>
                  <button className="icon-btn" title="Rename" onClick={() => handleRename(device)}>
                    <Pencil size={14} />
                  </button>
                  <button className="icon-btn danger" title="Delete" onClick={() => handleDelete(device)}>
                    <Trash2 size={14} />
                  </button>
                </span>
              </div>
            ))}
          </div>
        )}
      </section>
    </>
  );
}

function AccessPage({
  keys,
  invites,
  users,
  devices,
  canManageUsers,
  onUsersChanged,
  onCreate,
  onRevoke,
  onRotate,
  onCreateInvite,
  onDeleteInvite,
}: {
  keys: DeviceKey[];
  invites: Invite[];
  users: UserAccount[];
  devices: Device[];
  canManageUsers: boolean;
  onUsersChanged: () => Promise<void>;
  onCreate: () => void;
  onRevoke: (key: DeviceKey) => void;
  onRotate: (key: DeviceKey) => void;
  onCreateInvite: () => void;
  onDeleteInvite: (inv: Invite) => void;
}) {
  const [newUsername, setNewUsername] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [newRole, setNewRole] = useState("operator");
  const [newExpiry, setNewExpiry] = useState("");
  const [newDevices, setNewDevices] = useState<string[]>([]);
  const [showCreateUser, setShowCreateUser] = useState(false);
  const [savingUser, setSavingUser] = useState(false);
  const [createUserError, setCreateUserError] = useState("");

  const resetNewUserForm = useCallback(() => {
    setNewUsername("");
    setNewPassword("");
    setNewRole("operator");
    setNewExpiry("");
    setNewDevices([]);
    setCreateUserError("");
  }, []);

  const closeCreateUser = useCallback(() => {
    if (savingUser) return;
    setShowCreateUser(false);
    resetNewUserForm();
  }, [resetNewUserForm, savingUser]);

  useEffect(() => {
    if (!showCreateUser) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !savingUser) closeCreateUser();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [showCreateUser, savingUser, closeCreateUser]);

  const submitCreateUser = async (e: React.FormEvent) => {
    e.preventDefault();
    setSavingUser(true);
    setCreateUserError("");
    try {
      await createUser({
        username: newUsername.trim(),
        password: newPassword,
        role: newRole,
        deviceIds: newDevices,
        expiresAt: newExpiry ? new Date(newExpiry).toISOString() : undefined,
      });
      await onUsersChanged();
      setShowCreateUser(false);
      resetNewUserForm();
    } catch (error) {
      setCreateUserError(error instanceof Error ? error.message : "Could not create account.");
    } finally {
      setSavingUser(false);
    }
  };

  const handleResetPassword = async (user: UserAccount) => {
    const password = prompt(`New password for ${user.username}`);
    if (password) {
      await updateUser(user.username, { password });
      onUsersChanged();
    }
  };

  const handleRole = async (user: UserAccount) => {
    const role = prompt("Role (admin / operator)", user.role);
    if (role === "admin" || role === "operator") {
      await updateUser(user.username, { role });
      onUsersChanged();
    }
  };

  const handleExpiry = async (user: UserAccount) => {
    const value = prompt("Expiry date (YYYY-MM-DD), empty for none", user.expiresAt ? user.expiresAt.slice(0, 10) : "");
    if (value !== null) {
      await updateUser(user.username, { expiresAt: value ? new Date(value).toISOString() : "" });
      onUsersChanged();
    }
  };

  const handleDevices = async (user: UserAccount) => {
    const value = prompt("Allowed device ids, comma separated", (user.deviceIds ?? []).join(","));
    if (value !== null) {
      const ids = value.split(",").map((s) => s.trim()).filter(Boolean);
      await updateUser(user.username, { deviceIds: ids });
      onUsersChanged();
    }
  };

  const handleDeleteUser = async (user: UserAccount) => {
    if (confirm(`Delete account ${user.username}?`)) {
      await deleteUser(user.username);
      onUsersChanged();
    }
  };

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h3>Device keys</h3>
          <p>Each agent gets its own credential. Tokens are shown once at creation.</p>
        </div>
        <button className="primary-btn inline" onClick={onCreate}>
          <ShieldCheck size={15} />
          New key
        </button>
      </div>
      {keys.length === 0 ? (
        <div className="inline-empty">No device keys yet.</div>
      ) : (
        <div className="key-list">
          {keys.map((key) => (
            <div className="key-row" key={key.deviceId}>
              <div>
                <strong>{key.name}</strong>
                <span>{key.deviceId}</span>
                {key.expiresAt && <small>expires {new Date(key.expiresAt).toLocaleDateString()}</small>}
              </div>
              <button className="icon-btn" title="Rotate key" onClick={() => onRotate(key)}>
                <RefreshCw size={14} />
              </button>
              <button className="icon-btn danger" title="Revoke" onClick={() => onRevoke(key)}>
                <Trash2 size={14} />
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="panel-head">
        <div>
          <h3>Invite codes</h3>
          <p>One-time enrollment codes. Devices redeem them for a long-lived key.</p>
        </div>
        <button className="primary-btn inline" onClick={onCreateInvite}>
          New invite
        </button>
      </div>
      {invites.length === 0 ? (
        <div className="inline-empty">No invite codes yet.</div>
      ) : (
        <div className="key-list">
          {invites.map((inv) => (
            <div className="key-row" key={inv.code}>
              <div>
                <strong>{inv.code}</strong>
                <span>{inv.name} · expires {new Date(inv.expiresAt).toLocaleDateString()}</span>
              </div>
              <button className="icon-btn danger" title="Delete invite" onClick={() => onDeleteInvite(inv)}>
                <Trash2 size={14} />
              </button>
            </div>
          ))}
        </div>
      )}

      {canManageUsers && (
        <>
          <div className="panel-head">
            <div>
              <h3>Accounts</h3>
              <p>Admin accounts have full access. Operators only access granted devices.</p>
            </div>
            <button className="primary-btn inline" type="button" onClick={() => setShowCreateUser(true)}>
              Create account
            </button>
          </div>
          {users.length === 0 ? (
            <div className="inline-empty">No accounts yet.</div>
          ) : (
            <div className="key-list">
              {users.map((user) => (
                <div className="key-row" key={user.username}>
                  <div>
                    <strong>{user.username}</strong>
                    <span>
                      {user.role} · {(user.deviceIds ?? []).length} devices
                      {user.expiresAt ? ` · expires ${new Date(user.expiresAt).toLocaleDateString()}` : ""}
                    </span>
                  </div>
                  <button className="icon-btn" title="Reset password" onClick={() => handleResetPassword(user)}>
                    <Pencil size={14} />
                  </button>
                  <button className="icon-btn" title="Change role" onClick={() => handleRole(user)}>
                    <ShieldCheck size={14} />
                  </button>
                  <button className="icon-btn" title="Set expiry" onClick={() => handleExpiry(user)}>
                    <RefreshCw size={14} />
                  </button>
                  <button className="icon-btn" title="Edit devices" onClick={() => handleDevices(user)}>
                    <MonitorSmartphone size={14} />
                  </button>
                  <button className="icon-btn danger" title="Delete account" onClick={() => handleDeleteUser(user)}>
                    <Trash2 size={14} />
                  </button>
                </div>
              ))}
            </div>
          )}

          {showCreateUser && (
            <div className="modal-backdrop" onClick={closeCreateUser}>
              <section
                className="account-modal"
                role="dialog"
                aria-modal="true"
                aria-labelledby="create-account-title"
                onClick={(event) => event.stopPropagation()}
              >
                <div className="account-modal-head">
                  <div>
                    <h3 id="create-account-title">Create account</h3>
                    <p>Set credentials, role, and device access for this account.</p>
                  </div>
                  <button className="icon-btn" type="button" aria-label="Close dialog" onClick={closeCreateUser} disabled={savingUser}>
                    <X size={16} />
                  </button>
                </div>
                <form className="account-form" onSubmit={submitCreateUser}>
                  <label className="account-field">
                    <span>Username</span>
                    <input
                      autoFocus
                      autoComplete="username"
                      value={newUsername}
                      onChange={(e) => setNewUsername(e.target.value)}
                      required
                    />
                  </label>
                  <label className="account-field">
                    <span>Password</span>
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={newPassword}
                      onChange={(e) => setNewPassword(e.target.value)}
                      required
                    />
                  </label>
                  <div className="account-form-row">
                    <label className="account-field">
                      <span>Role</span>
                      <select value={newRole} onChange={(e) => setNewRole(e.target.value)}>
                        <option value="operator">operator</option>
                        <option value="admin">admin</option>
                      </select>
                    </label>
                    <label className="account-field">
                      <span>Expires on (optional)</span>
                      <input type="date" value={newExpiry} onChange={(e) => setNewExpiry(e.target.value)} />
                    </label>
                  </div>
                  {newRole === "operator" ? (
                    <fieldset className="account-device-fieldset">
                      <legend>Device access</legend>
                      <p>Select the devices this operator can access.</p>
                      {devices.length === 0 ? (
                        <div className="inline-empty">No devices are registered yet.</div>
                      ) : (
                        <div className="account-device-list">
                          {devices.map((device) => (
                            <label className="account-device-option" key={device.id}>
                              <input
                                type="checkbox"
                                checked={newDevices.includes(device.id)}
                                onChange={(event) =>
                                  setNewDevices((current) =>
                                    event.target.checked
                                      ? [...current, device.id]
                                      : current.filter((id) => id !== device.id),
                                  )
                                }
                              />
                              <span>
                                <strong>{device.name}</strong>
                                <small>{device.id}</small>
                              </span>
                            </label>
                          ))}
                        </div>
                      )}
                    </fieldset>
                  ) : (
                    <p className="account-admin-note">Administrators can access all devices.</p>
                  )}
                  {createUserError && <div className="account-form-error" role="alert">{createUserError}</div>}
                  <div className="account-form-actions">
                    <button className="secondary-btn" type="button" onClick={closeCreateUser} disabled={savingUser}>
                      Cancel
                    </button>
                    <button className="primary-btn inline" type="submit" disabled={savingUser}>
                      {savingUser ? "Creating…" : "Create account"}
                    </button>
                  </div>
                </form>
              </section>
            </div>
          )}
        </>
      )}
    </section>
  );
}

function ActivityPage({ audit }: { audit: AuditEntry[] }) {
  return (
    <section className="panel">
      <div className="panel-head">
        <h3>Audit log</h3>
      </div>
      <ActivityList audit={audit} />
    </section>
  );
}

function ActivityList({ audit }: { audit: AuditEntry[] }) {
  if (audit.length === 0) {
    return <div className="inline-empty">No activity yet.</div>;
  }
  return (
    <div className="activity-list">
      {audit.map((entry) => (
        <div className="activity-row" key={entry.id}>
          <span className="activity-time">{new Date(entry.createdAt).toLocaleString()}</span>
          <span className="activity-action">{entry.action}</span>
          <span className="activity-target">
            {entry.target}
            {entry.detail ? ` (${entry.detail})` : ""}
          </span>
          <span className="activity-actor">{entry.actor}</span>
        </div>
      ))}
    </div>
  );
}

function TerminalView({ device, onBack }: { device: Device; onBack: () => void }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState("connecting");

  useEffect(() => {
    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: "SFMono-Regular, Menlo, Consolas, monospace",
      theme: {
        background: "#0b0f14",
        foreground: "#d7e0e8",
        cursor: "#2dd4bf",
        selectionBackground: "#1e5f66",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(hostRef.current!);
    fit.fit();

    const socket = new WebSocket(
      wsUrl("/ws/terminal", {
        token: getToken(),
        device: device.id,
        cols: term.cols,
        rows: term.rows,
      }),
    );

    const sendResize = () => {
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "terminal:resize", cols: term.cols, rows: term.rows }));
      }
    };

    socket.onopen = () => term.focus();
    socket.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      if (msg.type === "terminal:output") {
        setStatus("connected");
        const bytes = Uint8Array.from(atob(msg.data), (c) => c.charCodeAt(0));
        term.write(bytes);
      }
      if (msg.type === "terminal:exit" || msg.type === "terminal:error") {
        setStatus(msg.type === "terminal:error" ? "error" : "disconnected");
        term.writeln("\r\n\x1b[90m[session ended]\x1b[0m");
        socket.close();
      }
    };
    socket.onerror = () => setStatus("error");
    socket.onclose = () => {
      setStatus((current) => (current === "error" ? current : "disconnected"));
      term.writeln("\r\n\x1b[90m[connection closed]\x1b[0m");
    };

    const dataDisposable = term.onData((data) => {
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "terminal:input", data }));
      }
    });

    const observer = new ResizeObserver(() => {
      fit.fit();
      sendResize();
    });
    observer.observe(hostRef.current!);

    return () => {
      observer.disconnect();
      dataDisposable.dispose();
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "terminal:stop" }));
      }
      socket.close();
      term.dispose();
    };
  }, [device.id]);

  return (
    <div className="app-shell terminal-page">
      <header className="topbar">
        <button className="back-btn" onClick={onBack}>
          <ArrowLeft size={16} />
          Back
        </button>
        <div className="terminal-title">
          <TerminalSquare size={16} />
          {device.name} - remote terminal
        </div>
        <div className="connection-meta">
          <span className="transport-indicator" title="Terminal traffic is forwarded through the Warpmesh VPS">
            Server relay
          </span>
          <div className={`screen-status ${status === "connected" ? "ok" : ""}`} role="status" aria-live="polite">
            <span className={`connection-dot ${status === "connected" ? "ok" : ""}`} />
            {status}
          </div>
        </div>
      </header>
      <main className="terminal-host">
        <div ref={hostRef} className="terminal" />
      </main>
    </div>
  );
}

function DesktopView({ device, onBack }: { device: Device; onBack: () => void }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<{ disconnect: () => void } | null>(null);
  const [status, setStatus] = useState("connecting");

  useEffect(() => {
    let disposed = false;
    const connect = async () => {
      try {
        const RFB = (await import("@novnc/novnc")).default;
        const url = wsUrl("/ws/screen", { token: getToken(), device: device.id });
        const rfb = new RFB(hostRef.current!, url, { credentials: { password: "" } });
        rfbRef.current = rfb;
        rfb.scaleViewport = true;
        rfb.resizeSession = false;
        rfb.addEventListener("connect", () => !disposed && setStatus("connected"));
        rfb.addEventListener("disconnect", () => !disposed && setStatus("disconnected"));
        rfb.addEventListener("credentialsrequired", () => {
          const password = prompt("VNC password (leave empty if not set)") || "";
          rfb.sendCredentials({ password });
        });
        rfb.addEventListener("securityfailure", (event: CustomEvent) => {
          const reason = (event.detail as { reason?: string })?.reason || "authentication failed";
          if (!disposed) setStatus(reason);
        });
      } catch (err) {
        if (!disposed) setStatus(String(err));
      }
    };
    connect();
    return () => {
      disposed = true;
      rfbRef.current?.disconnect();
    };
  }, [device.id]);

  return (
    <div className="app-shell desktop-page">
      <header className="topbar">
        <button className="back-btn" onClick={onBack}>
          <ArrowLeft size={16} />
          Back
        </button>
        <div className="terminal-title">
          <Monitor size={16} />
          {device.name} - remote desktop
        </div>
        <div className="connection-meta">
          <span className="transport-indicator" title="Desktop traffic is forwarded through the Warpmesh VPS">
            Server relay
          </span>
          <div className={`screen-status ${status === "connected" ? "ok" : ""}`} role="status" aria-live="polite">
            <span className={`connection-dot ${status === "connected" ? "ok" : ""}`} />
            {status}
          </div>
        </div>
      </header>
      <main className="desktop-host">
        <div ref={hostRef} className="desktop-canvas" />
      </main>
    </div>
  );
}

interface RemoteEntry {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  modTime: string;
}

function FileManagerView({ device, onBack }: { device: Device; onBack: () => void }) {
  const [path, setPath] = useState("");
  const [entries, setEntries] = useState<RemoteEntry[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [dragOver, setDragOver] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const fileOp = (type: string, payload: Record<string, unknown> = {}) =>
    new Promise<{ type: string; entries?: RemoteEntry[]; error?: string }>((resolve, reject) => {
      const ws = new WebSocket(wsUrl("/ws/files", { token: getToken(), device: device.id }));
      ws.onopen = () => ws.send(JSON.stringify({ type, ...payload }));
      ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        if (msg.type === "file:list:result" || msg.type === "file:roots:result" || msg.type === "file:done") {
          ws.close();
          resolve(msg);
        }
        if (msg.type === "file:error") {
          ws.close();
          reject(new Error(msg.error || "operation failed"));
        }
      };
      ws.onerror = () => reject(new Error("websocket error"));
    });

  const loadDir = async (p: string) => {
    setBusy(true);
    setMessage("");
    try {
      const msg = await fileOp("file:list", { path: p });
      setEntries(msg.entries || []);
      setPath(p);
      setSelected(new Set());
    } catch (err) {
      setMessage(String(err));
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    (async () => {
      try {
        const msg = await fileOp("file:roots");
        const roots = msg.entries || [];
        if (roots.length > 0) {
          await loadDir(roots[0].path);
        } else {
          setMessage("No allowed file roots configured on the agent.");
        }
      } catch (err) {
        setMessage(String(err));
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [device.id]);

  const separator = path.includes("\\") ? "\\" : "/";
  const joinPath = (dir: string, name: string) => (dir.endsWith(separator) ? dir + name : dir + separator + name);

  const uploadOne = (file: File) =>
    new Promise<void>((resolve, reject) => {
      const ws = new WebSocket(
        wsUrl("/ws/file", {
          token: getToken(),
          device: device.id,
          op: "upload",
          name: file.name,
          path,
          size: file.size,
        }),
      );
      ws.onopen = async () => {
        const raw = new Uint8Array(await file.arrayBuffer());
        const CHUNK = 64 * 1024;
        for (let i = 0; i < raw.length; i += CHUNK) {
          ws.send(raw.slice(i, i + CHUNK));
        }
        ws.send(JSON.stringify({ type: "file:upload:done" }));
      };
      ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        if (msg.type === "file:done") {
          resolve();
        }
        if (msg.type === "file:error") {
          reject(new Error(msg.error || "upload failed"));
        }
      };
      ws.onerror = () => reject(new Error("upload websocket error"));
    });

  const handleUpload = async (files: FileList | File[]) => {
    setBusy(true);
    setMessage("");
    try {
      for (const file of Array.from(files)) {
        await uploadOne(file);
      }
      await loadDir(path);
    } catch (err) {
      setMessage(String(err));
    } finally {
      setBusy(false);
      if (fileRef.current) {
        fileRef.current.value = "";
      }
    }
  };

  const downloadPath = (p: string, name: string) =>
    new Promise<void>((resolve, reject) => {
      const ws = new WebSocket(
        wsUrl("/ws/file", {
          token: getToken(),
          device: device.id,
          op: "download",
          path: p,
        }),
      );
      ws.binaryType = "arraybuffer";
      const chunks: BlobPart[] = [];
      ws.onmessage = (event) => {
        if (typeof event.data === "string") {
          const msg = JSON.parse(event.data);
          if (msg.type === "file:done") {
            const blob = new Blob(chunks);
            const url = URL.createObjectURL(blob);
            const a = document.createElement("a");
            a.href = url;
            a.download = name;
            a.click();
            URL.revokeObjectURL(url);
            ws.close();
            resolve();
          }
          if (msg.type === "file:error") {
            ws.close();
            reject(new Error(msg.error || "download failed"));
          }
        } else {
          chunks.push(event.data);
        }
      };
      ws.onerror = () => reject(new Error("download websocket error"));
    });

  const handleDownloadSelected = async () => {
    setBusy(true);
    setMessage("");
    try {
      for (const p of selected) {
        const entry = entries.find((e) => e.path === p);
        if (entry && !entry.isDir) {
          await downloadPath(p, entry.name);
        }
      }
    } catch (err) {
      setMessage(String(err));
    } finally {
      setBusy(false);
    }
  };

  const handleNewFolder = async () => {
    const name = prompt("New folder name");
    if (!name?.trim()) return;
    try {
      await fileOp("file:mkdir", { path: joinPath(path, name.trim()) });
      await loadDir(path);
    } catch (err) {
      setMessage(String(err));
    }
  };

  const handleRename = async (entry: RemoteEntry) => {
    const name = prompt("New name", entry.name);
    if (!name?.trim() || name.trim() === entry.name) return;
    try {
      await fileOp("file:rename", {
        path: entry.path,
        target: joinPath(path, name.trim()),
      });
      await loadDir(path);
    } catch (err) {
      setMessage(String(err));
    }
  };

  const handleDeleteSelected = async () => {
    if (selected.size === 0 || !confirm(`Delete ${selected.size} item(s)?`)) return;
    setBusy(true);
    setMessage("");
    try {
      for (const p of selected) {
        await fileOp("file:delete", { path: p });
      }
      await loadDir(path);
    } catch (err) {
      setMessage(String(err));
    } finally {
      setBusy(false);
    }
  };

  const toggleSelect = (p: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(p)) {
        next.delete(p);
      } else {
        next.add(p);
      }
      return next;
    });
  };

  const segments = path.split(separator).filter(Boolean);

  return (
    <div className="app-shell">
      <header className="topbar">
        <button className="back-btn" onClick={onBack}>
          <ArrowLeft size={16} />
          Back
        </button>
        <div className="terminal-title">
          <Folder size={16} />
          {device.name} - file manager
        </div>
        <span className="screen-status ok">{busy ? "working..." : ""}</span>
      </header>

      <main className="content">
        <div className="file-toolbar">
          <div className="breadcrumbs">
            {segments.map((seg, i) => {
              const p = segments.slice(0, i + 1).join(separator);
              return (
                <button key={p} onClick={() => loadDir(separator + p)}>
                  {seg}
                </button>
              );
            })}
          </div>
          <div className="toolbar-actions">
            <button className="icon-btn" title="Refresh" onClick={() => loadDir(path)}>
              <RefreshCw size={15} />
            </button>
            <button className="icon-btn" title="New folder" onClick={handleNewFolder}>
              <FolderPlus size={15} />
            </button>
            <button className="icon-btn" title="Upload files" onClick={() => fileRef.current?.click()}>
              <Upload size={15} />
            </button>
            <button className="icon-btn" title="Download selected" disabled={selected.size === 0} onClick={handleDownloadSelected}>
              <Download size={15} />
            </button>
            <button className="icon-btn danger" title="Delete selected" disabled={selected.size === 0} onClick={handleDeleteSelected}>
              <Trash2 size={15} />
            </button>
            <input ref={fileRef} type="file" multiple hidden onChange={(e) => e.target.files && handleUpload(e.target.files)} />
          </div>
        </div>

        <div
          className={`dropzone ${dragOver ? "over" : ""}`}
          onDragOver={(e) => {
            e.preventDefault();
            setDragOver(true);
          }}
          onDragLeave={() => setDragOver(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragOver(false);
            if (e.dataTransfer.files.length > 0) {
              handleUpload(e.dataTransfer.files);
            }
          }}
        >
          {message && <div className="file-message">{message}</div>}
          <div className="file-table">
            <div className="file-row file-row-head">
              <span></span>
              <span>Name</span>
              <span>Size</span>
              <span>Modified</span>
              <span>Actions</span>
            </div>
            {entries.map((entry) => (
              <div className="file-row" key={entry.path}>
                <span>
                  <input
                    type="checkbox"
                    checked={selected.has(entry.path)}
                    onChange={() => toggleSelect(entry.path)}
                  />
                </span>
                <button
                  className="file-name"
                  onClick={() => (entry.isDir ? loadDir(entry.path) : downloadPath(entry.path, entry.name))}
                >
                  {entry.isDir ? <Folder size={16} /> : <FileText size={16} />}
                  {entry.name}
                </button>
                <span>{entry.isDir ? "-" : formatBytes(entry.size)}</span>
                <span>{entry.modTime ? new Date(entry.modTime).toLocaleString() : "-"}</span>
                <span className="row-actions">
                  <button className="icon-btn" title="Rename" onClick={() => handleRename(entry)}>
                    <Pencil size={14} />
                  </button>
                </span>
              </div>
            ))}
            {entries.length === 0 && <div className="inline-empty">This folder is empty. Drag files here to upload.</div>}
          </div>
        </div>
      </main>
    </div>
  );
}

function formatBytes(n: number) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}
