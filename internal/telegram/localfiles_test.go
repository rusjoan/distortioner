package telegram

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepLocalFiles(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old, fresh := now.Add(-4*time.Hour), now.Add(-time.Hour)

	files := map[string]time.Time{
		"123:abc/videos/file_1.mp4":     old,   // deleted
		"123:abc/photos/sub/file_2.jpg": old,   // deleted, nested
		"123:abc/voice/file_3.oga":      fresh, // kept: younger than ttl
		"123:abc/td.binlog":             old,   // kept: server state
		"123:abc/db/db.sqlite":          old,   // kept: not a media dir
		"tqueue.binlog":                 old,   // kept: top level
	}
	for name, mtime := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	deleted, freed, err := SweepLocalFiles(root, 3*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 || freed != 10 {
		t.Fatalf("deleted %d, freed %d", deleted, freed)
	}
	for name := range files {
		_, err := os.Stat(filepath.Join(root, name))
		gone := os.IsNotExist(err)
		wantGone := name == "123:abc/videos/file_1.mp4" || name == "123:abc/photos/sub/file_2.jpg"
		if gone != wantGone {
			t.Errorf("%s: gone=%v, want %v", name, gone, wantGone)
		}
	}
}

func TestSweepLocalFilesMissingRoot(t *testing.T) {
	if _, _, err := SweepLocalFiles(filepath.Join(t.TempDir(), "nope"), time.Hour, time.Now()); err == nil {
		t.Fatal("expected error for a missing root")
	}
}

func TestSweepLocalFilesDoesNotFollowSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Hour)
	if err := os.Chtimes(victim, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "123:abc"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A media dir that is a symlink out of the volume.
	if err := os.Symlink(outside, filepath.Join(root, "123:abc", "videos")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SweepLocalFiles(root, time.Hour, time.Now()); err != nil {
		t.Logf("sweep reported: %v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("file outside the volume was deleted: %v", err)
	}
}
