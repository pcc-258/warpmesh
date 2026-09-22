package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
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

// Registry keeps the device catalog and persists it to disk.
type Registry struct {
	mu      sync.RWMutex
	devices map[string]*Device
	path    string
}

// NewRegistry loads an existing catalog from dataDir or starts empty.
func NewRegistry(dataDir string) (*Registry, error) {
	r := &Registry{devices: make(map[string]*Device)}
	if dataDir == "" {
		return r, nil
	}
	r.path = filepath.Join(dataDir, "devices.json")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) load() error {
	raw, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var devices []*Device
	if err := json.Unmarshal(raw, &devices); err != nil {
		return err
	}
	for _, d := range devices {
		r.devices[d.ID] = d
	}
	return nil
}

func (r *Registry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	devices := make([]*Device, 0, len(r.devices))
	for _, d := range r.devices {
		devices = append(devices, d)
	}
	raw, err := json.MarshalIndent(devices, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, raw, 0o644)
}

// Upsert registers metadata for a device and returns the stored record.
func (r *Registry) Upsert(id, name, hostname, osName, arch string, ips []string) (*Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	now := time.Now()
	if !ok {
		d = &Device{ID: id, CreatedAt: now}
		r.devices[id] = d
	}
	if name != "" {
		d.Name = name
	}
	if hostname != "" {
		d.Hostname = hostname
	}
	if osName != "" {
		d.OS = osName
	}
	if arch != "" {
		d.Arch = arch
	}
	if len(ips) > 0 {
		d.LANIPs = ips
	}
	d.LastSeen = now
	d.Online = true
	return d, r.saveLocked()
}

// SetOnline updates liveness for a device.
func (r *Registry) SetOnline(id string, online bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	if !ok {
		return nil
	}
	d.Online = online
	if online {
		d.LastSeen = time.Now()
	}
	return r.saveLocked()
}

// Get returns a copy of one device.
func (r *Registry) Get(id string) (Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.devices[id]
	if !ok {
		return Device{}, false
	}
	return *d, true
}

// List returns all devices sorted by name.
func (r *Registry) List() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Device, 0, len(r.devices))
	for _, d := range r.devices {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Rename updates the user-facing name of a device.
func (r *Registry) Rename(id, name string) error {
	if name == "" {
		return errors.New("name cannot be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[id]
	if !ok {
		return errors.New("device not found")
	}
	d.Name = name
	return r.saveLocked()
}

// Delete removes a device from the catalog.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.devices[id]; !ok {
		return errors.New("device not found")
	}
	delete(r.devices, id)
	return r.saveLocked()
}
