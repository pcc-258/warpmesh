package server

import "testing"

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
