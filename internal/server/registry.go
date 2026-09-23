package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// Device is the persisted metadata for one managed device.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hostname  string    `json:"hostname"`
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	LANIPs    []string  `json:"lanIPs,omitempty"`
	Group     string    `json:"group,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	LastSeen  time.Time `json:"lastSeen"`
	Online    bool      `json:"online"`
}

// User is an operator account that can log into the console.
type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// DeviceKey is a per-device credential. Token is only present on create.
type DeviceKey struct {
	DeviceID  string    `json:"deviceId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	Token     string    `json:"token,omitempty"`
}

// Invite is a one-time enrollment code.
type Invite struct {
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// AuditEntry records a security-relevant event.
type AuditEntry struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"createdAt"`
}

const registrySchema = `
CREATE TABLE IF NOT EXISTS devices (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	hostname   TEXT NOT NULL,
	os         TEXT NOT NULL,
	arch       TEXT NOT NULL,
	lan_ips    TEXT NOT NULL DEFAULT '[]',
	group_name TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	last_seen  TEXT NOT NULL,
	online     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	username      TEXT UNIQUE NOT NULL,
	password_hash TEXT NOT NULL,
	role          TEXT NOT NULL DEFAULT 'admin',
	created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS device_keys (
	device_id  TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL,
	name       TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS invite_codes (
	code       TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	actor      TEXT NOT NULL,
	action     TEXT NOT NULL,
	target     TEXT NOT NULL,
	detail     TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
`

// Registry keeps the device catalog and access metadata in SQLite.
type Registry struct {
	mu sync.RWMutex
	db *sql.DB
}

// NewRegistry opens the SQLite database and resets liveness state.
func NewRegistry(dataDir string) (*Registry, error) {
	dsn := ":memory:"
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, err
		}
		dsn = filepath.Join(dataDir, "devices.db")
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	r := &Registry{db: db}
	if _, err := db.Exec(registrySchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := r.ensureColumn("devices", "group_name", "group_name TEXT NOT NULL DEFAULT ''"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := r.ensureColumn("device_keys", "expires_at", "expires_at TEXT NOT NULL DEFAULT ''"); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Agents reconnect after a restart; nothing is online until it says hello.
	if _, err := db.Exec("UPDATE devices SET online = 0"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return r, nil
}

func (r *Registry) ensureColumn(table, column, ddl string) error {
	rows, err := r.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk); err != nil {
			return err
		}
		if name == column {
			found = true
		}
	}
	if found {
		return nil
	}
	_, err = r.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + ddl)
	return err
}

// Close releases the database handle.
func (r *Registry) Close() error {
	return r.db.Close()
}

// EnsureUser creates the initial operator account when it does not exist.
func (r *Registry) EnsureUser(username, password, role string) error {
	if username == "" || password == "" {
		return errors.New("username and password are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int
	if err := r.db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", username).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err = r.db.Exec("INSERT INTO users (username, password_hash, role, created_at) VALUES (?, ?, ?, ?)",
		username, string(hash), role, time.Now().Format(time.RFC3339Nano))
	return err
}

// ValidateUser checks credentials and returns the user.
func (r *Registry) ValidateUser(username, password string) (User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var u User
	var hash string
	if err := r.db.QueryRow("SELECT id, username, role, password_hash FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &u.Role, &hash); err != nil {
		return User{}, errors.New("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, errors.New("invalid credentials")
	}
	return u, nil
}

// Upsert registers metadata for a device and returns the stored record.
func (r *Registry) Upsert(id, name, hostname, osName, arch string, ips []string) (*Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	lanIPs, err := json.Marshal(ips)
	if err != nil {
		return nil, err
	}

	var existingName, existingGroup string
	err = r.db.QueryRow("SELECT name, group_name FROM devices WHERE id = ?", id).Scan(&existingName, &existingGroup)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = r.db.Exec(`
			INSERT INTO devices (id, name, hostname, os, arch, lan_ips, group_name, created_at, last_seen, online)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
			id, name, hostname, osName, arch, string(lanIPs), "", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		if name == "" {
			name = existingName
		}
		_, err = r.db.Exec(`
			UPDATE devices
			SET name = ?, hostname = ?, os = ?, arch = ?, lan_ips = ?, last_seen = ?, online = 1
			WHERE id = ?`,
			name, hostname, osName, arch, string(lanIPs), now.Format(time.RFC3339Nano), id,
		)
		if err != nil {
			return nil, err
		}
	}
	d, err := r.getLocked(id)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// SetOnline updates liveness for a device.
func (r *Registry) SetOnline(id string, online bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().Format(time.RFC3339Nano)
	_, err := r.db.Exec("UPDATE devices SET online = ?, last_seen = ? WHERE id = ?", boolInt(online), now, id)
	return err
}

// Get returns a copy of one device.
func (r *Registry) Get(id string) (Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, err := r.getLocked(id)
	if err != nil {
		return Device{}, false
	}
	return d, true
}

// List returns all devices sorted by name.
func (r *Registry) List() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rows, err := r.db.Query(`
		SELECT id, name, hostname, os, arch, lan_ips, group_name, created_at, last_seen, online
		FROM devices
		ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]Device, 0)
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			continue
		}
		out = append(out, d)
	}
	return out
}

// Rename updates the user-facing name of a device.
func (r *Registry) Rename(id, name string) error {
	if name == "" {
		return errors.New("name cannot be empty")
	}
	return r.updateField(id, "name", name)
}

// SetGroup assigns a device to a group.
func (r *Registry) SetGroup(id, group string) error {
	return r.updateField(id, "group_name", group)
}

func (r *Registry) updateField(id, column, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.db.Exec("UPDATE devices SET "+column+" = ? WHERE id = ?", value, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("device not found")
	}
	return nil
}

// Delete removes a device from the catalog.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.db.Exec("DELETE FROM devices WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("device not found")
	}
	return nil
}

// CreateDeviceKey issues a per-device credential and returns the plaintext
// token exactly once.
func (r *Registry) CreateDeviceKey(name string, ttl time.Duration) (DeviceKey, error) {
	now := time.Now()
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return DeviceKey{}, err
	}
	deviceID := "dev-" + hex.EncodeToString(idBytes)
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return DeviceKey{}, err
	}
	token := hex.EncodeToString(tokenBytes)
	hash := hashToken(token)
	expires := ""
	expiresTime := time.Time{}
	if ttl > 0 {
		expiresTime = now.Add(ttl)
		expires = expiresTime.Format(time.RFC3339Nano)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec("INSERT INTO device_keys (device_id, token_hash, name, created_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		deviceID, hash, name, now.Format(time.RFC3339Nano), expires)
	if err != nil {
		return DeviceKey{}, err
	}
	return DeviceKey{DeviceID: deviceID, Name: name, CreatedAt: now, ExpiresAt: expiresTime, Token: token}, nil
}

// ValidateDeviceKey checks a device id and token pair.
func (r *Registry) ValidateDeviceKey(deviceID, token string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var stored, expires string
	err := r.db.QueryRow("SELECT token_hash, expires_at FROM device_keys WHERE device_id = ?", deviceID).Scan(&stored, &expires)
	if err != nil {
		return false
	}
	if expires != "" {
		expiresTime, err := time.Parse(time.RFC3339Nano, expires)
		if err != nil || time.Now().After(expiresTime) {
			return false
		}
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(hashToken(token))) == 1
}

// ListDeviceKeys returns all issued device credentials.
func (r *Registry) ListDeviceKeys() []DeviceKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rows, err := r.db.Query("SELECT device_id, name, created_at, expires_at FROM device_keys ORDER BY created_at DESC")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]DeviceKey, 0)
	for rows.Next() {
		var k DeviceKey
		var created, expires string
		if err := rows.Scan(&k.DeviceID, &k.Name, &created, &expires); err != nil {
			continue
		}
		k.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		k.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
		out = append(out, k)
	}
	return out
}

// RotateDeviceKey issues a fresh token for an existing device.
func (r *Registry) RotateDeviceKey(deviceID string, ttl time.Duration) (DeviceKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var name, created string
	if err := r.db.QueryRow("SELECT name, created_at FROM device_keys WHERE device_id = ?", deviceID).Scan(&name, &created); err != nil {
		return DeviceKey{}, err
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return DeviceKey{}, err
	}
	token := hex.EncodeToString(tokenBytes)
	expires := ""
	expiresTime := time.Time{}
	if ttl > 0 {
		expiresTime = time.Now().Add(ttl)
		expires = expiresTime.Format(time.RFC3339Nano)
	}
	if _, err := r.db.Exec("UPDATE device_keys SET token_hash = ?, expires_at = ? WHERE device_id = ?", hashToken(token), expires, deviceID); err != nil {
		return DeviceKey{}, err
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, created)
	return DeviceKey{DeviceID: deviceID, Name: name, CreatedAt: createdAt, ExpiresAt: expiresTime, Token: token}, nil
}

// CreateInvite issues a one-time enrollment code.
func (r *Registry) CreateInvite(name string, ttl time.Duration) (Invite, error) {
	now := time.Now()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return Invite{}, err
	}
	code := "inv-" + hex.EncodeToString(b)
	expires := now.Add(ttl)
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec("INSERT INTO invite_codes (code, name, expires_at, created_at) VALUES (?, ?, ?, ?)",
		code, name, expires.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return Invite{}, err
	}
	return Invite{Code: code, Name: name, ExpiresAt: expires, CreatedAt: now}, nil
}

// ValidateInvite checks an enrollment code.
func (r *Registry) ValidateInvite(code string) (Invite, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var inv Invite
	var expires, created string
	if err := r.db.QueryRow("SELECT code, name, expires_at, created_at FROM invite_codes WHERE code = ?", code).Scan(&inv.Code, &inv.Name, &expires, &created); err != nil {
		return Invite{}, errors.New("invalid invite code")
	}
	inv.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	inv.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if time.Now().After(inv.ExpiresAt) {
		return Invite{}, errors.New("invite code expired")
	}
	return inv, nil
}

// UseInvite consumes a one-time enrollment code.
func (r *Registry) UseInvite(code string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.db.Exec("DELETE FROM invite_codes WHERE code = ?", code)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("invite code not found")
	}
	return nil
}

// ListInvites returns all outstanding enrollment codes.
func (r *Registry) ListInvites() []Invite {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rows, err := r.db.Query("SELECT code, name, expires_at, created_at FROM invite_codes ORDER BY created_at DESC")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]Invite, 0)
	for rows.Next() {
		var inv Invite
		var expires, created string
		if err := rows.Scan(&inv.Code, &inv.Name, &expires, &created); err != nil {
			continue
		}
		inv.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
		inv.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, inv)
	}
	return out
}

// RevokeDeviceKey removes a device credential.
func (r *Registry) RevokeDeviceKey(deviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.db.Exec("DELETE FROM device_keys WHERE device_id = ?", deviceID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("device key not found")
	}
	return nil
}

// RecordAudit appends a security-relevant event.
func (r *Registry) RecordAudit(actor, action, target, detail string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec("INSERT INTO audit_log (actor, action, target, detail, created_at) VALUES (?, ?, ?, ?, ?)",
		actor, action, target, detail, time.Now().Format(time.RFC3339Nano))
	return err
}

// ListAudit returns the most recent audit entries.
func (r *Registry) ListAudit(limit int) []AuditEntry {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rows, err := r.db.Query("SELECT id, actor, action, target, detail, created_at FROM audit_log ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]AuditEntry, 0)
	for rows.Next() {
		var e AuditEntry
		var created string
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &e.Detail, &created); err != nil {
			continue
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, e)
	}
	return out
}

// Groups returns the distinct device groups.
func (r *Registry) Groups() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rows, err := r.db.Query("SELECT DISTINCT group_name FROM devices WHERE group_name != '' ORDER BY group_name COLLATE NOCASE")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			continue
		}
		out = append(out, g)
	}
	return out
}

func (r *Registry) getLocked(id string) (Device, error) {
	row := r.db.QueryRow(`
		SELECT id, name, hostname, os, arch, lan_ips, group_name, created_at, last_seen, online
		FROM devices
		WHERE id = ?`, id)
	return scanDevice(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDevice(row rowScanner) (Device, error) {
	var d Device
	var lanIPs, created, lastSeen string
	var online int
	if err := row.Scan(&d.ID, &d.Name, &d.Hostname, &d.OS, &d.Arch, &lanIPs, &d.Group, &created, &lastSeen, &online); err != nil {
		return Device{}, err
	}
	_ = json.Unmarshal([]byte(lanIPs), &d.LANIPs)
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	d.LastSeen, _ = time.Parse(time.RFC3339Nano, lastSeen)
	d.Online = online == 1
	return d, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
