package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

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
	CreatedAt time.Time `json:"createdAt"`
	LastSeen  time.Time `json:"lastSeen"`
	Online    bool      `json:"online"`
}

const registrySchema = `
CREATE TABLE IF NOT EXISTS devices (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	hostname   TEXT NOT NULL,
	os         TEXT NOT NULL,
	arch       TEXT NOT NULL,
	lan_ips    TEXT NOT NULL DEFAULT '[]',
	created_at TEXT NOT NULL,
	last_seen  TEXT NOT NULL,
	online     INTEGER NOT NULL DEFAULT 0
);
`

// Registry keeps the device catalog in SQLite.
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
	// Agents reconnect after a restart; nothing is online until it says hello.
	if _, err := db.Exec("UPDATE devices SET online = 0"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return r, nil
}

// Close releases the database handle.
func (r *Registry) Close() error {
	return r.db.Close()
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

	var existingName string
	err = r.db.QueryRow("SELECT name FROM devices WHERE id = ?", id).Scan(&existingName)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = r.db.Exec(`
			INSERT INTO devices (id, name, hostname, os, arch, lan_ips, created_at, last_seen, online)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1)`,
			id, name, hostname, osName, arch, string(lanIPs), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
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
		SELECT id, name, hostname, os, arch, lan_ips, created_at, last_seen, online
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
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.db.Exec("UPDATE devices SET name = ? WHERE id = ?", name, id)
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

func (r *Registry) getLocked(id string) (Device, error) {
	row := r.db.QueryRow(`
		SELECT id, name, hostname, os, arch, lan_ips, created_at, last_seen, online
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
	if err := row.Scan(&d.ID, &d.Name, &d.Hostname, &d.OS, &d.Arch, &lanIPs, &created, &lastSeen, &online); err != nil {
		return Device{}, err
	}
	_ = json.Unmarshal([]byte(lanIPs), &d.LANIPs)
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	d.LastSeen, _ = time.Parse(time.RFC3339Nano, lastSeen)
	d.Online = online == 1
	return d, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
