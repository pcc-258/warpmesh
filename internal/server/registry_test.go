package server

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRegistryLifecycle(t *testing.T) {
	reg, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Upsert("dev-1", "home", "home-pc", "linux", "amd64", []string{"192.168.1.2"}); err != nil {
		t.Fatal(err)
	}
	dev, ok := reg.Get("dev-1")
	if !ok || !dev.Online {
		t.Fatalf("device should be online: %+v", dev)
	}
	if err := reg.SetOnline("dev-1", false); err != nil {
		t.Fatal(err)
	}
	dev, _ = reg.Get("dev-1")
	if dev.Online {
		t.Fatal("device should be offline")
	}
	if err := reg.Rename("dev-1", "nas"); err != nil {
		t.Fatal(err)
	}
	if err := reg.Delete("dev-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("dev-1"); ok {
		t.Fatal("device should be gone")
	}
}

// TestForwardPolicyMigrationDefaultsToAny pins the zero-configuration promise:
// a database created before devices.forward_policy existed must migrate and
// treat its existing rows as "any", not as denied.
func TestForwardPolicyMigrationDefaultsToAny(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE devices (
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
	INSERT INTO devices (id, name, hostname, os, arch, created_at, last_seen, online)
	VALUES ('dev-old', 'old', 'old-box', 'linux', 'amd64', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', 0);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reg, err := NewRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Close() }()

	dev, ok := reg.Get("dev-old")
	if !ok {
		t.Fatal("pre-existing device disappeared during migration")
	}
	if dev.ForwardPolicy != ForwardPolicyAny {
		t.Fatalf("migrated device should default to %q, got %q", ForwardPolicyAny, dev.ForwardPolicy)
	}
	if !reg.ForwardAllowed("dev-old", "dev-other", 443) {
		t.Fatal("a migrated device must be allowed to forward without any grant")
	}
}

// TestForwardGrantAuthorization exercises the restricted-path rule directly:
// exact target match, wildcard target, port bounds, and unknown sources.
func TestForwardGrantAuthorization(t *testing.T) {
	reg, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Close() }()
	for _, id := range []string{"dev-a", "dev-b", "dev-c"} {
		if _, err := reg.Upsert(id, id, id, "linux", "amd64", nil); err != nil {
			t.Fatal(err)
		}
	}

	// Unconfigured devices use the default policy and need no grant.
	if !reg.ForwardAllowed("dev-a", "dev-b", 22) {
		t.Fatal("default policy must allow any target and port")
	}
	if !reg.ForwardAllowed("dev-a", "dev-b", 65535) {
		t.Fatal("default policy must allow arbitrary ports")
	}

	if err := reg.SetForwardPolicy("dev-a", ForwardPolicyRestricted); err != nil {
		t.Fatal(err)
	}
	if dev, _ := reg.Get("dev-a"); dev.ForwardPolicy != ForwardPolicyRestricted {
		t.Fatalf("policy was not persisted: %+v", dev)
	}
	if reg.ForwardAllowed("dev-a", "dev-b", 22) {
		t.Fatal("restricted device without grants must be denied")
	}
	if reg.ForwardAllowed("dev-unknown", "dev-b", 22) {
		t.Fatal("an unknown source must fail closed")
	}

	if err := reg.SetForwardPolicy("dev-a", "sometimes"); err == nil {
		t.Fatal("an invalid policy must be rejected")
	}
	if err := reg.SetForwardPolicy("dev-missing", ForwardPolicyRestricted); err == nil {
		t.Fatal("setting a policy on a missing device must fail")
	}

	if err := reg.AddForwardGrant("dev-a", "dev-b", 20, 25); err != nil {
		t.Fatal(err)
	}
	if !reg.ForwardAllowed("dev-a", "dev-b", 22) || !reg.ForwardAllowed("dev-a", "dev-b", 20) || !reg.ForwardAllowed("dev-a", "dev-b", 25) {
		t.Fatal("a grant must allow its inclusive port range")
	}
	if reg.ForwardAllowed("dev-a", "dev-b", 19) || reg.ForwardAllowed("dev-a", "dev-b", 26) {
		t.Fatal("a grant must not allow ports outside its range")
	}
	if reg.ForwardAllowed("dev-a", "dev-c", 22) {
		t.Fatal("a grant for one target must not allow another target")
	}

	// A wildcard target grant covers every device inside its port range.
	if err := reg.AddForwardGrant("dev-a", "*", 8000, 9000); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"dev-b", "dev-c", "dev-later"} {
		if !reg.ForwardAllowed("dev-a", target, 8080) {
			t.Fatalf("wildcard grant must allow %s:8080", target)
		}
	}
	if reg.ForwardAllowed("dev-a", "dev-b", 7999) || reg.ForwardAllowed("dev-a", "dev-b", 9001) {
		t.Fatal("wildcard grant must respect its port range")
	}

	grants := reg.ListForwardGrantsFor("dev-a")
	if len(grants) != 2 {
		t.Fatalf("expected two grants for dev-a, got %+v", grants)
	}
	if all := reg.ListForwardGrants(); len(all) != 2 {
		t.Fatalf("expected two grants overall, got %+v", all)
	}
	// Re-adding the same grant is idempotent.
	if err := reg.AddForwardGrant("dev-a", "dev-b", 20, 25); err != nil {
		t.Fatal(err)
	}
	if all := reg.ListForwardGrants(); len(all) != 2 {
		t.Fatalf("duplicate grant must not be stored twice, got %+v", all)
	}
	stored, ok := reg.GetForwardGrant("dev-a", "dev-b", 20, 25)
	if !ok || stored.CreatedAt.IsZero() {
		t.Fatalf("stored grant should round-trip with a timestamp: %+v ok=%v", stored, ok)
	}

	if err := reg.DeleteForwardGrant("dev-a", "dev-b", 20, 25); err != nil {
		t.Fatal(err)
	}
	if reg.ForwardAllowed("dev-a", "dev-b", 22) {
		t.Fatal("a deleted grant must stop authorizing")
	}
	if err := reg.DeleteForwardGrant("dev-a", "dev-b", 20, 25); err == nil {
		t.Fatal("deleting a missing grant must fail")
	}

	for _, bad := range []struct {
		source string
		target string
		port   int
	}{
		{"", "dev-b", 22},
		{"dev-a", "", 22},
		{"dev-a", "dev-b", 0},
	} {
		if err := reg.AddForwardGrant(bad.source, bad.target, bad.port, bad.port); err == nil {
			t.Fatalf("invalid grant (%q, %q, %d) must be rejected", bad.source, bad.target, bad.port)
		}
	}
	if err := reg.AddForwardGrant("dev-a", "dev-b", 100, 50); err == nil {
		t.Fatal("an inverted port range must be rejected")
	}
	if err := reg.AddForwardGrant("dev-missing", "dev-b", 22, 22); err == nil {
		t.Fatal("a grant for a missing source must be rejected")
	}
}

func TestListForwardPoliciesCountsGrants(t *testing.T) {
	reg, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Close() }()
	if _, err := reg.Upsert("dev-b", "beta", "beta", "linux", "amd64", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Upsert("dev-a", "alpha", "alpha", "linux", "amd64", nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetForwardPolicy("dev-a", ForwardPolicyRestricted); err != nil {
		t.Fatal(err)
	}
	if err := reg.AddForwardGrant("dev-a", "dev-b", 22, 22); err != nil {
		t.Fatal(err)
	}
	if err := reg.AddForwardGrant("dev-a", "*", 9000, 9001); err != nil {
		t.Fatal(err)
	}

	policies := reg.ListForwardPolicies()
	if len(policies) != 2 {
		t.Fatalf("expected two devices, got %+v", policies)
	}
	if policies[0].DeviceID != "dev-a" || policies[0].ForwardPolicy != ForwardPolicyRestricted || policies[0].GrantCount != 2 {
		t.Fatalf("unexpected alpha record: %+v", policies[0])
	}
	if policies[1].DeviceID != "dev-b" || policies[1].ForwardPolicy != ForwardPolicyAny || policies[1].GrantCount != 0 {
		t.Fatalf("unexpected beta record: %+v", policies[1])
	}
}
