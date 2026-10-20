package settings

import (
	"context"
	"testing"

	"github.com/graynk/distortioner/internal/store"
)

func TestPersistence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	s, err := Load(ctx, st, Options{PriorityChats: []int64{-100}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Int(MaxVideoSeconds) != 60 || !s.IsPriority(-100) {
		t.Fatal("defaults not applied")
	}
	if err := s.SetInt(ctx, MaxVideoSeconds, 1); err == nil {
		t.Fatal("out of range value accepted")
	}
	if err := s.SetInt(ctx, MaxVideoSeconds, 90); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(ctx, store.Bans, 7); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMaintenance(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMaintenanceText(ctx, "brb"); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(ctx, st, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Int(MaxVideoSeconds) != 90 || !reloaded.IsBanned(7) || !reloaded.IsPriority(-100) {
		t.Fatal("settings not persisted")
	}
	if on, text := reloaded.Maintenance(); !on || text != "brb" {
		t.Fatalf("maintenance %v %q", on, text)
	}
	if err := reloaded.Remove(ctx, store.Bans, 7); err != nil || reloaded.IsBanned(7) {
		t.Fatal("unban failed")
	}
}

func TestMaxFileBounds(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	public, err := Load(ctx, st, Options{MaxFileMB: 50, MaxFileLimitMB: PublicAPIMaxFileMB})
	if err != nil {
		t.Fatal(err)
	}
	if public.Int(MaxFileMB) != 20 {
		t.Fatalf("default not capped: %d", public.Int(MaxFileMB))
	}
	if err := public.SetInt(ctx, MaxFileMB, 21); err == nil {
		t.Fatal("value above the public API limit accepted")
	}

	local, err := Load(ctx, st, Options{MaxFileMB: 2000, MaxFileLimitMB: LocalAPIMaxFileMB})
	if err != nil {
		t.Fatal(err)
	}
	if err := local.SetInt(ctx, MaxFileMB, 500); err != nil {
		t.Fatal(err)
	}
	// Switching back to the public API ignores the stored 500 MB.
	again, err := Load(ctx, st, Options{MaxFileMB: 10, MaxFileLimitMB: PublicAPIMaxFileMB})
	if err != nil {
		t.Fatal(err)
	}
	if again.Int(MaxFileMB) != 10 {
		t.Fatalf("got %d", again.Int(MaxFileMB))
	}
}
