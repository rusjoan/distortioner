package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStats(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()
	events := []Event{
		{ChatID: 1, Kind: "photo", Status: StatusOK, Duration: time.Second, At: now},
		{ChatID: 1, Kind: "video", Status: StatusOK, Duration: 3 * time.Second, At: now},
		{ChatID: -5, IsGroup: true, Kind: "video", Status: StatusFailed, At: now},
		{ChatID: 2, Kind: "video", Status: StatusRejected, At: now},
		{ChatID: 3, Kind: "photo", Status: StatusOK, At: now.Add(-48 * time.Hour)},
	}
	for _, e := range events {
		if err := s.RecordEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.Stats(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if st.Requests != 4 || st.Chats != 3 || st.Groups != 1 || st.Failed != 1 || st.Rejected != 1 {
		t.Fatalf("stats %+v", st)
	}
	if st.AvgDuration != 2*time.Second {
		t.Fatalf("avg %v", st.AvgDuration)
	}
	if st.ByKind["video"] != 2 || st.ByKind["photo"] != 1 {
		t.Fatalf("by kind %v", st.ByKind)
	}

	n, err := s.PruneEvents(ctx, now.Add(-24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
}

func TestChatSettings(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, ok, err := s.ChatSettings(ctx, 7); ok || err != nil {
		t.Fatalf("expected none, got ok=%v err=%v", ok, err)
	}
	want := ChatSettings{Strength: 70, FryStrength: 30, DistortAudio: true, Lang: "ru"}
	if err := s.SaveChatSettings(ctx, 7, want); err != nil {
		t.Fatal(err)
	}
	want.Strength = 30
	if err := s.SaveChatSettings(ctx, 7, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.ChatSettings(ctx, 7)
	if err != nil || !ok || got != want {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
}

func TestChatSets(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, id := range []int64{1, 2, 2} {
		if err := s.AddChat(ctx, Bans, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RemoveChat(ctx, Bans, 1); err != nil {
		t.Fatal(err)
	}
	ids, err := s.Chats(ctx, Bans)
	if err != nil || len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("ids %v err %v", ids, err)
	}
	if ids, _ := s.Chats(ctx, PriorityChats); len(ids) != 0 {
		t.Fatalf("priority should be empty, got %v", ids)
	}
}

func TestFailures(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	id, err := s.AddFailure(ctx, Failure{ChatID: 1, Kind: "video", Error: "boom", InputPath: "/tmp/x"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Failure(ctx, id)
	if err != nil || f.Error != "boom" || f.InputPath != "/tmp/x" {
		t.Fatalf("%+v %v", f, err)
	}
	if err := s.ForgetFailureInput(ctx, id); err != nil {
		t.Fatal(err)
	}
	if inputs, _ := s.FailureInputs(ctx); len(inputs) != 0 {
		t.Fatalf("inputs %v", inputs)
	}
	if _, err := s.Failure(ctx, 999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
}

func TestImportsLegacyStats(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "distortioner.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`create table stats(id integer not null primary key, user_id integer, is_group_chat integer, date integer, type text);
		insert into stats(user_id, is_group_chat, date, type) values
			(10, 0, '2024-05-01 12:00:00.123456+03:00', 'videonote'),
			(-20, 1, '2024-05-02 12:00:00+03:00', 'photo');`)
	if err != nil {
		t.Fatal(err)
	}
	_ = legacy.Close()

	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	st, err := s.Stats(ctx, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if st.Requests != 2 || st.Groups != 1 || st.ByKind["video_note"] != 1 {
		t.Fatalf("imported %+v", st)
	}

	// Reopening must not import twice.
	_ = s.Close()
	s2, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if st, _ := s2.Stats(ctx, time.Time{}); st.Requests != 2 {
		t.Fatalf("re-imported: %+v", st)
	}
}

func TestEffectStats(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()
	for _, e := range []Event{
		{ChatID: 1, Kind: "video", Status: StatusOK, Source: SourceRandom, Effects: []string{"bulge", "earrape"}, At: now},
		{ChatID: 1, Kind: "photo", Status: StatusOK, Source: SourcePrompt, Effects: []string{"fry"}, At: now},
		{ChatID: 2, Kind: "video", Status: StatusFailed, Source: SourceRandom, Effects: []string{"bulge"}, At: now},
		{ChatID: 3, Kind: "voice", Status: StatusOK, Source: SourceRandom, Effects: []string{"earrape"}, At: now.Add(-48 * time.Hour)},
	} {
		if err := s.RecordEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.EffectStats(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if st.Effects["bulge"] != 1 || st.Effects["earrape"] != 1 || st.Effects["fry"] != 1 || len(st.Effects) != 3 {
		t.Fatalf("effects %v", st.Effects)
	}
	if st.Sources[SourceRandom] != 1 || st.Sources[SourcePrompt] != 1 {
		t.Fatalf("sources %v", st.Sources)
	}
	// Pruning old events takes their effect uses along.
	if _, err := s.PruneEvents(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.EffectStats(ctx, time.Time{}); all.Effects["earrape"] != 1 {
		t.Fatalf("after prune %v", all.Effects)
	}
}

func TestActiveSince(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Now()
	for _, e := range []Event{
		{ChatID: 1, UserID: 1, Kind: "photo", Status: StatusOK, At: now},
		{ChatID: -5, UserID: 1, IsGroup: true, Kind: "video", Status: StatusOK, At: now},
		{ChatID: -5, UserID: 2, IsGroup: true, Kind: "video", Status: StatusRejected, At: now},
		{ChatID: 3, UserID: 3, Kind: "text", Status: StatusOK, At: now.Add(-48 * time.Hour)},
	} {
		if err := s.RecordEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.ActiveSince(ctx, now.Add(-24*time.Hour))
	if err != nil || a != (Active{Users: 2, Chats: 2, Groups: 1}) {
		t.Fatalf("%+v %v", a, err)
	}
}
