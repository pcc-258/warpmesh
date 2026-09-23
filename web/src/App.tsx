import { useCallback, useEffect, useRef, useState } from "react";
import {
  Activity,
  ArrowLeft,
  CloudUpload,
  Download,
  HardDrive,
  KeyRound,
  LayoutDashboard,
  Layers,
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
  Wifi,
} from "lucide-react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import {
  clearToken,
  createDeviceKey,
  deleteDevice,
  getStats,
  getToken,
  lastSeenLabel,
  listAudit,
  listDeviceKeys,
  listDevices,
  login,
  logout,
  renameDevice,
  revokeDeviceKey,
  type AuditEntry,
  type Device,
  type DeviceKey,
  type Stats,
  wsUrl,
} from "./api";

type Page = "dashboard" | "devices" | "access" | "activity";

type View =
  | { name: "page"; page: Page }
  | { name: "terminal"; device: Device }
  | { name: "desktop"; device: Device }
  | { name: "files"; device: Device };

export default function App() {
  const [token, setTokenState] = useState(getToken());
  const [view, setView] = useState<View>({ name: "page", page: "dashboard" });

  useEffect(() => {
    if (!token) {
      return;
    }
    const match = location.hash.match(/^#\/(desktop|terminal)\/(.+)$/);
    if (!match) {
      return;
    }
    listDevices()
      .then((devices) => {
        const device = devices.find((d) => d.id === decodeURIComponent(match[2]));
        if (device) {
          setView({ name: match[1] === "desktop" ? "desktop" : "terminal", device });
        }
      })
      .catch(() => {});
  }, [token]);

  if (!token) {
    return <Login onLogin={() => setTokenState(getToken())} />;
  }

  if (view.name === "terminal") {
    return <TerminalView device={view.device} onBack={() => setView({ name: "page", page: "devices" })} />;
  }

  if (view.name === "desktop") {
    return <DesktopView device={view.device} onBack={() => setView({ name: "page", page: "devices" })} />;
  }

  if (view.name === "files") {
    return <FilesView device={view.device} onBack={() => setView({ name: "page", page: "devices" })} />;
  }

  return (
    <Console
      page={view.page}
      onNavigate={(page) => setView({ name: "page", page })}
      onTerminal={(d) => setView({ name: "terminal", device: d })}
      onDesktop={(d) => setView({ name: "desktop", device: d })}
      onFiles={(d) => setView({ name: "files", device: d })}
      onLogout={async () => {
        try {
          await logout();
        } catch {
          // local logout must still work when the server is unreachable
        }
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
  const [stats, setStats] = useState<Stats | null>(null);
  const [keys, setKeys] = useState<DeviceKey[]>([]);
  const [audit, setAudit] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const [deviceList, statsData] = await Promise.all([listDevices(), getStats()]);
      setDevices(deviceList);
      setStats(statsData);
    } catch {
      onLogout();
    } finally {
      setLoading(false);
    }
  }, [onLogout]);

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

  useEffect(() => {
    refresh();
    loadKeys();
    loadAudit();
    const timer = setInterval(refresh, 5000);
    return () => clearInterval(timer);
  }, [refresh, loadKeys, loadAudit]);

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
          <button className={page === "dashboard" ? "active" : ""} onClick={() => onNavigate("dashboard")}>
            <LayoutDashboard size={17} />
            Dashboard
          </button>
          <button className={page === "devices" ? "active" : ""} onClick={() => onNavigate("devices")}>
            <MonitorSmartphone size={17} />
            Devices
          </button>
          <button className={page === "access" ? "active" : ""} onClick={() => onNavigate("access")}>
            <KeyRound size={17} />
            Access
          </button>
          <button className={page === "activity" ? "active" : ""} onClick={() => onNavigate("activity")}>
            <ScrollText size={17} />
            Activity
          </button>
        </nav>
        <div className="sidebar-foot">
          <div className="stat-pill">
            <Activity size={14} />
            {stats?.online ?? 0}/{stats?.total ?? 0} online
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
            <p>{page === "dashboard" ? "Fleet overview" : page === "devices" ? "Managed devices" : page === "access" ? "Device credentials" : "Security events"}</p>
          </div>
          <button className="icon-btn" onClick={refresh} title="Refresh" disabled={loading}>
            <RefreshCw size={16} className={loading ? "spin" : ""} />
          </button>
        </header>

        <div className="content">
          {page === "dashboard" && <DashboardPage stats={stats} audit={audit} />}
          {page === "devices" && (
            <DevicesPage devices={devices} onTerminal={onTerminal} onDesktop={onDesktop} onFiles={onFiles} />
          )}
          {page === "access" && <AccessPage keys={keys} onCreate={handleCreateKey} onRevoke={handleRevokeKey} />}
          {page === "activity" && <ActivityPage audit={audit} />}
        </div>
      </section>
    </div>
  );
}

function DashboardPage({ stats, audit }: { stats: Stats | null; audit: AuditEntry[] }) {
  const osEntries = Object.entries(stats?.byOS ?? {}).sort((a, b) => b[1] - a[1]);
  const activity = audit.length > 0 ? audit : stats?.recentActivity ?? [];
  return (
    <div className="page-grid">
      <section className="stat-grid">
        <div className="stat-card">
          <MonitorSmartphone size={18} />
          <strong>{stats?.total ?? 0}</strong>
          <span>Devices</span>
        </div>
        <div className="stat-card accent">
          <Activity size={18} />
          <strong>{stats?.online ?? 0}</strong>
          <span>Online</span>
        </div>
        <div className="stat-card">
          <Layers size={18} />
          <strong>{stats?.groups?.length ?? 0}</strong>
          <span>Groups</span>
        </div>
        <div className="stat-card">
          <HardDrive size={18} />
          <strong>{Object.keys(stats?.byOS ?? {}).length}</strong>
          <span>Platforms</span>
        </div>
      </section>

      <section className="panel">
        <div className="panel-head">
          <h3>Platform distribution</h3>
        </div>
        {osEntries.length === 0 ? (
          <div className="inline-empty">No agents registered yet.</div>
        ) : (
          <div className="os-list">
            {osEntries.map(([os, count]) => (
              <div className="os-row" key={os}>
                <span>{os}</span>
                <div className="os-track">
                  <div style={{ width: `${Math.min(100, (count / Math.max(1, osEntries[0][1])) * 100)}%` }} />
                </div>
                <strong>{count}</strong>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="panel">
        <div className="panel-head">
          <h3>Recent activity</h3>
        </div>
        <ActivityList audit={activity.slice(0, 10)} />
      </section>
    </div>
  );
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
  const groups = Array.from(new Set(devices.map((d) => d.group).filter(Boolean)));
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
  onCreate,
  onRevoke,
}: {
  keys: DeviceKey[];
  onCreate: () => void;
  onRevoke: (key: DeviceKey) => void;
}) {
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
              </div>
              <button className="icon-btn danger" title="Revoke" onClick={() => onRevoke(key)}>
                <Trash2 size={14} />
              </button>
            </div>
          ))}
        </div>
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
        const bytes = Uint8Array.from(atob(msg.data), (c) => c.charCodeAt(0));
        term.write(bytes);
      }
      if (msg.type === "terminal:exit" || msg.type === "terminal:error") {
        term.writeln("\r\n\x1b[90m[session ended]\x1b[0m");
        socket.close();
      }
    };
    socket.onclose = () => term.writeln("\r\n\x1b[90m[connection closed]\x1b[0m");

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
        <div className={`screen-status ${status === "connected" ? "ok" : ""}`}>{status}</div>
      </header>
      <main className="desktop-host">
        <div ref={hostRef} className="desktop-canvas" />
      </main>
    </div>
  );
}

function FilesView({ device, onBack }: { device: Device; onBack: () => void }) {
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  const upload = async (file: File) => {
    setBusy(true);
    setMessage("");
    try {
      const socket = new WebSocket(
        wsUrl("/ws/file", {
          token: getToken(),
          device: device.id,
          op: "upload",
          name: file.name,
          size: file.size,
        }),
      );
      await new Promise<void>((resolve, reject) => {
        socket.onopen = () => resolve();
        socket.onerror = () => reject(new Error("socket error"));
      });
      const raw = new Uint8Array(await file.arrayBuffer());
      const CHUNK = 64 * 1024;
      for (let i = 0; i < raw.length; i += CHUNK) {
        socket.send(raw.slice(i, i + CHUNK));
      }
      const done = new Promise<void>((resolve) => {
        socket.onmessage = (event) => {
          const msg = JSON.parse(event.data);
          if (msg.type === "file:done" || msg.type === "file:error") {
            socket.close();
            resolve();
          }
        };
        socket.onclose = () => resolve();
      });
      socket.send(JSON.stringify({ type: "file:upload:done" }));
      await done;
      setMessage(`Uploaded ${file.name} to ${device.name}.`);
    } catch (err) {
      setMessage(String(err));
    } finally {
      setBusy(false);
    }
  };

  const download = async () => {
    if (!path.trim()) {
      setMessage("Enter a path on the device, e.g. /home/user/notes.txt");
      return;
    }
    setBusy(true);
    setMessage("");
    try {
      const socket = new WebSocket(
        wsUrl("/ws/file", {
          token: getToken(),
          device: device.id,
          op: "download",
          path: path.trim(),
        }),
      );
      socket.binaryType = "arraybuffer";
      const chunks: BlobPart[] = [];
      await new Promise<void>((resolve, reject) => {
        socket.onopen = () => resolve();
        socket.onerror = () => reject(new Error("socket error"));
      });
      await new Promise<void>((resolve) => {
        socket.onmessage = (event) => {
          if (typeof event.data === "string") {
            const msg = JSON.parse(event.data);
            if (msg.type === "file:done") {
              const blob = new Blob(chunks);
              const url = URL.createObjectURL(blob);
              const a = document.createElement("a");
              a.href = url;
              a.download = path.trim().split("/").pop() || "download";
              a.click();
              URL.revokeObjectURL(url);
              socket.close();
              resolve();
            }
            if (msg.type === "file:error") {
              setMessage(msg.error || "download failed");
              socket.close();
              resolve();
            }
          } else {
            chunks.push(event.data);
          }
        };
        socket.onclose = () => resolve();
      });
      setMessage("Download complete.");
    } catch (err) {
      setMessage(String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="app-shell">
      <header className="topbar">
        <button className="back-btn" onClick={onBack}>
          <ArrowLeft size={16} />
          Back
        </button>
        <div className="terminal-title">
          <CloudUpload size={16} />
          {device.name} - file transfer
        </div>
      </header>
      <main className="content files-content">
        <section className="file-panel">
          <h3>Upload to device</h3>
          <p>Files land in the agent data directory under uploads/.</p>
          <input
            ref={fileRef}
            type="file"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) upload(file);
            }}
          />
        </section>
        <section className="file-panel">
          <h3>Download from device</h3>
          <p>Read any file inside the agent's allowed path roots.</p>
          <div className="path-row">
            <input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="/home/user/notes.txt"
            />
            <button className="primary-btn inline" disabled={busy} onClick={download}>
              <Download size={15} />
              Download
            </button>
          </div>
        </section>
        {message && <div className="file-message">{message}</div>}
      </main>
    </div>
  );
}
