package node

const uiPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>WarpMesh Client</title>
<style>
:root{
  color-scheme:dark;
  --bg:#0b0e12;--panel:#131820;--panel-2:#171d26;--line:#28323e;--line-soft:#202832;
  --text:#edf3f7;--muted:#91a0af;--faint:#657383;--accent:#39d5b0;--accent-2:#8de7d0;
  --blue:#6aa9ff;--amber:#f4b860;--red:#ff7272;--radius:8px;
}
*{box-sizing:border-box}
html,body{height:100%}
body{margin:0;background:var(--bg);color:var(--text);font-family:Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;font-size:14px}
button,input,select{font:inherit}
button{cursor:pointer}
button:disabled{cursor:not-allowed;opacity:.48}
.app{min-height:100%;display:grid;grid-template-columns:244px minmax(0,1fr)}
.sidebar{background:#10151b;border-right:1px solid var(--line);padding:20px 14px;display:flex;flex-direction:column;min-height:100vh}
.brand{display:flex;align-items:center;gap:11px;padding:2px 8px 22px}
.brand-mark{width:34px;height:34px;border-radius:8px;background:var(--accent);color:#08201b;display:grid;place-items:center;font-weight:900;font-size:16px}
.brand-copy strong{display:block;font-size:15px;letter-spacing:0}
.brand-copy span{display:block;color:var(--muted);font-size:11px;margin-top:2px;text-transform:uppercase;letter-spacing:.08em}
.nav{display:grid;gap:4px}
.nav button{border:0;background:transparent;color:var(--muted);display:flex;align-items:center;gap:11px;padding:10px 11px;border-radius:7px;text-align:left;width:100%}
.nav button:hover{background:#171e27;color:var(--text)}
.nav button.active{background:#18262a;color:var(--accent-2)}
.nav .icon{width:17px;height:17px;flex:0 0 17px}
.side-node{margin-top:auto;border-top:1px solid var(--line);padding:16px 8px 2px}
.side-node-label{color:var(--faint);font-size:11px;text-transform:uppercase;letter-spacing:.08em;margin-bottom:8px}
.side-node-name{font-weight:650;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.side-node-meta{color:var(--muted);font-size:12px;margin-top:4px}
.state{display:inline-flex;align-items:center;gap:6px;margin-top:10px;color:var(--muted);font-size:12px}
.state-dot{width:7px;height:7px;border-radius:50%;background:var(--amber);box-shadow:0 0 0 3px rgba(244,184,96,.1)}
.state.online .state-dot{background:var(--accent);box-shadow:0 0 0 3px rgba(57,213,176,.1)}
.state.offline .state-dot{background:var(--red);box-shadow:0 0 0 3px rgba(255,114,114,.1)}
.main{min-width:0;padding:26px 30px 42px}
.topbar{display:flex;align-items:center;justify-content:space-between;gap:24px;margin-bottom:25px}
.eyebrow{color:var(--accent);font-size:11px;text-transform:uppercase;letter-spacing:.11em;font-weight:700;margin-bottom:6px}
h1{font-size:25px;line-height:1.15;margin:0;letter-spacing:0}
.topbar p{color:var(--muted);margin:7px 0 0}
.toolbar{display:flex;align-items:center;gap:8px}
.icon-btn,.btn{min-height:36px;border:1px solid var(--line);border-radius:7px;background:var(--panel);color:var(--text);display:inline-flex;align-items:center;justify-content:center;gap:8px;padding:0 12px}
.icon-btn{width:36px;padding:0}
.icon-btn .icon,.btn .icon{width:16px;height:16px}
.icon-btn:hover,.btn:hover{background:#1b222c;border-color:#354252}
.btn.primary{background:var(--accent);border-color:var(--accent);color:#06251e;font-weight:700}
.btn.primary:hover{background:var(--accent-2)}
.btn.danger{color:var(--red)}
.btn.small{min-height:30px;padding:0 9px;font-size:12px}
.view{display:none}
.view.active{display:block}
.notice{border:1px solid rgba(255,114,114,.35);background:rgba(255,114,114,.08);color:#ffd1d1;padding:11px 13px;border-radius:7px;margin-bottom:18px;display:none}
.notice.show{display:block}
.overview-strip{border:1px solid var(--line);background:var(--panel);border-radius:var(--radius);padding:18px 20px;display:flex;align-items:center;justify-content:space-between;gap:20px}
.node-title{display:flex;align-items:center;gap:12px;min-width:0}
.node-glyph{width:42px;height:42px;border-radius:8px;background:#1c2730;color:var(--accent);display:grid;place-items:center;flex:0 0 42px}
.node-glyph .icon{width:22px;height:22px}
.node-title strong{display:block;font-size:17px}
.node-title small{display:block;color:var(--muted);margin-top:4px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.endpoint{text-align:right;min-width:0}
.endpoint span{display:block;color:var(--faint);font-size:11px;text-transform:uppercase;letter-spacing:.08em;margin-bottom:5px}
.endpoint code{color:var(--accent-2);font:12px ui-monospace,SFMono-Regular,Consolas,monospace;word-break:break-all}
.metrics{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin:14px 0}
.metric{border:1px solid var(--line);background:var(--panel);border-radius:var(--radius);padding:15px 16px;min-width:0}
.metric-head{display:flex;align-items:center;justify-content:space-between;color:var(--muted);font-size:12px}
.metric-head .icon{width:16px;height:16px}
.metric strong{display:block;font-size:23px;margin-top:10px;line-height:1;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.metric small{display:block;color:var(--faint);margin-top:7px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.grid-2{display:grid;grid-template-columns:minmax(0,1.2fr) minmax(320px,.8fr);gap:14px;margin-top:14px}
.panel{border:1px solid var(--line);background:var(--panel);border-radius:var(--radius);min-width:0}
.panel-head{display:flex;align-items:flex-start;justify-content:space-between;gap:18px;padding:17px 18px 12px}
.panel-head h2{font-size:14px;margin:0}
.panel-head p{margin:5px 0 0;color:var(--muted);font-size:12px}
.panel-body{padding:0 18px 17px}
.service-list,.details-list{display:grid;gap:0}
.service-row,.detail-row{display:flex;align-items:center;justify-content:space-between;gap:18px;border-top:1px solid var(--line-soft);padding:12px 0;min-width:0}
.service-name{display:flex;align-items:center;gap:9px;min-width:150px;flex:0 0 auto}
.service-name .icon{width:16px;height:16px;color:var(--muted)}
.service-name span{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.service-detail{color:var(--muted);font-size:12px;text-align:right;min-width:0;flex:1;max-width:68%}
.status-pill{display:inline-flex;align-items:center;gap:6px;border:1px solid #34404c;border-radius:999px;padding:3px 8px;font-size:11px;color:var(--muted);white-space:nowrap}
.status-pill.ok{border-color:rgba(57,213,176,.35);background:rgba(57,213,176,.08);color:var(--accent-2)}
.status-pill.warn{border-color:rgba(244,184,96,.35);background:rgba(244,184,96,.08);color:#ffd294}
.section-head{display:flex;align-items:center;justify-content:space-between;gap:16px;margin:4px 0 14px}
.section-head h2{font-size:18px;margin:0}
.section-head p{color:var(--muted);margin:5px 0 0}
.table-wrap{overflow:auto;border:1px solid var(--line);border-radius:var(--radius);background:var(--panel)}
table{width:100%;border-collapse:collapse;min-width:720px}
th{text-align:left;color:var(--faint);font-size:11px;font-weight:650;text-transform:uppercase;letter-spacing:.07em;padding:11px 14px;background:#11161d;border-bottom:1px solid var(--line)}
td{padding:13px 14px;border-bottom:1px solid var(--line-soft);vertical-align:middle}
tr:last-child td{border-bottom:0}
tbody tr:hover{background:#161c24}
.link-route{display:flex;align-items:center;gap:9px;white-space:nowrap}
.link-route code,.mono{font:12px ui-monospace,SFMono-Regular,Consolas,monospace}
.link-route code{color:var(--accent-2)}
.route-arrow{color:var(--faint)}
.target strong{display:block;font-weight:600}
.target small{display:block;color:var(--muted);margin-top:3px}
.table-actions{display:flex;justify-content:flex-end;gap:6px}
.empty{border:1px dashed #33404d;border-radius:var(--radius);padding:38px 20px;text-align:center;color:var(--muted)}
.empty strong{display:block;color:var(--text);margin-bottom:6px}
.access-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}
.access-card{border:1px solid var(--line);background:var(--panel);border-radius:var(--radius);padding:18px}
.access-icon{width:38px;height:38px;border-radius:8px;background:#1b252e;color:var(--accent);display:grid;place-items:center;margin-bottom:15px}
.access-icon .icon{width:20px;height:20px}
.access-card h3{font-size:14px;margin:0 0 7px}
.access-card p{color:var(--muted);font-size:12px;line-height:1.55;margin:0 0 16px;min-height:55px}
.access-card .btn{width:100%}
.authorization{border:1px solid var(--line);background:var(--panel);border-radius:var(--radius);padding:20px;margin-top:14px}
.authorization-head{display:flex;align-items:flex-start;justify-content:space-between;gap:20px}
.authorization h2{font-size:16px;margin:0}
.authorization p{color:var(--muted);line-height:1.55;margin:7px 0 0;max-width:720px}
.auth-points{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;margin-top:18px}
.auth-point{border-top:1px solid var(--line);padding-top:13px}
.auth-point strong{display:block;font-size:12px}
.auth-point span{display:block;color:var(--muted);font-size:11px;line-height:1.5;margin-top:5px}
.diagnostic-box{background:#0d1116;border:1px solid var(--line);border-radius:var(--radius);padding:16px;overflow:auto;max-height:520px;color:#c7d5e2;font:12px/1.65 ui-monospace,SFMono-Regular,Consolas,monospace;white-space:pre-wrap;overflow-wrap:anywhere}
.automation-panel{border:1px solid var(--line);background:var(--panel);border-radius:var(--radius);padding:20px;margin-bottom:14px}
.automation-panel h2{font-size:16px;margin:0}
.automation-panel p{color:var(--muted);line-height:1.55;margin:7px 0 0}
.automation-endpoint{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:16px;padding:12px 13px;border:1px solid var(--line);border-radius:7px;background:#0d1218}
.automation-endpoint code{color:var(--accent-2);font:12px ui-monospace,SFMono-Regular,Consolas,monospace;overflow-wrap:anywhere}
.automation-tools{display:grid;gap:8px;margin-top:14px}
.automation-tool{display:grid;grid-template-columns:auto minmax(0,1fr);gap:10px;padding:11px 12px;border:1px solid var(--line-soft);border-radius:7px;background:#10161e}
.automation-tool code{color:var(--accent-2);font:12px ui-monospace,SFMono-Regular,Consolas,monospace}
.automation-tool span{color:var(--muted);font-size:12px;text-align:right}
.muted{color:var(--muted)}
.dialog{border:1px solid #364250;border-radius:9px;background:#151b23;color:var(--text);padding:0;width:min(480px,calc(100vw - 32px));box-shadow:0 24px 80px rgba(0,0,0,.55)}
.dialog::backdrop{background:rgba(3,6,9,.72);backdrop-filter:blur(3px)}
.dialog-head{display:flex;align-items:flex-start;justify-content:space-between;padding:18px 18px 12px}
.dialog-head h2{font-size:16px;margin:0}
.dialog-head p{margin:5px 0 0;color:var(--muted);font-size:12px}
.form{display:grid;gap:13px;padding:4px 18px 18px}
.field{display:grid;gap:6px}
.field label{color:var(--muted);font-size:12px}
.field input,.field select{width:100%;height:39px;border:1px solid var(--line);border-radius:7px;background:#0f141a;color:var(--text);padding:0 10px;outline:0}
.field input:focus,.field select:focus{border-color:var(--accent);box-shadow:0 0 0 2px rgba(57,213,176,.12)}
.field-row{display:grid;grid-template-columns:1fr 1fr;gap:10px}
.form-error{color:#ffb3b3;font-size:12px;min-height:16px}
.form-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:2px}
.toast{position:fixed;right:22px;bottom:20px;max-width:min(420px,calc(100vw - 40px));background:#19232b;border:1px solid #36534e;color:#dffbf3;padding:11px 14px;border-radius:7px;box-shadow:0 14px 45px rgba(0,0,0,.4);opacity:0;transform:translateY(8px);pointer-events:none;transition:.18s}
.toast.show{opacity:1;transform:translateY(0)}
.icon{width:18px;height:18px;fill:none;stroke:currentColor;stroke-width:2;stroke-linecap:round;stroke-linejoin:round}
.spin{animation:spin .8s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
@media(max-width:1080px){
  .app{grid-template-columns:210px minmax(0,1fr)}
  .main{padding:22px}
  .metrics{grid-template-columns:repeat(2,minmax(0,1fr))}
  .grid-2{grid-template-columns:1fr}
  .access-grid{grid-template-columns:1fr}
  .access-card p{min-height:0}
}
@media(max-width:760px){
  .app{display:block}
  .sidebar{min-height:auto;border-right:0;border-bottom:1px solid var(--line);padding:14px}
  .brand{padding-bottom:14px}
  .nav{display:flex;overflow:auto;padding-bottom:2px}
  .nav button{width:auto;white-space:nowrap;padding:8px 10px}
  .side-node{display:none}
  .main{padding:18px 14px 34px}
  .topbar{align-items:flex-start;margin-bottom:18px}
  .topbar p{display:none}
  .overview-strip{align-items:flex-start;flex-direction:column}
  .endpoint{text-align:left}
  .metrics,.grid-2,.auth-points{grid-template-columns:1fr}
  .grid-2{margin-top:12px}
  .service-row{align-items:flex-start;flex-direction:column;gap:7px}
  .service-detail{max-width:100%;text-align:left}
}
</style>
</head>
<body>
<svg width="0" height="0" style="position:absolute">
  <symbol id="i-grid" viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/></symbol>
  <symbol id="i-link" viewBox="0 0 24 24"><path d="M10 13a5 5 0 0 0 7.5.5l2-2a5 5 0 0 0-7-7l-1.1 1"/><path d="M14 11a5 5 0 0 0-7.5-.5l-2 2a5 5 0 0 0 7 7l1.1-1"/></symbol>
  <symbol id="i-users" viewBox="0 0 24 24"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></symbol>
  <symbol id="i-shield" viewBox="0 0 24 24"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z"/><path d="m9 12 2 2 4-4"/></symbol>
  <symbol id="i-terminal" viewBox="0 0 24 24"><polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/></symbol>
  <symbol id="i-monitor" viewBox="0 0 24 24"><rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></symbol>
  <symbol id="i-refresh" viewBox="0 0 24 24"><path d="M20 11a8.1 8.1 0 0 0-15.5-2M4 4v5h5"/><path d="M4 13a8.1 8.1 0 0 0 15.5 2M20 20v-5h-5"/></symbol>
  <symbol id="i-external" viewBox="0 0 24 24"><path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/></symbol>
  <symbol id="i-plus" viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></symbol>
  <symbol id="i-copy" viewBox="0 0 24 24"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></symbol>
  <symbol id="i-trash" viewBox="0 0 24 24"><path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M19 6l-1 15H6L5 6"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/></symbol>
  <symbol id="i-activity" viewBox="0 0 24 24"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></symbol>
  <symbol id="i-server" viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></symbol>
  <symbol id="i-folder" viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z"/></symbol>
  <symbol id="i-file" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z"/><polyline points="14 2 14 8 20 8"/></symbol>
  <symbol id="i-code" viewBox="0 0 24 24"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></symbol>
  <symbol id="i-close" viewBox="0 0 24 24"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></symbol>
</svg>
<div class="app">
  <aside class="sidebar">
    <div class="brand">
      <div class="brand-mark">W</div>
      <div class="brand-copy"><strong>WarpMesh</strong><span>Node console</span></div>
    </div>
    <nav class="nav" aria-label="Client sections">
      <button class="active" data-view="overview"><svg class="icon"><use href="#i-grid"></use></svg>Overview</button>
      <button data-view="links"><svg class="icon"><use href="#i-link"></use></svg>Links</button>
      <button data-view="peers"><svg class="icon"><use href="#i-users"></use></svg>Peers</button>
      <button data-view="access"><svg class="icon"><use href="#i-shield"></use></svg>Access</button>
      <button data-view="automation"><svg class="icon"><use href="#i-code"></use></svg>Automation</button>
      <button data-view="diagnostics"><svg class="icon"><use href="#i-activity"></use></svg>Diagnostics</button>
    </nav>
    <div class="side-node">
      <div class="side-node-label">This device</div>
      <div class="side-node-name" id="side-name">Connecting...</div>
      <div class="side-node-meta" id="side-id">Local node</div>
      <div class="state" id="side-state"><span class="state-dot"></span><span id="side-state-text">Checking server</span></div>
    </div>
  </aside>
  <main class="main">
    <header class="topbar">
      <div>
        <div class="eyebrow">Local node</div>
        <h1 id="page-title">Overview</h1>
        <p id="page-subtitle">Connection health and access entry points</p>
      </div>
      <div class="toolbar">
        <button class="icon-btn" id="refresh" title="Refresh local state" aria-label="Refresh local state"><svg class="icon"><use href="#i-refresh"></use></svg></button>
        <button class="btn" id="console-top"><svg class="icon"><use href="#i-server"></use></svg>Control plane</button>
      </div>
    </header>
    <div class="notice" id="notice" role="alert"></div>

    <section class="view active" data-panel="overview">
      <div class="overview-strip">
        <div class="node-title">
          <div class="node-glyph"><svg class="icon"><use href="#i-monitor"></use></svg></div>
          <div><strong id="node-name">Loading device...</strong><small id="node-meta">Reading local node state</small></div>
        </div>
        <div class="endpoint"><span>Control plane</span><code id="console-url">Not configured</code></div>
      </div>
      <div class="metrics">
        <div class="metric"><div class="metric-head"><span>Server path</span><svg class="icon"><use href="#i-activity"></use></svg></div><strong id="metric-connection">Checking</strong><small id="metric-server">-</small></div>
        <div class="metric"><div class="metric-head"><span>Active links</span><svg class="icon"><use href="#i-link"></use></svg></div><strong id="metric-active">0</strong><small id="metric-links">0 configured</small></div>
        <div class="metric"><div class="metric-head"><span>Uptime</span><svg class="icon"><use href="#i-server"></use></svg></div><strong id="metric-uptime">-</strong><small>Local node process</small></div>
        <div class="metric"><div class="metric-head"><span>Desktop</span><svg class="icon"><use href="#i-monitor"></use></svg></div><strong id="metric-desktop">Checking</strong><small id="metric-vnc">VNC status</small></div>
      </div>
      <div class="grid-2">
        <div class="panel">
          <div class="panel-head"><div><h2>Remote access</h2><p>Services exposed by this node to authorized operators</p></div><button class="btn small" id="manage-access">Manage access</button></div>
          <div class="panel-body service-list" id="service-list"></div>
        </div>
        <div class="panel">
          <div class="panel-head"><div><h2>Node details</h2><p>Identity and runtime reported by the node service</p></div></div>
          <div class="panel-body details-list" id="node-details"></div>
        </div>
      </div>
      <div class="panel" style="margin-top:14px">
        <div class="panel-head"><div><h2>Recent links</h2><p>Local TCP listeners mapped to remote peers</p></div><button class="btn small" data-go="links">View all</button></div>
        <div class="panel-body" id="recent-links"></div>
      </div>
    </section>

    <section class="view" data-panel="links">
      <div class="section-head"><div><h2>TCP links</h2><p>Expose a local loopback port to a port on another device.</p></div><button class="btn primary" id="add-link"><svg class="icon"><use href="#i-plus"></use></svg>New link</button></div>
      <div class="table-wrap"><table><thead><tr><th>Local endpoint</th><th>Peer endpoint</th><th>Runtime</th><th>Last activity</th><th></th></tr></thead><tbody id="link-rows"></tbody></table></div>
      <div class="empty" id="links-empty" style="display:none"><strong>No links configured</strong>Create a local port to connect to SSH, RDP, databases, or any TCP service on a peer device.</div>
    </section>

    <section class="view" data-panel="peers">
      <div class="section-head"><div><h2>Linked peers</h2><p>The remote device IDs currently referenced by this node's links.</p></div></div>
      <div class="table-wrap"><table><thead><tr><th>Peer device</th><th>Local ports</th><th>Target ports</th><th>Links</th><th>Active</th></tr></thead><tbody id="peer-rows"></tbody></table></div>
      <div class="empty" id="peers-empty" style="display:none"><strong>No linked peers</strong>Add a TCP link to see the peer endpoint and its port mapping here.</div>
    </section>

    <section class="view" data-panel="access">
      <div class="section-head"><div><h2>Access and authorization</h2><p>Authorization is enforced by the control plane, not by browser visibility.</p></div></div>
      <div class="access-grid">
        <article class="access-card"><div class="access-icon"><svg class="icon"><use href="#i-terminal"></use></svg></div><h3>Remote terminal</h3><p>ConPTY terminal access for this device. Authorized administrators and operators can open it from the control plane.</p><button class="btn" data-console="/#/terminal/DEVICE"><svg class="icon"><use href="#i-external"></use></svg>Open terminal console</button></article>
        <article class="access-card"><div class="access-icon"><svg class="icon"><use href="#i-monitor"></use></svg></div><h3>Remote desktop</h3><p>VNC-backed desktop access. The control plane checks operator device grants before the session starts.</p><button class="btn" data-console="/#/desktop/DEVICE"><svg class="icon"><use href="#i-external"></use></svg>Open desktop console</button></article>
        <article class="access-card"><div class="access-icon"><svg class="icon"><use href="#i-shield"></use></svg></div><h3>Operator access</h3><p>Review accounts, per-device grants, forwarding policy, and link grants in the server Access page.</p><button class="btn" data-console="/access"><svg class="icon"><use href="#i-external"></use></svg>Manage authorization</button></article>
      </div>
      <div class="authorization">
        <div class="authorization-head"><div><h2>Authorization model</h2><p>This client displays local readiness and endpoint mappings. Credentials and policies stay on the server so a local browser cannot grant itself access.</p></div><span class="status-pill ok">Server enforced</span></div>
        <div class="auth-points">
          <div class="auth-point"><strong>Terminal</strong><span>The server checks the operator session and the target device grant before opening ConPTY.</span></div>
          <div class="auth-point"><strong>Desktop</strong><span>The server checks the same device grant before bridging the VNC WebSocket.</span></div>
          <div class="auth-point"><strong>Device links</strong><span>Outbound forwarding follows each source device's policy and explicit target/port grants.</span></div>
        </div>
      </div>
    </section>

    <section class="view" data-panel="diagnostics">
      <div class="section-head"><div><h2>Diagnostics</h2><p>Local runtime details for troubleshooting connection and service availability.</p></div><button class="btn" id="copy-diagnostics"><svg class="icon"><use href="#i-copy"></use></svg>Copy JSON</button></div>
      <pre class="diagnostic-box" id="diagnostics">Loading...</pre>
    </section>

    <section class="view" data-panel="automation">
      <div class="section-head"><div><h2>Automation API</h2><p>Every node operation is also available to local AI clients as typed HTTP and MCP tools.</p></div><span class="status-pill ok">Bearer protected</span></div>
      <div class="automation-panel">
        <h2>Stable HTTP API</h2>
        <p>Local loopback only. Mutating and read operations require the owner-readable UI token from the node data directory.</p>
        <div class="automation-endpoint"><code id="api-base">/api/v1</code><button class="icon-btn" id="copy-api-base" title="Copy API base URL"><svg class="icon"><use href="#i-copy"></use></svg></button></div>
      </div>
      <div class="automation-panel">
        <h2>MCP over stdio</h2>
        <p>Register the node executable as an MCP server. The client discovers the same tools and read-only resources as the HTTP API.</p>
        <div class="automation-endpoint"><code id="mcp-command">warpmesh-node mcp -data-dir &lt;data-dir&gt;</code><button class="icon-btn" id="copy-mcp-command" title="Copy MCP command"><svg class="icon"><use href="#i-copy"></use></svg></button></div>
      </div>
      <div class="automation-panel">
        <h2>Tool surface</h2>
        <p>Tool definitions include JSON Schema input contracts and safety annotations.</p>
        <div class="automation-tools" id="automation-tools"><div class="muted">Loading capabilities…</div></div>
      </div>
    </section>
  </main>
</div>

<dialog class="dialog" id="link-dialog">
  <div class="dialog-head"><div><h2>New TCP link</h2><p>The listener binds only to 127.0.0.1.</p></div><button class="icon-btn" type="button" id="close-dialog" aria-label="Close"><svg class="icon"><use href="#i-close"></use></svg></button></div>
  <form class="form" id="link-form">
    <div class="field-row">
      <div class="field"><label for="local-port">Local port</label><input id="local-port" type="number" min="1" max="65535" placeholder="2222" required></div>
      <div class="field"><label for="target-port">Target port</label><input id="target-port" type="number" min="1" max="65535" placeholder="22" required></div>
    </div>
    <div class="field"><label for="target-device">Target device ID</label><input id="target-device" placeholder="dev-xxxxxxxxxxxxxxxx" required></div>
    <div class="form-error" id="form-error" role="alert"></div>
    <div class="form-actions"><button class="btn" type="button" id="cancel-dialog">Cancel</button><button class="btn primary" type="submit" id="save-link">Create link</button></div>
  </form>
</dialog>
<div class="toast" id="toast" role="status"></div>

<script>
const state={overview:null,forwards:[],peers:[],diagnostics:null,view:"overview",loading:false};
const titles={
  overview:["Overview","Connection health and access entry points"],
  links:["Links","Manage local TCP listeners and their remote endpoints"],
  peers:["Peers","Inspect remote device mappings used by local links"],
  access:["Access","Terminal, desktop, and authorization entry points"],
  automation:["Automation","Typed HTTP and MCP control for local AI clients"],
  diagnostics:["Diagnostics","Local runtime and connection details"]
};

function byId(id){return document.getElementById(id)}
function svgIcon(name){
  const svg=document.createElementNS("http://www.w3.org/2000/svg","svg");
  svg.setAttribute("class","icon");
  const use=document.createElementNS("http://www.w3.org/2000/svg","use");
  use.setAttribute("href","#i-"+name);
  svg.appendChild(use);
  return svg;
}
function button(label,icon,className,onClick){
  const b=document.createElement("button");
  b.className=className||"btn small";
  if(icon)b.appendChild(svgIcon(icon));
  if(label)b.appendChild(document.createTextNode(label));
  b.addEventListener("click",onClick);
  return b;
}
function cell(text,className){
  const td=document.createElement("td");
  if(className)td.className=className;
  td.textContent=text;
  return td;
}
function pill(text,ok){const span=document.createElement("span");span.className="status-pill "+(ok?"ok":"warn");span.textContent=text;return span}
function formatUptime(seconds){
  if(!Number.isFinite(seconds)||seconds<0)return "-";
  if(seconds<60)return Math.floor(seconds)+"s";
  if(seconds<3600)return Math.floor(seconds/60)+"m";
  const hours=Math.floor(seconds/3600);
  const mins=Math.floor((seconds%3600)/60);
  return hours+"h "+mins+"m";
}
function formatTime(value){
  if(!value)return "Never";
  const date=new Date(value);
  return Number.isNaN(date.getTime())?"Unknown":date.toLocaleString();
}
async function fetchJSON(path,options){
  const response=await fetch(path,options);
  const body=await response.json().catch(function(){return null});
  if(!response.ok)throw new Error(body&&body.error?body.error:"Request failed ("+response.status+")");
  return body;
}
function showNotice(message){
  const notice=byId("notice");
  notice.textContent=message||"";
  notice.classList.toggle("show",!!message);
}
function toast(message){
  const node=byId("toast");
  node.textContent=message;
  node.classList.add("show");
  clearTimeout(node.hideTimer);
  node.hideTimer=setTimeout(function(){node.classList.remove("show")},2200);
}
function setView(view){
  state.view=view;
  document.querySelectorAll(".nav button").forEach(function(btn){btn.classList.toggle("active",btn.dataset.view===view)});
  document.querySelectorAll(".view").forEach(function(panel){panel.classList.toggle("active",panel.dataset.panel===view)});
  byId("page-title").textContent=titles[view][0];
  byId("page-subtitle").textContent=titles[view][1];
  if(view==="automation")loadAutomation();
  if(view==="diagnostics")loadDiagnostics();
}
function consolePath(path){
  if(!state.overview||!state.overview.consoleUrl){toast("Control plane URL is unavailable");return}
  const finalPath=(path||"/").replace("DEVICE",encodeURIComponent(state.overview.deviceId));
  location.assign(state.overview.consoleUrl+finalPath);
}
async function copyText(text,message){
  try{
    await navigator.clipboard.writeText(text);
    toast(message||"Copied");
  }catch(error){
    toast("Clipboard access is unavailable");
  }
}

function renderOverview(){
  const data=state.overview;
  if(!data)return;
  byId("side-name").textContent=data.deviceName||data.deviceId;
  byId("side-id").textContent=data.os+"/"+data.arch+" · "+(data.deviceId||"unknown");
  byId("side-state").className="state "+(data.connected?"online":"offline");
  byId("side-state-text").textContent=data.connected?"Connected":"Reconnecting";
  byId("node-name").textContent=data.deviceName||data.deviceId;
  byId("node-meta").textContent=data.deviceId+" · "+data.os+"/"+data.arch;
  byId("console-url").textContent=data.consoleUrl||"Not configured";
  byId("metric-connection").textContent=data.connected?"Connected":"Reconnecting";
  byId("metric-server").textContent=data.serverUrl||"Server URL unavailable";
  byId("metric-active").textContent=String(data.activeForwards||0);
  byId("metric-links").textContent=(data.forwardCount||0)+" configured";
  byId("metric-uptime").textContent=formatUptime(data.uptimeSeconds);
  byId("metric-desktop").textContent=data.desktop?"Ready":"Setup needed";
  byId("metric-vnc").textContent="VNC port "+data.vncPort;

  const services=byId("service-list");services.replaceChildren();
  [
    ["terminal","Remote terminal",data.terminal,"ConPTY","Open console"],
    ["monitor","Remote desktop",data.desktop,data.desktopHint,"Open console"],
    ["folder","File manager",data.files,"Allowed roots: "+((data.allowedPaths||[]).join(", ")||"home"),"Open console"]
  ].forEach(function(item){
    const row=document.createElement("div");row.className="service-row";
    const name=document.createElement("div");name.className="service-name";name.appendChild(svgIcon(item[0]));
    const label=document.createElement("span");label.textContent=item[1];name.appendChild(label);row.appendChild(name);
    const detail=document.createElement("div");detail.className="service-detail";
    detail.appendChild(pill(item[2]?"Available":"Unavailable",!!item[2]));
    const small=document.createElement("div");small.style.marginTop="5px";small.textContent=item[3];detail.appendChild(small);
    row.appendChild(detail);services.appendChild(row);
  });

  const details=byId("node-details");details.replaceChildren();
  [
    ["Hostname",data.hostname||"Not reported"],
    ["Platform",data.os+"/"+data.arch],
    ["Control plane",data.consoleUrl||"Not configured"],
    ["VNC endpoint","127.0.0.1:"+data.vncPort],
    ["Node uptime",formatUptime(data.uptimeSeconds)]
  ].forEach(function(item){
    const row=document.createElement("div");row.className="detail-row";
    const key=document.createElement("span");key.className="muted";key.textContent=item[0];
    const value=document.createElement("span");value.textContent=item[1];value.style.textAlign="right";value.style.overflowWrap="anywhere";
    row.append(key,value);details.appendChild(row);
  });
  renderRecentLinks();
  applyConsoleButtons();
}
function linkRow(forward){
  const tr=document.createElement("tr");
  const local=document.createElement("td");
  const route=document.createElement("div");route.className="link-route";
  const code=document.createElement("code");code.textContent="127.0.0.1:"+forward.localPort;
  route.append(code,document.createTextNode(" →"));
  local.appendChild(route);tr.appendChild(local);
  const target=document.createElement("td");target.className="target";
  const peer=document.createElement("strong");peer.textContent=forward.targetDeviceId;target.appendChild(peer);
  const endpoint=document.createElement("small");endpoint.textContent="port "+forward.targetPort;target.appendChild(endpoint);tr.appendChild(target);
  const runtime=document.createElement("td");runtime.appendChild(pill(forward.active?forward.active+" active":"Idle",forward.active>0));tr.appendChild(runtime);
  tr.appendChild(cell(formatTime(forward.lastActivity),"muted"));
  const actions=document.createElement("td");const wrap=document.createElement("div");wrap.className="table-actions";
  wrap.appendChild(button("","copy","icon-btn small",function(){copyText("127.0.0.1:"+forward.localPort,"Local endpoint copied")}));
  const remove=button("","trash","icon-btn small danger",async function(){
    if(!confirm("Remove the link on 127.0.0.1:"+forward.localPort+"?"))return;
    try{await fetchJSON("/api/forwards/"+forward.localPort,{method:"DELETE"});toast("Link removed");await loadAll()}catch(error){showNotice(error.message)}
  });
  remove.title="Remove link";wrap.appendChild(remove);actions.appendChild(wrap);tr.appendChild(actions);
  return tr;
}
function renderLinks(){
  const rows=byId("link-rows");rows.replaceChildren();
  state.forwards.forEach(function(forward){rows.appendChild(linkRow(forward))});
  byId("links-empty").style.display=state.forwards.length?"none":"block";
  const recent=byId("recent-links");recent.replaceChildren();
  if(!state.forwards.length){
    const empty=document.createElement("div");empty.className="empty";empty.textContent="No links configured.";
    recent.appendChild(empty);
  }else{
    const wrap=document.createElement("div");wrap.className="service-list";
    state.forwards.slice(0,4).forEach(function(forward){
      const row=document.createElement("div");row.className="service-row";
      const name=document.createElement("div");name.className="service-name";name.appendChild(svgIcon("link"));
      const label=document.createElement("span");label.textContent="127.0.0.1:"+forward.localPort+" → "+forward.targetDeviceId+":"+forward.targetPort;name.appendChild(label);
      const status=pill(forward.active?forward.active+" active":"Idle",forward.active>0);
      row.append(name,status);wrap.appendChild(row);
    });
    recent.appendChild(wrap);
  }
}
function renderRecentLinks(){renderLinks()}
function renderPeers(){
  const rows=byId("peer-rows");rows.replaceChildren();
  state.peers.forEach(function(peer){
    const tr=document.createElement("tr");
    tr.appendChild(cell(peer.deviceId));
    tr.appendChild(cell((peer.localPorts||[]).join(", "),"mono"));
    tr.appendChild(cell((peer.targetPorts||[]).join(", "),"mono"));
    tr.appendChild(cell(String(peer.forwardCount||0)));
    const active=document.createElement("td");active.appendChild(pill(peer.active?String(peer.active):"None",peer.active>0));tr.appendChild(active);
    rows.appendChild(tr);
  });
  byId("peers-empty").style.display=state.peers.length?"none":"block";
}
function applyConsoleButtons(){
  document.querySelectorAll("[data-console]").forEach(function(btn){
    btn.onclick=function(){consolePath(btn.dataset.console)};
    if(btn.dataset.console.indexOf("DEVICE")>=0)btn.disabled=!state.overview;
  });
}
async function loadDiagnostics(){
  try{
    state.diagnostics=await fetchJSON("/api/diagnostics");
    byId("diagnostics").textContent=JSON.stringify(state.diagnostics,null,2);
  }catch(error){byId("diagnostics").textContent="Unable to load diagnostics: "+error.message}
}
async function loadAutomation(){
  try{
    const data=await fetchJSON("/api/v1/capabilities");
    const host=location.host;
    byId("api-base").textContent="http://"+host+"/api/v1";
    const list=byId("automation-tools");list.replaceChildren();
    for(const tool of data.tools||[]){
      const row=document.createElement("div");row.className="automation-tool";
      const name=document.createElement("code");name.textContent=tool.name;
      const details=document.createElement("span");details.textContent=(tool.method||"")+" "+(tool.path||"")+" · "+(tool.description||"");
      row.append(name,details);list.appendChild(row);
    }
  }catch(error){
    byId("automation-tools").textContent="Unable to load automation capabilities: "+error.message;
  }
}
async function loadAll(){
  if(state.loading)return;
  state.loading=true;
  byId("refresh").classList.add("spin");
  try{
    const values=await Promise.all([fetchJSON("/api/overview"),fetchJSON("/api/forwards"),fetchJSON("/api/peers")]);
    state.overview=values[0];state.forwards=values[1].forwards||[];state.peers=values[2]||[];
    showNotice("");
    renderOverview();renderPeers();
    if(state.view==="diagnostics")await loadDiagnostics();
  }catch(error){
    byId("side-state").className="state offline";
    byId("side-state-text").textContent="Node unavailable";
    showNotice("Local node state is unavailable: "+error.message);
  }finally{
    state.loading=false;byId("refresh").classList.remove("spin");
  }
}
function openDialog(){
  byId("form-error").textContent="";
  byId("link-form").reset();
  const dialog=byId("link-dialog");
  if(dialog.showModal)dialog.showModal();else dialog.setAttribute("open","");
}
function closeDialog(){const dialog=byId("link-dialog");if(dialog.close)dialog.close();else dialog.removeAttribute("open")}

document.querySelectorAll(".nav button").forEach(function(btn){btn.addEventListener("click",function(){setView(btn.dataset.view)})});
document.querySelectorAll("[data-go]").forEach(function(btn){btn.addEventListener("click",function(){setView(btn.dataset.go)})});
byId("refresh").addEventListener("click",loadAll);
byId("console-top").addEventListener("click",function(){consolePath("/")});
byId("manage-access").addEventListener("click",function(){consolePath("/access")});
byId("copy-api-base").addEventListener("click",function(){copyText(byId("api-base").textContent,"API base URL copied")});
byId("copy-mcp-command").addEventListener("click",function(){copyText(byId("mcp-command").textContent,"MCP command copied")});
byId("add-link").addEventListener("click",openDialog);
byId("close-dialog").addEventListener("click",closeDialog);
byId("cancel-dialog").addEventListener("click",closeDialog);
byId("copy-diagnostics").addEventListener("click",function(){copyText(byId("diagnostics").textContent,"Diagnostics copied")});
byId("link-form").addEventListener("submit",async function(event){
  event.preventDefault();
  const payload={
    localPort:Number(byId("local-port").value),
    targetDeviceId:byId("target-device").value.trim(),
    targetPort:Number(byId("target-port").value)
  };
  const error=byId("form-error");error.textContent="";
  if(!payload.targetDeviceId){error.textContent="Target device ID is required.";return}
  byId("save-link").disabled=true;
  try{
    await fetchJSON("/api/forwards",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(payload)});
    closeDialog();toast("Link created");await loadAll();setView("links");
  }catch(failure){error.textContent=failure.message}
  finally{byId("save-link").disabled=false}
});
loadAll();
loadAutomation();
setInterval(loadAll,5000);
</script>
</body>
</html>`
