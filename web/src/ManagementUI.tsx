import { useEffect, useRef, useState } from "react";
import { Activity, Download, Monitor, Search, TerminalSquare, X } from "lucide-react";
import { createInvite, deleteDevice, deleteInvite, listAgentDownloads, optionalDate, setForwardPolicy, updateDevice, updateUser, type Device, type Invite, type UserAccount } from "./api";
import { enrollmentCommands, expiryPatch, filterDevices } from "./managementHelpers";

export type ActionRequest = {
  title: string;
  description?: string;
  destructive?: boolean;
  fields?: { name: string; label: string; value?: string; type?: "text" | "password"; required?: boolean }[];
  run: (values: Record<string, string>) => Promise<string | void>;
};

export function Modal({ title, children, onClose, busy = false }: { title: string; children: React.ReactNode; onClose: () => void; busy?: boolean }) {
  const root = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);
  const busyRef = useRef(busy);
  closeRef.current = onClose;
  busyRef.current = busy;
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const focusable = () => Array.from(root.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea, a[href]') ?? []);
    focusable()[0]?.focus();
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busyRef.current) closeRef.current();
      if (event.key !== "Tab") return;
      const list = focusable();
      const first = list[0], last = list[list.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    };
    document.addEventListener("keydown", key);
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => { document.removeEventListener("keydown", key); document.body.style.overflow = overflow; previous?.focus(); };
  }, []);
  return <div className="management-overlay" onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) onClose(); }}>
    <div className="management-dialog" ref={root} role="dialog" aria-modal="true" aria-label={title}>
      <div className="management-dialog-head"><h2>{title}</h2><button className="icon-btn" aria-label="Close dialog" disabled={busy} onClick={onClose}><X size={18} /></button></div>
      {children}
    </div>
  </div>;
}

export function ActionDialog({ request, onClose, onSuccess }: { request: ActionRequest; onClose: () => void; onSuccess: (message: string) => void }) {
  const [values, setValues] = useState<Record<string, string>>(() => Object.fromEntries((request.fields ?? []).map((f) => [f.name, f.value ?? ""])));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState("");
  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setBusy(true); setError("");
    try {
      for (const field of request.fields ?? []) { if (field.required && !values[field.name]?.trim()) throw new Error(`${field.label} is required.`); }
      const message = await request.run(values);
      if (message) setResult(message);
      else { onSuccess(`${request.title} completed.`); onClose(); }
    } catch (failure) { setError(failure instanceof Error ? failure.message : "Operation failed. Please retry."); }
    finally { setBusy(false); }
  };
  return <Modal title={request.title} onClose={onClose} busy={busy}>
    {result ? <><p>Save this information securely. Credentials are shown only once.</p><textarea aria-label="Operation result" readOnly value={result} rows={7} /><button className="primary-btn inline" onClick={onClose}>Done</button></> :
      <form onSubmit={submit} className="management-form">
        {request.description && <p>{request.description}</p>}
        {(request.fields ?? []).map((field) => <label key={field.name}>{field.label}<input type={field.type ?? "text"} required={field.required} value={values[field.name]} onChange={(event) => setValues({ ...values, [field.name]: event.target.value })} disabled={busy} /></label>)}
        {error && <div role="alert" className="form-error">{error}</div>}
        <div className="management-actions"><button className="secondary-btn" type="button" disabled={busy} onClick={onClose}>Cancel</button><button className={`primary-btn inline ${request.destructive ? "danger" : ""}`} disabled={busy}>{busy ? "Working…" : request.destructive ? "Confirm deletion" : "Save"}</button></div>
      </form>}
  </Modal>;
}

export function DeviceManager({ devices, canManage, onChanged, onAction, onAdd, onTerminal, onDesktop, onFiles }: {
  devices: Device[]; canManage: boolean; onChanged: () => Promise<void>; onAction: (request: ActionRequest) => void; onAdd: () => void;
  onTerminal: (device: Device) => void; onDesktop: (device: Device) => void; onFiles: (device: Device) => void;
}) {
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("all");
  const [group, setGroup] = useState("");
  const [os, setOS] = useState("");
  const [detailID, setDetailID] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const groups = Array.from(new Set(devices.map((d) => d.group).filter(Boolean))) as string[];
  const systems = Array.from(new Set(devices.map((d) => d.os))).sort();
  const filtered = filterDevices(devices, query, status, group, os);
  const detail = devices.find((d) => d.id === detailID);
  const targets = devices.filter((d) => selected.includes(d.id));
  const open = (device: Device, kind: "terminal" | "desktop" | "files") => { setDetailID(""); ({ terminal: onTerminal, desktop: onDesktop, files: onFiles })[kind](device); };
  const edit = (device: Device) => {
    setDetailID("");
    onAction({ title: `Edit ${device.name}`, fields: [{ name: "name", label: "Device name", value: device.name, required: true }, { name: "group", label: "Group (leave empty to remove)", value: device.group }], run: async (values) => {
      await updateDevice(device.id, { name: values.name.trim(), group: values.group.trim() }); await onChanged();
    } });
  };
  const remove = (device: Device) => {
    setDetailID("");
    onAction({ title: `Remove ${device.name}`, destructive: true, description: "Removes the inventory entry. To prevent re-enrollment, also revoke its device key in Access.", run: async () => { await deleteDevice(device.id); await onChanged(); } });
  };
  const batchGroup = () => onAction({ title: `Group ${targets.length} devices`, fields: [{ name: "group", label: "Group (leave empty to remove)" }], run: async (values) => {
    const failed: string[] = [];
    for (const d of targets) { try { await updateDevice(d.id, { group: values.group.trim() }); } catch { failed.push(d.name); } }
    await onChanged();
    if (failed.length) throw new Error(`Some devices could not be updated: ${failed.join(", ")}. Successful changes are retained.`);
    setSelected([]);
  } });
  const operationButtons = (device: Device) => <div className="management-actions">
    <button className="secondary-btn" disabled={!device.online || device.capabilities?.terminal === false} onClick={() => open(device, "terminal")}><TerminalSquare size={15} /> Terminal</button>
    <button className="secondary-btn" disabled={!device.online || device.capabilities?.desktop === false} title={device.capabilities?.desktopHint} onClick={() => open(device, "desktop")}><Monitor size={15} /> Desktop</button>
    <button className="secondary-btn" disabled={!device.online || device.capabilities?.files === false} onClick={() => open(device, "files")}><Download size={15} /> Files</button>
  </div>;
  return <>
    <div className="management-toolbar">
      <label className="management-search"><Search size={16} /><input aria-label="Search devices" placeholder="Search name, IP, hostname or group" value={query} onChange={(e) => setQuery(e.target.value)} /></label>
      <select aria-label="Device status" value={status} onChange={(e) => setStatus(e.target.value)}><option value="all">All statuses</option><option value="online">Online</option><option value="offline">Offline</option></select>
      <select aria-label="Device group" value={group} onChange={(e) => setGroup(e.target.value)}><option value="">All groups</option>{groups.map((g) => <option key={g}>{g}</option>)}</select>
      <select aria-label="Operating system" value={os} onChange={(e) => setOS(e.target.value)}><option value="">All systems</option>{systems.map((s) => <option key={s}>{s}</option>)}</select>
      {canManage && <button className="primary-btn inline" onClick={onAdd}>Add device</button>}
    </div>
    <div className="management-summary"><span>{filtered.length} of {devices.length} devices · {devices.filter((d) => d.online).length} online</span><button className="secondary-btn" onClick={() => { setQuery(""); setGroup(""); setOS(""); setStatus("all"); }}>Clear filters</button></div>
    {canManage && <div className="management-actions"><label><input type="checkbox" aria-label="Select visible devices" checked={filtered.length > 0 && filtered.every((d) => selected.includes(d.id))} onChange={(e) => setSelected(e.target.checked ? Array.from(new Set([...selected, ...filtered.map((d) => d.id)])) : selected.filter((id) => !filtered.some((d) => d.id === id)))} /> Select visible</label><button className="secondary-btn" disabled={!targets.length} onClick={batchGroup}>Group selected ({targets.length})</button></div>}
    <section className="panel management-device-list">
      {!filtered.length && <div className="inline-empty">{devices.length ? "No devices match these filters." : "Add your first device to get started."}</div>}
      {filtered.map((device) => <article className="management-device" key={device.id}>
        <div className="management-device-title">{canManage && <input type="checkbox" aria-label={`Select ${device.name}`} checked={selected.includes(device.id)} onChange={(e) => setSelected(e.target.checked ? [...selected, device.id] : selected.filter((id) => id !== device.id))} />}<span className={`status-dot ${device.online ? "online" : "offline"}`} /><button className="management-name" onClick={() => setDetailID(device.id)}>{device.name}</button><span className="management-badge">{device.online ? "Online" : "Offline"}</span></div>
        <p>{device.hostname || device.id} · {device.os}/{device.arch}{device.group ? ` · ${device.group}` : ""}</p>
        <small>{(device.lanIPs ?? []).join(", ") || "No network addresses reported"}</small>
        {device.online && <p className="management-hint">{!device.capabilities ? "Service status unknown — update the agent for readiness checks." : device.capabilities.desktop ? "VNC reachable · Screen access depends on system permissions" : "Desktop setup required · Terminal and files supported"}</p>}
        {operationButtons(device)}
        <div className="management-actions"><button className="secondary-btn" onClick={() => setDetailID(device.id)}>Details</button>{canManage && <><button className="secondary-btn" onClick={() => edit(device)}>Edit</button><button className="secondary-btn danger" onClick={() => remove(device)}>Remove</button></>}</div>
      </article>)}
    </section>
    {detail && <Modal title={detail.name} onClose={() => setDetailID("")}>
      <dl className="management-details">{Object.entries({ Status: detail.online ? "Online" : "Offline", Hostname: detail.hostname || "Not reported", System: `${detail.os}/${detail.arch}`, Group: detail.group || "None", "Device ID": detail.id, Addresses: (detail.lanIPs ?? []).join(", ") || "Not reported", "Last seen": new Date(detail.lastSeen).toLocaleString(), Registered: new Date(detail.createdAt).toLocaleString(), Forwarding: detail.forwardPolicy }).map(([key, value]) => <div key={key}><dt>{key}</dt><dd>{value}</dd></div>)}</dl>
      <h3>Remote access</h3><p>{!detail.online ? "This device is offline. Check that the agent service is running and can reach this server." : detail.capabilities?.desktopHint || "This agent has not reported service readiness. Install the latest agent to enable checks."}</p>
      {operationButtons(detail)}
      {canManage && <div className="management-actions"><button className="secondary-btn" onClick={() => edit(detail)}>Edit device</button><button className="secondary-btn" onClick={() => { setDetailID(""); onAction({ title: "Change forwarding access", description: detail.forwardPolicy === "restricted" ? "Allow this device to forward to any device and port?" : "Restrict this device to explicitly granted destinations?", run: async () => { await setForwardPolicy(detail.id, detail.forwardPolicy === "restricted" ? "any" : "restricted"); await onChanged(); } }); }}>Change forwarding access</button></div>}
    </Modal>}
  </>;
}

export function AccountEditor({ user, devices, onClose, onSaved }: { user: UserAccount; devices: Device[]; onClose: () => void; onSaved: () => Promise<void> }) {
  const [role, setRole] = useState(user.role);
  const [password, setPassword] = useState("");
  const [expiry, setExpiry] = useState(optionalDate(user.expiresAt)?.slice(0, 10) ?? "");
  const [selected, setSelected] = useState(user.deviceIds ?? []);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setBusy(true); setError("");
    try { await updateUser(user.username, { role, deviceIds: selected, expiresAt: expiry === (optionalDate(user.expiresAt)?.slice(0, 10) ?? "") ? undefined : expiryPatch(expiry), ...(password ? { password } : {}) }); await onSaved(); onClose(); }
    catch (failure) { setError(failure instanceof Error ? failure.message : "Unable to save account."); }
    finally { setBusy(false); }
  };
  const visible = filterDevices(devices, query, "all", "", "");
  return <Modal title={`Edit account: ${user.username}`} onClose={onClose} busy={busy}><form className="management-form" onSubmit={submit}>
    <label>Role<select value={role} disabled={busy} onChange={(e) => setRole(e.target.value)}><option value="operator">Operator — selected devices</option><option value="admin">Administrator — all devices</option></select></label>
    <label>New password (leave empty to keep current)<input type="password" autoComplete="new-password" value={password} disabled={busy} onChange={(e) => setPassword(e.target.value)} /></label>
    <label>Expiry date (leave empty for no expiry)<input type="date" value={expiry} disabled={busy} onChange={(e) => setExpiry(e.target.value)} /></label>
    <button type="button" className="secondary-btn" disabled={busy || !expiry} onClick={() => setExpiry("")}>Clear expiry</button>
    {role === "admin" ? <p>Administrators can manage and access all devices.</p> : <fieldset><legend>Allowed devices ({selected.length})</legend>
      <input aria-label="Search allowed devices" placeholder="Search devices" value={query} disabled={busy} onChange={(e) => setQuery(e.target.value)} />
      <div className="management-actions"><button type="button" className="secondary-btn" disabled={busy} onClick={() => setSelected(Array.from(new Set([...selected, ...visible.map((d) => d.id)])))}>Select matches</button><button type="button" className="secondary-btn" disabled={busy} onClick={() => setSelected([])}>Clear selection</button></div>
      <div className="account-device-list">{visible.map((device) => <label className="account-device-option" key={device.id}><input type="checkbox" disabled={busy} checked={selected.includes(device.id)} onChange={(e) => setSelected(e.target.checked ? [...selected, device.id] : selected.filter((id) => id !== device.id))} /><span><strong>{device.name}</strong><small>{device.group || device.os} · {device.online ? "Online" : "Offline"}</small></span></label>)}</div>
      {!visible.length && <p>No matching devices.</p>}
    </fieldset>}
    {error && <div role="alert" className="form-error">{error}</div>}
    <div className="management-actions"><button type="button" className="secondary-btn" disabled={busy} onClick={onClose}>Cancel</button><button className="primary-btn inline" disabled={busy}>{busy ? "Saving…" : "Save account"}</button></div>
  </form></Modal>;
}

export function EnrollmentDialog({ devices, onClose, onInvitesChanged }: { devices: Device[]; onClose: () => void; onInvitesChanged: () => Promise<void> }) {
  const [platform, setPlatform] = useState("linux-amd64");
  const [name, setName] = useState("");
  const [invite, setInvite] = useState<Invite | null>(null);
  const [builds, setBuilds] = useState<{ platform: string; available: boolean }[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const knownIDs = useRef(new Set(devices.map((d) => d.id)));
  useEffect(() => { let active = true; listAgentDownloads().then((value) => { if (active) setBuilds(value ?? []); }).catch((failure) => { if (active) setError(String(failure)); }); return () => { active = false; }; }, []);
  const added = devices.find((d) => d.online && !knownIDs.current.has(d.id) && d.name === name.trim());
  const ready = builds.find((b) => b.platform === platform)?.available;
  const command = invite ? enrollmentCommands(location.origin, invite.code, name.trim(), platform) : "";
  const generate = async (event: React.FormEvent) => {
    event.preventDefault(); setBusy(true); setError("");
    try { setInvite(await createInvite(name.trim())); await onInvitesChanged(); }
    catch (failure) { setError(failure instanceof Error ? failure.message : "Unable to create invite."); }
    finally { setBusy(false); }
  };
  const cancelInvite = async () => {
    if (!invite) return;
    setBusy(true); setError("");
    try { await deleteInvite(invite.code); await onInvitesChanged(); onClose(); }
    catch (failure) { setError(failure instanceof Error ? failure.message : "Could not revoke invite."); }
    finally { setBusy(false); }
  };
  return <Modal title="Add device" onClose={onClose} busy={busy}>
    <p>1. Choose a platform. 2. Download the agent. 3. Run the commands on that device.</p>
    <form className="management-form" onSubmit={generate}>
      <label>Device name<input required maxLength={100} value={name} disabled={busy || !!invite} onChange={(e) => setName(e.target.value)} placeholder="Home desktop" /></label>
      <label>System / architecture<select value={platform} onChange={(e) => { setPlatform(e.target.value); setCopied(false); }}><option value="linux-amd64">Linux · Intel / AMD 64-bit</option><option value="linux-arm64">Linux · ARM 64-bit</option><option value="darwin-arm64">macOS · Apple Silicon</option><option value="darwin-amd64">macOS · Intel</option><option value="windows-amd64">Windows · Intel / AMD 64-bit</option></select></label>
      {!invite && <button className="primary-btn inline" disabled={busy || !name.trim()}>{busy ? "Preparing…" : "Prepare enrollment"}</button>}
    </form>
    {invite && <div className="management-form">
      <p>Invite expires {new Date(invite.expiresAt).toLocaleString()}. It can enroll one device.</p>
      {ready ? <a className="primary-btn inline" href={`/api/agent-downloads/${platform}`} download>Download agent</a> : <div role="status" className="management-hint">This server does not have a downloadable build for this platform yet.</div>}
      <p>On the target device, open {platform.startsWith("windows") ? "PowerShell" : "a terminal"} in the download folder, then run:</p>
      <textarea aria-label="Enrollment commands" rows={6} readOnly value={command} />
      <button className="secondary-btn" onClick={async () => { try { await navigator.clipboard.writeText(command); setCopied(true); } catch { setError("Clipboard access is unavailable. Select and copy the commands above."); } }}>{copied ? "Copied" : "Copy commands"}</button>
      <p>Keep the data folder: it stores the device identity. These commands run the agent in the foreground; configure an OS service for unattended startup.</p>
      <p>{platform.startsWith("darwin") ? "For desktop access, enable macOS Screen Sharing and check screen access permissions." : platform.startsWith("windows") ? "For desktop access, install and start TightVNC Server." : "For desktop access, use an X11 session with x11vnc, or the WarpMesh desktop container."}</p>
      <div className="management-enrollment-status" role="status"><Activity size={16} />{added ? `${added.name} is online. Open its details to check available services.` : "Waiting for the new device to come online…"}</div>
      <div className="management-actions"><button className="secondary-btn danger" disabled={busy || !!added} onClick={cancelInvite}>Revoke unused invite</button><button className="primary-btn inline" onClick={onClose}>Done</button></div>
    </div>}
    {error && <div role="alert" className="form-error">{error}</div>}
  </Modal>;
}
