package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := load(env(map[string]string{"DISTORTIONER_BOT_TOKEN": "t"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != ModePolling || c.Codec != "libx264" || c.MaxFileMB != 20 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.JobTimeout != 5*time.Minute {
		t.Fatalf("job timeout %v", c.JobTimeout)
	}
}

func TestLoadParsesValues(t *testing.T) {
	c, err := load(env(map[string]string{
		"DISTORTIONER_BOT_TOKEN":      "t",
		"DISTORTIONER_ADMIN_IDS":      "1, 2,,3",
		"DISTORTIONER_API_URL":        "http://bot-api:8081/",
		"DISTORTIONER_PRIORITY_CHATS": "-100",
		"DISTORTIONER_JOB_TIMEOUT":    "90s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.AdminIDs) != 3 || !c.IsAdmin(2) || c.IsAdmin(4) {
		t.Fatalf("admins %v", c.AdminIDs)
	}
	if c.APIURL != "http://bot-api:8081" || c.MaxFileMB != 2000 || c.MaxFileLimitMB != 2000 {
		t.Fatalf("api url %q max %d", c.APIURL, c.MaxFileMB)
	}
	if len(c.PriorityChats) != 1 || c.PriorityChats[0] != -100 || c.JobTimeout != 90*time.Second {
		t.Fatalf("unexpected %+v", c)
	}
}

func TestLegacyAdminID(t *testing.T) {
	c, err := load(env(map[string]string{"DISTORTIONER_BOT_TOKEN": "t", "DISTORTIONER_ADMIN_ID": "42"}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsAdmin(42) {
		t.Fatal("legacy admin id ignored")
	}
}

func TestLoadCollectsErrors(t *testing.T) {
	_, err := load(env(map[string]string{
		"DISTORTIONER_MODE":          "webhook",
		"DISTORTIONER_VIDEO_WORKERS": "zero",
		"DISTORTIONER_ADMIN_IDS":     "abc",
		"DISTORTIONER_MAX_FILE_MB":   "50",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"BOT_TOKEN", "WEBHOOK_URL", "VIDEO_WORKERS", "ADMIN_IDS", "MAX_FILE_MB"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}
