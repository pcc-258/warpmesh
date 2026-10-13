package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (a *Agent) startLocalUI() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.uiIndex)
	mux.HandleFunc("/api/forwards", a.uiForwards)
	mux.HandleFunc("/api/forwards/", a.uiForwardByPort)
	go func() {
		addr := fmt.Sprintf("127.0.0.1:%d", a.cfg.UIPort)
		log.Printf("local ui: http://%s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("local ui stopped: %v", err)
		}
	}()
}

func (a *Agent) uiIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(uiPage))
}

func (a *Agent) uiForwards(w http.ResponseWriter, r *http.Request) {
	if !localUIRequest(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeLocalJSON(w, http.StatusOK, map[string]any{
			"device":   a.cfg.DeviceID,
			"forwards": a.ListForwards(),
		})
	case http.MethodPost:
		var req ForwardSpec
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeLocalJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := a.StartForward(req); err != nil {
			writeLocalJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		writeLocalJSON(w, http.StatusCreated, req)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *Agent) uiForwardByPort(w http.ResponseWriter, r *http.Request) {
	if !localUIRequest(w, r) {
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, "/api/forwards/")
	port, err := strconv.Atoi(raw)
	if err != nil {
		writeLocalJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid port"})
		return
	}
	if err := a.StopForward(port); err != nil {
		writeLocalJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Loopback binding alone does not prevent browser CSRF or DNS rebinding.
// Origin-less clients remain supported, but browser origins must match exactly.
func localUIRequest(w http.ResponseWriter, r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	allowed := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		allowed = allowed && err == nil && u.Scheme == "http" && u.Host == r.Host && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		allowed = false
	}
	if !allowed {
		writeLocalJSON(w, http.StatusForbidden, map[string]any{"error": "local same-origin requests only"})
	}
	return allowed
}

func writeLocalJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const uiPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>WarpMesh Agent</title>
<style>
body{background:#0f1216;color:#e8eef4;font-family:system-ui,sans-serif;margin:0;padding:24px}
.card{background:#171d24;border:1px solid #2a333e;border-radius:8px;padding:18px;max-width:640px;margin:0 auto}
h1{margin:0 0 6px;font-size:20px}
p{color:#97a5b3;margin:0 0 16px}
label{display:block;margin:10px 0 4px;color:#97a5b3;font-size:13px}
input{width:100%;padding:9px 10px;border:1px solid #2a333e;border-radius:6px;background:#1d242d;color:#e8eef4}
button{margin-top:14px;padding:10px 14px;border:0;border-radius:6px;background:#2dd4bf;color:#06231f;font-weight:600}
.row{display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid #2a333e;padding:10px 0}
.row small{color:#97a5b3}
a{color:#2dd4bf}
.del{background:#f87171;color:#1a0b0b;margin:0;padding:6px 10px}
</style>
</head>
<body>
<div class="card">
<h1>WarpMesh Agent</h1>
<p id="meta">loading...</p>
<form id="f">
<label>Local port</label><input id="port" type="number" placeholder="2222">
<label>Target device id</label><input id="target" placeholder="dev-xxxxxxxx">
<label>Target port</label><input id="tport" type="number" placeholder="22">
<button type="submit">Add forward</button>
</form>
<div id="list"></div>
<p><a id="console" target="_blank">Open web console</a></p>
</div>
<script>
async function load(){
  const r=await fetch('/api/forwards'); const data=await r.json();
  document.getElementById('meta').textContent='device: '+data.device;
  const list=document.getElementById('list'); list.replaceChildren();
  for(const f of data.forwards ?? []){
    const row=document.createElement('div'); row.className='row';
    const info=document.createElement('div');
    const port=document.createElement('strong'); port.textContent='127.0.0.1:'+f.localPort;
    const target=document.createElement('small'); target.textContent=f.targetDeviceId+':'+f.targetPort;
    info.append(port,document.createElement('br'),target); row.appendChild(info);
    const btn=document.createElement('button'); btn.className='del'; btn.textContent='Delete'; btn.onclick=async()=>{await fetch('/api/forwards/'+f.localPort,{method:'DELETE'});load();};
    row.appendChild(btn); list.appendChild(row);
  }
  document.getElementById('console').href=location.origin;
}
document.getElementById('f').onsubmit=async(e)=>{
  e.preventDefault();
  await fetch('/api/forwards',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({
    localPort:+document.getElementById('port').value,
    targetDeviceId:document.getElementById('target').value,
    targetPort:+document.getElementById('tport').value
  })});
  load();
};
load();
</script>
</body>
</html>`
