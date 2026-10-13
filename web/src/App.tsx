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
  clearSessionFlag,
  clearToken,
  createDeviceKey,
  createUser,
  deleteInvite,
  deleteUser,
  getAnalytics,
  hasSession,
  listAudit,
  listDeviceKeys,
  listDevices,
  listInvites,
  listUsers,
  login,
  logout,
  revokeDeviceKey,
  rotateDeviceKey,
  type AuditEntry,
  type AnalyticsData,
  type ConnectionRecord,
  type Device,
  type DeviceKey,
  type Invite,
  type UserAccount,
  optionalDate,
  wsUrl,
} from "./api";
import {
  DIRECT_BUDGET_MS,
  formatElapsed,
  stageDetail,
  stageIsPending,
  stageLabel,
  useSessionStage,
  type SessionKind,
  type StageAttempt,
} from "./sessionStage";

import { downloadFile, requestFiles, uploadFile } from "./fileTransport";
import { canFallback, createDeadline, HANDSHAKE_BUDGET_MS, negotiateOffer, refreshFailure, terminalOutputIsLive } from "./sessionLifecycle";

import { AccountEditor, ActionDialog, DeviceManager, EnrollmentDialog, type ActionRequest } from "./ManagementUI";

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

function deviceView(kind: DeviceRouteKind, device: Device): View {
  switch (kind) {
    case "terminal":
      return { name: "terminal", device };
    case "desktop":
      return { name: "desktop", device };
    case "files":
      return { name: "files", device };
    default:
      return { name: "page", page: "devices" };
  }
}

/**
 * Parse a device deep link. The two URL shapes intentionally differ:
 *   path  /devices/<id>/<kind>   (written by openDeviceView)
 *   hash  #/<kind>/<id>          (written by the agent CLI)
 * Reading one shape with the other's group order silently resolved the wrong
 * device and dropped the operator back on the dashboard.
 */
function parseDeviceRoute(pathname: string, hash: string): { kind: DeviceRouteKind; id: string } | null {
  const kinds = "(terminal|desktop|files)";
  const pathMatch = pathname.match(new RegExp(`^/devices/([^/]+)/${kinds}$`));
  if (pathMatch) {
    return { id: decodeURIComponent(pathMatch[1]), kind: pathMatch[2] as DeviceRouteKind };
  }
  const hashMatch = hash.match(new RegExp(`^#/${kinds}/(.+)$`));
  if (hashMatch) {
    return { kind: hashMatch[1] as DeviceRouteKind, id: decodeURIComponent(hashMatch[2]) };
  }
  return null;
}

function normalizedPath(path: string): string {
  return path.split("?")[0].replace(/\/+$/, "") || "/";
}

function pageFromPath(path: string): Page {
  return PAGES_BY_PATH[normalizedPath(path)] ?? "dashboard";
}

const RTC_STUN_SERVERS = [
  "stun:stun.l.google.com:19302",
  "stun:stun.miwifi.com:3478",
  "stun:stun.chat.bilibili.com:3478",
];

type BrowserRTC = { peer: RTCPeerConnection; channel: RTCDataChannel };

function createBrowserRTC(
  socket: WebSocket,
  service: "terminal" | "desktop",
  handlers: {
    onFailure?: () => void;
    onLocalCandidate?: () => void;
    onStateChange?: (state: RTCPeerConnectionState) => void;
  } = {},
): BrowserRTC {
  const peer = new RTCPeerConnection({
    iceServers: [{ urls: RTC_STUN_SERVERS }],
    iceCandidatePoolSize: 2,
  });
  const channel = peer.createDataChannel("warpmesh");
  channel.binaryType = "arraybuffer";
  // Surface the peer state so the UI can explain a stalled attempt instead of
  // showing one static label for the whole direct budget.
  peer.onconnectionstatechange = () => handlers.onStateChange?.(peer.connectionState);
  // Trickle ICE: forward each candidate as it is gathered so connectivity
  // checks can start immediately instead of waiting for full gathering.
  peer.onicecandidate = (event) => {
    if (event.candidate) handlers.onLocalCandidate?.();
    if (event.candidate && socket.readyState === WebSocket.OPEN) {
      socket.send(
        JSON.stringify({
          type: "rtc:ice",
          service,
          data: btoa(JSON.stringify(event.candidate.toJSON())),
        }),
      );
    }
  };
  void negotiateOffer(peer, (description) => {
    if (socket.readyState !== WebSocket.OPEN) throw new Error("Control channel closed");
    socket.send(JSON.stringify({ type: "rtc:offer", service, data: btoa(JSON.stringify(description)) }));
  }, () => handlers.onFailure?.());
  return { peer, channel };
}

function decodeRTCDescription(data: string): RTCSessionDescriptionInit {
  return JSON.parse(atob(data)) as RTCSessionDescriptionInit;
}

function decodeRTCCandidate(data: string): RTCIceCandidateInit {
  return JSON.parse(atob(data)) as RTCIceCandidateInit;
}

type View =
  | { name: "page"; page: Page }
  | { name: "terminal"; device: Device }
  | { name: "desktop"; device: Device }
  | { name: "files"; device: Device };

export default function App() {
  // The credential lives in an HttpOnly cookie; this is only a marker that a
  // session exists, so the console knows whether to render the login form.
  const [token, setTokenState] = useState(hasSession());
  const [view, setView] = useState<View>(() => ({ name: "page", page: pageFromPath(location.pathname) }));
  const routeSeq = useRef(0);

  const applyLocation = useCallback(async () => {
    const seq = ++routeSeq.current;
    const route = parseDeviceRoute(location.pathname, location.hash);
    if (!route) {
      if (routeSeq.current === seq) {
        setView({ name: "page", page: pageFromPath(location.pathname) });
      }
      return;
    }
    try {
      const devices = await listDevices();
      const device = devices.find((d) => d.id === route.id);
      if (routeSeq.current !== seq) {
        return;
      }
      if (device) {
        setView(deviceView(route.kind, device));
      } else {
        // Stale link (the device is gone): show the inventory rather than
        // leaving the URL claiming a view that cannot render.
        setView({ name: "page", page: "devices" });
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

  const handleLogout = useCallback(async () => {
    try { await logout(); } catch { /* local logout works offline */ }
    history.replaceState({}, "", "/");
    clearToken(); clearSessionFlag(); setTokenState(false);
  }, []);

  if (!token) {
    return <Login onLogin={() => setTokenState(true)} />;
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
      onLogout={handleLogout}
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
  const [refreshError, setRefreshError] = useState("");
  const [analyticsError, setAnalyticsError] = useState("");
  const [action, setAction] = useState<ActionRequest | null>(null);
  const [enrolling, setEnrolling] = useState(false);
  const [notice, setNotice] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const deviceList = await listDevices();
      setDevices(deviceList ?? []);
      setRefreshError("");
    } catch (error) {
      const failure = refreshFailure(error);
      if (failure.logout) onLogout();
      else setRefreshError(failure.message);
    } finally {
      setLoading(false);
    }
  }, [onLogout]);

  const loadAnalytics = useCallback(async () => {
    setAnalyticsLoading(true);
    try {
      setAnalytics(await getAnalytics(analyticsRange));
      setAnalyticsError("");
    } catch (error) {
      const failure = refreshFailure(error);
      if (failure.logout) onLogout();
      else setAnalyticsError(failure.message.replace("devices", "analytics"));
      // Retain the last successful snapshot during transient failures.
    } finally {
      setAnalyticsLoading(false);
    }
  }, [analyticsRange, onLogout]);

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

  const handleCreateKey = () => setAction({ title: "Create device key", fields: [{ name: "name", label: "Device name", required: true }], run: async (values) => {
    const key = await createDeviceKey(values.name.trim()); await loadKeys();
    return `Device ID: ${key.deviceId}\nToken: ${key.token}\n\nUse the Add device wizard for guided enrollment.`;
  } });
  const handleRevokeKey = (key: DeviceKey) => setAction({ title: `Revoke key: ${key.name}`, destructive: true, description: "This device will be unable to authenticate on its next connection.", run: async () => { await revokeDeviceKey(key.deviceId); await loadKeys(); } });
  const handleRotateKey = (key: DeviceKey) => setAction({ title: `Rotate key: ${key.name}`, description: "The old token will stop working. Update the agent with the new token.", run: async () => { const rotated = await rotateDeviceKey(key.deviceId); await loadKeys(); return `New device token:\n${rotated.token}`; } });
  const handleCreateInvite = () => setEnrolling(true);
  const handleDeleteInvite = (inv: Invite) => setAction({ title: "Delete invite", destructive: true, description: `Revoke the enrollment invite for ${inv.name}?`, run: async () => { await deleteInvite(inv.code); await loadInvites(); } });

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
          {notice && <div className="management-notice" role="status">{notice}<button className="icon-btn" aria-label="Dismiss notification" onClick={() => setNotice("")}><X size={14} /></button></div>}
          {refreshError && <div className="file-message" role="alert">{refreshError} <button disabled={loading} onClick={() => void refresh()}>Retry</button></div>}
          {analyticsError && <div className="file-message" role="alert">{analyticsError} <button disabled={analyticsLoading} onClick={() => void loadAnalytics()}>Retry</button></div>}
          {page === "dashboard" && (
            <DashboardPage
              analytics={analytics}
              loading={analyticsLoading}
              range={analyticsRange}
              onRangeChange={setAnalyticsRange}
            />
          )}
          {page === "devices" && (
            <DeviceManager devices={devices} canManage={canManageUsers} onChanged={refresh} onAction={setAction} onAdd={() => setEnrolling(true)} onTerminal={onTerminal} onDesktop={onDesktop} onFiles={onFiles} />
          )}
          {page === "access" && (
            <AccessPage
              keys={keys}
              invites={invites}
              users={users}
              devices={devices}
              canManageUsers={canManageUsers}
              onUsersChanged={loadUsers}
              onAction={setAction}
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
      {action && <ActionDialog request={action} onClose={() => setAction(null)} onSuccess={setNotice} />}
      {enrolling && <EnrollmentDialog devices={devices} onClose={() => setEnrolling(false)} onInvitesChanged={loadInvites} />}
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
  const knownRoutes = (analytics?.directSessions ?? 0) + (analytics?.relaySessions ?? 0);
  const directPercent = knownRoutes ? Math.round(((analytics?.directSessions ?? 0) / knownRoutes) * 100) : 0;
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
          <span>Direct sessions</span>
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
        <p className="chart-note">This chart counts bytes that passed through the VPS. Direct terminal, desktop and device-forwarding traffic bypasses it; file operations use the relay.</p>
      </section>

      <div className="dashboard-lower-grid">
        <section className="panel route-panel">
          <div className="panel-head">
            <div>
              <h3>Connection path</h3>
              <p>Actual paths used by terminals, desktops and device forwarding</p>
            </div>
          </div>
          {knownRoutes === 0 ? (
            <div className="inline-empty">No direct or relayed sessions in this period.</div>
          ) : (
            <>
              <div className="route-split-bar" aria-label={`${directPercent}% direct, ${100 - directPercent}% relay`}>
                <span className="route-direct" style={{ width: `${directPercent}%` }} />
                <span className="route-relay" style={{ width: `${100 - directPercent}%` }} />
              </div>
              <div className="route-counts">
                <span><i className="route-direct-dot" />Direct <strong>{analytics?.directSessions ?? 0}</strong></span>
                <span><i className="route-relay-dot" />Server relay <strong>{analytics?.relaySessions ?? 0}</strong></span>
              </div>
            </>
          )}
          <p className="chart-note">Path shows the actual transport for device forwarding, terminal and desktop sessions. File operations use the server relay.</p>
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

function AccessPage({
  keys,
  invites,
  users,
  devices,
  canManageUsers,
  onUsersChanged,
  onAction,
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
  onAction: (request: ActionRequest) => void;
  onCreate: () => void;
  onRevoke: (key: DeviceKey) => void;
  onRotate: (key: DeviceKey) => void;
  onCreateInvite: () => void;
  onDeleteInvite: (inv: Invite) => void;
}) {
  const [accountNotice, setAccountNotice] = useState("");
  const [editingUser, setEditingUser] = useState<UserAccount | null>(null);
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

  const handleDeleteUser = (user: UserAccount) => onAction({ title: `Delete account: ${user.username}`, destructive: true, description: "This account will lose access to the console.", run: async () => { await deleteUser(user.username); await onUsersChanged(); } });

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
                {optionalDate(key.expiresAt) ? <small>expires {new Date(key.expiresAt!).toLocaleDateString()}</small> : <small>No expiry</small>}
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

      {accountNotice && <div className="management-notice" role="status">{accountNotice}<button className="icon-btn" aria-label="Dismiss account notification" onClick={() => setAccountNotice("")}><X size={14} /></button></div>}
      {editingUser && <AccountEditor user={editingUser} devices={devices} onClose={() => setEditingUser(null)} onSaved={async () => { await onUsersChanged(); setAccountNotice("Account saved."); }} />}
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
                      {user.role} · {user.role === "admin" ? "all devices" : `${(user.deviceIds ?? []).length} devices`}
                      {optionalDate(user.expiresAt) ? ` · expires ${new Date(user.expiresAt!).toLocaleDateString()}` : ""}
                    </span>
                  </div>
                  <button className="secondary-btn" onClick={() => setEditingUser(user)}>Edit account</button>
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

/**
 * Connection progress for a terminal or desktop session.
 *
 * A direct WebRTC attempt can spend up to DIRECT_BUDGET_MS gathering
 * candidates and running connectivity checks. Showing one static label for that
 * whole window reads as a hung page, so this reports the stage, a live elapsed
 * clock, the ICE candidate counts and a countdown to the relay fallback.
 */
function SessionProgress({ attempt, kind }: { attempt: StageAttempt; kind: SessionKind }) {
  const pending = stageIsPending(attempt.stage);
  const ok = attempt.stage === "connected";
  const bad = attempt.stage === "error";
  return (
    <div className="connection-meta">
      <div className="connection-progress" role="status" aria-live="polite">
        <div className={`screen-status ${ok ? "ok" : bad ? "bad" : ""}`}>
          <span className={`connection-dot ${ok ? "ok" : bad ? "bad" : ""} ${pending ? "pulse" : ""}`} />
          <span className="connection-stage">{stageLabel(attempt.stage)}</span>
          {/* The path matters once the session is live: direct is peer-to-peer,
              relay goes through the VPS. */}
          {attempt.stage === "connected" && (
            <span className={`transport-indicator ${attempt.transport}`}>
              {transportLabel(attempt.transport)}
            </span>
          )}
          {pending && <span className="connection-elapsed">{formatElapsed(attempt.elapsedMs)}</span>}
        </div>
        <span className="connection-detail">{stageDetail(attempt, kind)}</span>
      </div>
    </div>
  );
}

/** Full-panel overlay so a blank desktop area never looks like a frozen page. */
function SessionOverlay({ attempt, kind }: { attempt: StageAttempt; kind: SessionKind }) {
  return (
    <div className="session-overlay" role="status" aria-live="polite">
      <span className="session-spinner" aria-hidden="true" />
      <strong>{stageLabel(attempt.stage)}</strong>
      <span>{stageDetail(attempt, kind)}</span>
      <span className="session-overlay-elapsed">elapsed {formatElapsed(attempt.elapsedMs)}</span>
    </div>
  );
}

function TerminalView({ device, onBack }: { device: Device; onBack: () => void }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const session = useSessionStage();
  const { setStage, startBudget, stopBudget: termStopBudget, markRelaying, markConnected,
          markFailed, markDisconnected, noteLocalCandidate, noteRemoteCandidate } = session;

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

    // No credential in the URL: the browser attaches the HttpOnly session
    // cookie to the WebSocket handshake automatically.
    const socket = new WebSocket(
      wsUrl("/ws/terminal", {
        device: device.id,
        cols: term.cols,
        rows: term.rows,
      }),
    );
    let rtc: BrowserRTC | null = null;
    let directActive = false;
    let relayActive = false;
    let receivedOutput = false;
    let directAcknowledged = false;
    let fallbackRequested = false;
    const directDeadline = createDeadline();
    const relayDeadline = createDeadline();
    let disposed = false;
    // A live session must not be torn down by a stale fallback.
    let live = false;

    const startFallback = (failed = false) => {
      // Ignore stale deadlines, but recover real failures even after connect.
      if (!canFallback(disposed, fallbackRequested, live, failed)) return;
      live = false;
      fallbackRequested = true;
      directActive = false;
      directDeadline.cancel();
      termStopBudget();
      rtc?.peer.close();
      setStage("switching");
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "rtc:fallback" }));
        relayDeadline.arm(HANDSHAKE_BUDGET_MS, () => {
          markFailed("Terminal relay timed out. Return and retry the session.");
          socket.close();
        });
      } else markFailed("The control channel closed before relay recovery.");
    };

    const markLive = () => {
      live = true;
      directDeadline.cancel();
      relayDeadline.cancel();
      termStopBudget();
      markConnected();
    };

    const activateDirect = () => {
      if (!directAcknowledged || rtc?.channel.readyState !== "open" || fallbackRequested) return;
      directActive = true;
      markLive();
    };

    const sendResize = () => {
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "terminal:resize", cols: term.cols, rows: term.rows }));
      }
    };

    socket.onopen = () => {
      if (disposed) return;
      term.focus();
      startBudget(DIRECT_BUDGET_MS);
      directDeadline.arm(DIRECT_BUDGET_MS, startFallback);
      try {
        rtc = createBrowserRTC(socket, "terminal", {
          onFailure: () => startFallback(true),
          onLocalCandidate: noteLocalCandidate,
          onStateChange: (state) => {
            if (state === "failed" || state === "disconnected") startFallback(true);
            if (state === "new" || state === "connecting") setStage("checking");
          },
        });
      } catch {
        startFallback();
        return;
      }
      rtc.channel.onopen = activateDirect;
      rtc.channel.onmessage = (event) => {
        if (!disposed && !fallbackRequested && event.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(event.data));
          markLive();
        }
      };
      rtc.channel.onclose = () => {
        startFallback(true);
      };
      rtc.channel.onerror = () => {
        startFallback(true);
      };
    };
    socket.onmessage = async (event) => {
      if (disposed) return;
      let msg: { type: string; data?: string; error?: string };
      try {
        msg = JSON.parse(event.data as string) as typeof msg;
      } catch {
        return;
      }
      if (msg.type === "rtc:answer" && msg.data && rtc && !fallbackRequested) {
        setStage("negotiating");
        try {
          await rtc.peer.setRemoteDescription(decodeRTCDescription(msg.data));
        } catch {
          startFallback();
        }
        return;
      }
      if (msg.type === "rtc:ice" && msg.data && rtc && !fallbackRequested) {
        noteRemoteCandidate();
        try {
          await rtc.peer.addIceCandidate(decodeRTCCandidate(msg.data));
        } catch {
          // Candidate races are expected during trickle ICE.
        }
        return;
      }
      if (msg.type === "rtc:direct-ok") {
        directAcknowledged = true;
        activateDirect();
        return;
      }
      if (msg.type === "rtc:direct-error") {
        startFallback(true);
        return;
      }
      if (msg.type === "rtc:relay") {
        directActive = false;
        relayActive = true;
        fallbackRequested = true;
        directDeadline.cancel();
        rtc?.peer.close();
        markRelaying();
        sendResize();
        if (receivedOutput) markLive();
        else relayDeadline.arm(HANDSHAKE_BUDGET_MS, () => {
          markFailed("Terminal output timed out. Return and retry the session.");
          socket.close();
        });
        return;
      }
      if (msg.type === "terminal:output" && typeof msg.data === "string") {
        // The agent can emit welcome output during negotiation. It does not
        // prove a direct transport and must not cancel its attempt budget.
        receivedOutput = true;
        if (terminalOutputIsLive(directActive, relayActive)) markLive();
        const bytes = Uint8Array.from(atob(msg.data), (c) => c.charCodeAt(0));
        term.write(bytes);
      }
      if (msg.type === "terminal:exit" || msg.type === "terminal:error") {
        markDisconnected(msg.type === "terminal:error" ? "The device reported a terminal error." : "Session ended.");
        term.writeln("\r\n\x1b[90m[session ended]\x1b[0m");
        socket.close();
      }
    };
    socket.onerror = () => { if (!disposed) markFailed("The control channel failed."); };
    socket.onclose = () => {
      if (disposed) return;
      directDeadline.cancel();
      relayDeadline.cancel();
      fallbackRequested = true;
      rtc?.peer.close();
      markDisconnected("The control channel closed.");
      term.writeln("\r\n\x1b[90m[connection closed]\x1b[0m");
    };

    const dataDisposable = term.onData((data) => {
      if (directActive && rtc?.channel.readyState === "open") {
        rtc.channel.send(new TextEncoder().encode(data));
        return;
      }
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
      disposed = true;
      directDeadline.dispose();
      relayDeadline.dispose();
      observer.disconnect();
      dataDisposable.dispose();
      rtc?.peer.close();
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "terminal:stop" }));
      }
      socket.close();
      term.dispose();
    };
  }, [device.id, setStage, startBudget, termStopBudget, markRelaying, markConnected, markFailed,
      markDisconnected, noteLocalCandidate, noteRemoteCandidate]);

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
        <SessionProgress attempt={session.attempt} kind="terminal" />
      </header>
      <main className="terminal-host">
        <div ref={hostRef} className="terminal" />
        {stageIsPending(session.stage) && <SessionOverlay attempt={session.attempt} kind="terminal" />}
      </main>
    </div>
  );
}

function DesktopView({ device, onBack }: { device: Device; onBack: () => void }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<{ disconnect: () => void } | null>(null);
  const session = useSessionStage();
  const { setStage, startBudget, stopBudget: rfbStopBudget, markRelaying, markConnected,
          markFailed, markDisconnected, noteLocalCandidate, noteRemoteCandidate } = session;

  useEffect(() => {
    let disposed = false;
    let socket: WebSocket | null = null;
    let rtc: BrowserRTC | null = null;
    let directAcknowledged = false;
    let fallbackRequested = false;
    const directDeadline = createDeadline();
    const handshakeDeadline = createDeadline();
    // A live session must not be torn down by a stale fallback.
    let live = false;
    const connect = async () => {
      try {
        const RFB = (await import("@novnc/novnc")).default;
        if (disposed) return;
        socket = new WebSocket(wsUrl("/ws/screen", { device: device.id }));

        const attachRFB = (channel: RTCDataChannel | WebSocket, mode: "direct" | "relay") => {
          if (!hostRef.current || disposed) return;
          handshakeDeadline.arm(HANDSHAKE_BUDGET_MS, () => {
            if (mode === "direct") startFallback(true);
            else {
              markFailed("Desktop handshake timed out. Return and retry the session.");
              rfbRef.current?.disconnect();
              rfbRef.current = null;
              socket?.close();
            }
          });
          const rfb = new RFB(hostRef.current, channel, { credentials: { password: "" } });
          rfbRef.current = rfb;
          rfb.scaleViewport = true;
          rfb.resizeSession = false;
          rfb.addEventListener("connect", () => {
            if (disposed || rfbRef.current !== rfb || (mode === "direct" && fallbackRequested)) return;
            // The session is live: stop the direct-attempt budget and the
            // fallback timer so they cannot tear it down mid-stream.
            live = true;
            directDeadline.cancel();
            handshakeDeadline.cancel();
            rfbStopBudget();
            markConnected();
          });
          rfb.addEventListener("disconnect", () => {
            if (disposed || rfbRef.current !== rfb || (mode === "direct" && fallbackRequested)) return;
            handshakeDeadline.cancel();
            if (mode === "direct") startFallback(true);
            else markDisconnected("The device closed the desktop session.");
          });
          rfb.addEventListener("credentialsrequired", () => {
            const password = prompt("VNC password (leave empty if not set)") || "";
            rfb.sendCredentials({ password });
          });
          rfb.addEventListener("securityfailure", (event: CustomEvent) => {
            const reason = (event.detail as { reason?: string })?.reason || "authentication failed";
            if (!disposed) {
              handshakeDeadline.cancel();
              directDeadline.cancel();
              fallbackRequested = true;
              markFailed(reason);
              rtc?.peer.close();
              socket?.close();
            }
          });
          if (mode === "direct" && socket?.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ type: "rtc:ready" }));
          }
        };

        const startFallback = (failed = false) => {
          // Ignore stale deadlines, but recover real failures even after connect.
          if (!canFallback(disposed, fallbackRequested, live, failed)) return;
          live = false;
          fallbackRequested = true;
          directDeadline.cancel();
          handshakeDeadline.cancel();
          rfbStopBudget();
          if (rfbRef.current) {
            rfbRef.current.disconnect();
            rfbRef.current = null;
          }
          rtc?.peer.close();
          setStage("switching");
          if (socket?.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ type: "rtc:fallback" }));
            handshakeDeadline.arm(HANDSHAKE_BUDGET_MS, () => {
              markFailed("Desktop relay timed out. Return and retry the session.");
              socket?.close();
            });
          } else markFailed("The control channel closed before relay recovery.");
        };

        const maybeAttachDirect = () => {
          if (!directAcknowledged || rtc?.channel.readyState !== "open" || fallbackRequested || rfbRef.current) return;
          attachRFB(rtc.channel, "direct");
        };

        socket.onopen = () => {
          if (disposed) return;
          startBudget(DIRECT_BUDGET_MS);
          directDeadline.arm(DIRECT_BUDGET_MS, startFallback);
          try {
            rtc = createBrowserRTC(socket!, "desktop", {
              onFailure: () => startFallback(true),
              onLocalCandidate: noteLocalCandidate,
              onStateChange: (state) => {
                if (state === "failed" || state === "disconnected") startFallback(true);
                if (state === "new" || state === "connecting") setStage("checking");
              },
            });
          } catch {
            startFallback();
            return;
          }
          rtc.channel.onopen = maybeAttachDirect;
          rtc.channel.onclose = () => {
            startFallback(true);
          };
          rtc.channel.onerror = () => {
            startFallback(true);
          };
        };
        socket.onmessage = async (event) => {
          if (disposed) return;
          let msg: { type: string; data?: string; error?: string };
          try {
            msg = JSON.parse(event.data as string) as typeof msg;
          } catch {
            return;
          }
          if (msg.type === "rtc:answer" && msg.data && rtc && !fallbackRequested) {
            setStage("negotiating");
            try {
              await rtc.peer.setRemoteDescription(decodeRTCDescription(msg.data));
            } catch {
              startFallback();
            }
            return;
          }
          if (msg.type === "rtc:ice" && msg.data && rtc && !fallbackRequested) {
            noteRemoteCandidate();
            try {
              await rtc.peer.addIceCandidate(decodeRTCCandidate(msg.data));
            } catch {
              // Candidate races are expected during trickle ICE.
            }
            return;
          }
          if (msg.type === "rtc:direct-ok") {
            directAcknowledged = true;
            maybeAttachDirect();
            return;
          }
          if (msg.type === "rtc:direct-error") {
            startFallback(true);
            return;
          }
          if (msg.type === "rtc:relay") {
            fallbackRequested = true;
            directDeadline.cancel();
            handshakeDeadline.cancel();
            rfbRef.current?.disconnect();
            rfbRef.current = null;
            rtc?.peer.close();
            markRelaying();
            attachRFB(socket!, "relay");
            return;
          }
          if (msg.type === "screen:error") {
            markFailed(msg.error || "screen connection failed");
          }
        };
        socket.onerror = () => {
          if (!disposed) markFailed("The control channel failed.");
        };
        socket.onclose = () => {
          if (!disposed) {
            directDeadline.cancel();
            handshakeDeadline.cancel();
            fallbackRequested = true;
            rfbRef.current?.disconnect();
            rfbRef.current = null;
            rtc?.peer.close();
            markDisconnected("The control channel closed.");
          }
        };
      } catch (err) {
        if (!disposed) markFailed(err instanceof Error ? err.message : String(err));
      }
    };
    connect();
    return () => {
      disposed = true;
      directDeadline.dispose();
      handshakeDeadline.dispose();
      rfbRef.current?.disconnect();
      rfbRef.current = null;
      rtc?.peer.close();
      socket?.close();
    };
  }, [device.id, setStage, startBudget, rfbStopBudget, markRelaying, markConnected, markFailed,
      markDisconnected, noteLocalCandidate, noteRemoteCandidate]);

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
        <SessionProgress attempt={session.attempt} kind="desktop" />
      </header>
      <main className="desktop-host">
        <div ref={hostRef} className="desktop-canvas" />
        {stageIsPending(session.stage) && <SessionOverlay attempt={session.attempt} kind="desktop" />}
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

  const operations = useRef(new AbortController());

  const fileOp = (type: string, payload: Record<string, unknown> = {}) =>
    requestFiles<{ type: string; entries?: RemoteEntry[]; error?: string }>(
      new WebSocket(wsUrl("/ws/files", { device: device.id })),
      { type, ...payload },
      { signal: operations.current.signal },
    );

  const loadDir = async (p: string) => {
    const signal = operations.current.signal;
    setBusy(true);
    setMessage("");
    try {
      const msg = await fileOp("file:list", { path: p });
      if (signal.aborted) return;
      setEntries(msg.entries || []);
      setPath(p);
      setSelected(new Set());
    } catch (err) {
      if (!signal.aborted) setMessage(String(err));
    } finally {
      if (!signal.aborted) setBusy(false);
    }
  };

  useEffect(() => {
    const controller = new AbortController();
    operations.current = controller;
    (async () => {
      try {
        const msg = await fileOp("file:roots");
        if (controller.signal.aborted) return;
        const roots = msg.entries || [];
        if (roots.length > 0) {
          await loadDir(roots[0].path);
        } else {
          setMessage("No allowed file roots configured on the agent.");
        }
      } catch (err) {
        if (!controller.signal.aborted) setMessage(String(err));
      }
    })();
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [device.id]);

  const separator = path.includes("\\") ? "\\" : "/";
  const joinPath = (dir: string, name: string) => (dir.endsWith(separator) ? dir + name : dir + separator + name);

  const uploadOne = (file: File) => uploadFile(
    new WebSocket(wsUrl("/ws/file", { device: device.id, op: "upload", name: file.name, path, size: file.size })),
    file,
    { signal: operations.current.signal },
  );

  const handleUpload = async (files: FileList | File[]) => {
    const signal = operations.current.signal;
    setBusy(true);
    setMessage("");
    try {
      for (const file of Array.from(files)) {
        await uploadOne(file);
        if (signal.aborted) return;
      }
      await loadDir(path);
    } catch (err) {
      if (!signal.aborted) setMessage(String(err));
    } finally {
      if (!signal.aborted) setBusy(false);
      if (fileRef.current) {
        fileRef.current.value = "";
      }
    }
  };

  const downloadPath = async (p: string, name: string) => {
    const signal = operations.current.signal;
    const blob = await downloadFile(
      new WebSocket(wsUrl("/ws/file", { device: device.id, op: "download", path: p })),
      { signal },
    );
    if (signal.aborted) return;
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = name;
    a.click();
    // Give the browser time to consume the URL before revoking it.
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  };

  const handleDownload = async (p: string, name: string) => {
    const signal = operations.current.signal;
    setBusy(true);
    setMessage("");
    try { await downloadPath(p, name); }
    catch (err) { if (!signal.aborted) setMessage(String(err)); }
    finally { if (!signal.aborted) setBusy(false); }
  };

  const handleDownloadSelected = async () => {
    const signal = operations.current.signal;
    setBusy(true);
    setMessage("");
    try {
      for (const p of selected) {
        const entry = entries.find((e) => e.path === p);
        if (entry && !entry.isDir) {
          await downloadPath(p, entry.name);
          if (signal.aborted) return;
        }
      }
    } catch (err) {
      if (!signal.aborted) setMessage(String(err));
    } finally {
      if (!signal.aborted) setBusy(false);
    }
  };

  const handleNewFolder = async () => {
    const signal = operations.current.signal;
    const name = prompt("New folder name");
    if (!name?.trim()) return;
    try {
      await fileOp("file:mkdir", { path: joinPath(path, name.trim()) });
      if (signal.aborted) return;
      await loadDir(path);
    } catch (err) {
      if (!signal.aborted) setMessage(String(err));
    }
  };

  const handleRename = async (entry: RemoteEntry) => {
    const signal = operations.current.signal;
    const name = prompt("New name", entry.name);
    if (!name?.trim() || name.trim() === entry.name) return;
    try {
      await fileOp("file:rename", {
        path: entry.path,
        target: joinPath(path, name.trim()),
      });
      if (signal.aborted) return;
      await loadDir(path);
    } catch (err) {
      if (!signal.aborted) setMessage(String(err));
    }
  };

  const handleDeleteSelected = async () => {
    const signal = operations.current.signal;
    if (selected.size === 0 || !confirm(`Delete ${selected.size} item(s)?`)) return;
    setBusy(true);
    setMessage("");
    try {
      for (const p of selected) {
        await fileOp("file:delete", { path: p });
        if (signal.aborted) return;
      }
      await loadDir(path);
    } catch (err) {
      if (!signal.aborted) setMessage(String(err));
    } finally {
      if (!signal.aborted) setBusy(false);
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
        <p className="file-message">Downloads are buffered in browser memory (maximum 128 MiB). Use another file transfer tool for larger files.</p>
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
                  onClick={() => (entry.isDir ? loadDir(entry.path) : handleDownload(entry.path, entry.name))}
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
