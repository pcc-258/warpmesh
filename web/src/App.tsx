import { useCallback, useEffect, useRef, useState } from "react";
import {
  Activity,
  ArrowLeft,
  Download,
  FileText,
  Folder,
  FolderPlus,
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
  Upload,
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
  getStats,
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
  type Device,
  type DeviceKey,
  type Invite,
  type Stats,
  type UserAccount,
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
    return <FileManagerView device={view.device} onBack={() => setView({ name: "page", page: "devices" })} />;
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
  const [invites, setInvites] = useState<Invite[]>([]);
  const [users, setUsers] = useState<UserAccount[]>([]);
  const [canManageUsers, setCanManageUsers] = useState(false);
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
  onUsersChanged: () => void;
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

  const submitCreateUser = async (e: React.FormEvent) => {
    e.preventDefault();
    await createUser({
      username: newUsername.trim(),
      password: newPassword,
      role: newRole,
      deviceIds: newDevices,
      expiresAt: newExpiry ? new Date(newExpiry).toISOString() : undefined,
    });
    setNewUsername("");
    setNewPassword("");
    setNewDevices([]);
    setNewExpiry("");
    onUsersChanged();
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
    const value = prompt("Allowed device ids, comma separated", user.deviceIds.join(","));
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
          </div>
          <form className="user-form" onSubmit={submitCreateUser}>
            <input value={newUsername} onChange={(e) => setNewUsername(e.target.value)} placeholder="username" required />
            <input type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} placeholder="password" required />
            <select value={newRole} onChange={(e) => setNewRole(e.target.value)}>
              <option value="operator">operator</option>
              <option value="admin">admin</option>
            </select>
            <input type="date" value={newExpiry} onChange={(e) => setNewExpiry(e.target.value)} />
            <select multiple value={newDevices} onChange={(e) => setNewDevices(Array.from(e.target.selectedOptions, (o) => o.value))}>
              {devices.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.name} ({d.id})
                </option>
              ))}
            </select>
            <button className="primary-btn inline" type="submit">
              Create user
            </button>
          </form>
          <div className="key-list">
            {users.map((user) => (
              <div className="key-row" key={user.username}>
                <div>
                  <strong>{user.username}</strong>
                  <span>
                    {user.role} · {user.deviceIds.length} devices
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
