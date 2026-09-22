import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Activity,
  ArrowLeft,
  CloudUpload,
  Download,
  HardDrive,
  LogOut,
  MonitorSmartphone,
  Pencil,
  RefreshCw,
  Server,
  TerminalSquare,
  Trash2,
  Wifi,
} from "lucide-react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import {
  clearToken,
  deleteDevice,
  getToken,
  lastSeenLabel,
  listDevices,
  renameDevice,
  setToken,
  type Device,
  wsUrl,
} from "./api";

type View =
  | { name: "dashboard" }
  | { name: "terminal"; device: Device }
  | { name: "files"; device: Device };

export default function App() {
  const [token, setTokenState] = useState(getToken());
  const [view, setView] = useState<View>({ name: "dashboard" });

  if (!token) {
    return <Login onLogin={(t) => setTokenState(t)} />;
  }

  if (view.name === "terminal") {
    return <TerminalView device={view.device} onBack={() => setView({ name: "dashboard" })} />;
  }

  if (view.name === "files") {
    return <FilesView device={view.device} onBack={() => setView({ name: "dashboard" })} />;
  }

  return (
    <Dashboard
      onLogout={() => {
        clearToken();
        setTokenState("");
      }}
      onTerminal={(d) => setView({ name: "terminal", device: d })}
      onFiles={(d) => setView({ name: "files", device: d })}
    />
  );
}

function Login({ onLogin }: { onLogin: (token: string) => void }) {
  const [value, setValue] = useState("");
  const [error, setError] = useState("");

  return (
    <div className="login-wrap">
      <div className="login-panel">
        <div className="brand-mark">
          <Server size={22} />
        </div>
        <h1>Device Relay</h1>
        <p>Centralized control plane for your devices.</p>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!value.trim()) {
              setError("Token is required");
              return;
            }
            setToken(value.trim());
            onLogin(value.trim());
          }}
        >
          <label htmlFor="token">Admin token</label>
          <input
            id="token"
            type="password"
            value={value}
            onChange={(e) => {
              setValue(e.target.value);
              setError("");
            }}
            placeholder="Enter your admin token"
            autoFocus
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

function Dashboard({
  onLogout,
  onTerminal,
  onFiles,
}: {
  onLogout: () => void;
  onTerminal: (d: Device) => void;
  onFiles: (d: Device) => void;
}) {
  const [devices, setDevices] = useState<Device[]>([]);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setDevices(await listDevices());
    } catch {
      onLogout();
    } finally {
      setLoading(false);
    }
  }, [onLogout]);

  useEffect(() => {
    refresh();
    const timer = setInterval(refresh, 5000);
    return () => clearInterval(timer);
  }, [refresh]);

  const online = devices.filter((d) => d.online).length;

  const handleRename = async (device: Device) => {
    const name = prompt("New device name", device.name);
    if (name && name.trim()) {
      await renameDevice(device.id, name.trim());
      refresh();
    }
  };

  const handleDelete = async (device: Device) => {
    if (confirm(`Remove ${device.name} from the catalog?`)) {
      await deleteDevice(device.id);
      refresh();
    }
  };

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand">
          <div className="brand-mark small">
            <Server size={18} />
          </div>
          <div>
            <strong>Device Relay</strong>
            <span>management console</span>
          </div>
        </div>
        <div className="topbar-actions">
          <div className="stat-pill">
            <Activity size={15} />
            {online}/{devices.length} online
          </div>
          <button className="icon-btn" onClick={refresh} title="Refresh" disabled={loading}>
            <RefreshCw size={16} className={loading ? "spin" : ""} />
          </button>
          <button className="icon-btn" onClick={onLogout} title="Log out">
            <LogOut size={16} />
          </button>
        </div>
      </header>

      <main className="content">
        <section className="page-head">
          <div>
            <h2>Devices</h2>
            <p>Agents connect outbound, so every device works behind NAT.</p>
          </div>
        </section>

        {devices.length === 0 ? (
          <div className="empty-state">
            <MonitorSmartphone size={32} />
            <h3>No devices yet</h3>
            <p>Run the agent on a device with the shared device token and it will appear here.</p>
          </div>
        ) : (
          <div className="device-grid">
            {devices.map((device) => (
              <article className="device-card" key={device.id}>
                <div className="device-card-head">
                  <div className={`status-dot ${device.online ? "online" : "offline"}`} />
                  <div className="device-title">
                    <strong>{device.name}</strong>
                    <span>{device.hostname || device.id}</span>
                  </div>
                  <span className="os-tag">
                    {device.os} / {device.arch}
                  </span>
                </div>
                <div className="device-meta">
                  <div>
                    <Wifi size={14} />
                    {device.lanIPs?.length ? device.lanIPs.join(", ") : "no LAN IP"}
                  </div>
                  <div>
                    <Activity size={14} />
                    {lastSeenLabel(device.lastSeen)}
                  </div>
                  <div>
                    <HardDrive size={14} />
                    {device.id}
                  </div>
                </div>
                <div className="device-actions">
                  <button className="action-btn" disabled={!device.online} onClick={() => onTerminal(device)}>
                    <TerminalSquare size={15} />
                    Terminal
                  </button>
                  <button className="action-btn" disabled={!device.online} onClick={() => onFiles(device)}>
                    <Download size={15} />
                    Files
                  </button>
                  <button className="icon-btn" title="Rename" onClick={() => handleRename(device)}>
                    <Pencil size={14} />
                  </button>
                  <button className="icon-btn danger" title="Delete" onClick={() => handleDelete(device)}>
                    <Trash2 size={14} />
                  </button>
                </div>
              </article>
            ))}
          </div>
        )}
      </main>
    </div>
  );
}

function TerminalView({ device, onBack }: { device: Device; onBack: () => void }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);

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
    wsRef.current = socket;

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
          <p>Read any file the agent process can access.</p>
          <div className="path-row">
            <input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="/home/user/notes.txt"
            />
            <button className="primary-btn" disabled={busy} onClick={download}>
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
