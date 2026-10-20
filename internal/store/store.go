// Package store persists statistics, chat preferences and admin state in SQLite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store is a thin repository over a single SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database in dir and applies migrations.
func Open(ctx context.Context, dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + filepath.Join(dir, "distortioner.sqlite") +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows a single writer; one connection avoids SQLITE_BUSY entirely.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`create table if not exists schema_migrations (version integer primary key, applied_at integer not null)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(filepath.Base(name), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %s: %w", name, err)
		}
		var applied bool
		if err := s.db.QueryRowContext(ctx,
			`select exists(select 1 from schema_migrations where version = ?)`, version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := migrationsFS.ReadFile(name)
		if err != nil {
			return err
		}
		if err := s.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return err
			}
			if version == 1 {
				if err := importLegacyStats(ctx, tx); err != nil {
					return fmt.Errorf("import v1 stats: %w", err)
				}
			}
			_, err := tx.ExecContext(ctx, `insert into schema_migrations(version, applied_at) values (?, ?)`,
				version, time.Now().Unix())
			return err
		}); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

// importLegacyStats copies rows from the v1 "stats" table, if the database
// was created by the old bot, so the history survives the upgrade.
func importLegacyStats(ctx context.Context, tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRowContext(ctx,
		`select exists(select 1 from sqlite_master where type = 'table' and name = 'stats')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	// v1 stored Go's time.Time text ("2006-01-02 15:04:05.999 -0700 MST");
	// the first 19 characters are a local timestamp SQLite can parse.
	_, err := tx.ExecContext(ctx, `
		insert into events (chat_id, is_group, kind, status, duration_ms, created_at)
		select user_id, coalesce(is_group_chat, 0),
		       case type when 'videonote' then 'video_note' else type end,
		       'ok', 0, cast(strftime('%s', substr(date, 1, 19)) as integer)
		from stats
		where strftime('%s', substr(date, 1, 19)) is not null`)
	return err
}

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Event statuses.
const (
	StatusOK       = "ok"
	StatusFailed   = "failed"
	StatusRejected = "rejected"
)

// Event sources: how the user asked for the effects.
const (
	SourceCommand = "command" // /wobble, /fry
	SourceRandom  = "random"  // /distort or the 🎲 button
	SourcePrompt  = "prompt"  // a button of the "what to do?" question
	SourcePicker  = "picker"  // effects picked one by one
	SourceButton  = "button"  // a button under a result
)

// Event is a single distortion request outcome.
type Event struct {
	ChatID   int64
	IsGroup  bool
	Kind     string
	Status   string
	Duration time.Duration
	At       time.Time
	Source   string
	Effects  []string // recorded for successful events
}

// RecordEvent stores one event with the effects it applied.
func (s *Store) RecordEvent(ctx context.Context, e Event) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`insert into events (chat_id, is_group, kind, status, duration_ms, created_at, source) values (?, ?, ?, ?, ?, ?, ?)`,
			e.ChatID, e.IsGroup, e.Kind, e.Status, e.Duration.Milliseconds(), e.At.Unix(), e.Source)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, effect := range e.Effects {
			if _, err := tx.ExecContext(ctx, `insert into effect_uses (event_id, effect) values (?, ?)`, id, effect); err != nil {
				return err
			}
		}
		return nil
	})
}

// EffectStats counts effect uses and request sources of successful events.
type EffectStats struct {
	Effects map[string]int
	Sources map[string]int
}

// EffectStats aggregates successful events created at or after since.
func (s *Store) EffectStats(ctx context.Context, since time.Time) (EffectStats, error) {
	st := EffectStats{Effects: map[string]int{}, Sources: map[string]int{}}
	if err := s.countInto(ctx, st.Effects, `
		select u.effect, count(*) from effect_uses u join events e on e.id = u.event_id
		where e.created_at >= ? and e.status = 'ok' group by u.effect`, since.Unix()); err != nil {
		return st, err
	}
	err := s.countInto(ctx, st.Sources, `
		select source, count(*) from events
		where created_at >= ? and status = 'ok' and source != '' group by source`, since.Unix())
	return st, err
}

func (s *Store) countInto(ctx context.Context, m map[string]int, q string, args ...any) error {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		m[k] = n
	}
	return rows.Err()
}

// Stats is an aggregate over a period.
type Stats struct {
	Requests    int
	Chats       int
	Groups      int
	Failed      int
	Rejected    int
	AvgDuration time.Duration
	ByKind      map[string]int
}

// Stats aggregates events created at or after since.
func (s *Store) Stats(ctx context.Context, since time.Time) (Stats, error) {
	st := Stats{ByKind: map[string]int{}}
	var avg sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		select count(*),
		       count(distinct chat_id),
		       count(distinct case when is_group = 1 then chat_id end),
		       count(case when status = 'failed' then 1 end),
		       count(case when status = 'rejected' then 1 end),
		       avg(case when status = 'ok' and duration_ms > 0 then duration_ms end)
		from events where created_at >= ?`, since.Unix()).
		Scan(&st.Requests, &st.Chats, &st.Groups, &st.Failed, &st.Rejected, &avg)
	if err != nil {
		return st, err
	}
	st.AvgDuration = time.Duration(avg.Float64) * time.Millisecond

	rows, err := s.db.QueryContext(ctx,
		`select kind, count(*) from events where created_at >= ? and status != 'rejected' group by kind`, since.Unix())
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return st, err
		}
		st.ByKind[kind] = n
	}
	return st, rows.Err()
}

// PruneEvents deletes events older than before.
func (s *Store) PruneEvents(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `delete from events where created_at < ?`, before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ChatSettings are per-chat user preferences.
type ChatSettings struct {
	Strength     int // wobble
	FryStrength  int
	DistortAudio bool
	Lang         string // empty means "detect from the user"
}

// ChatSettings returns stored preferences; ok is false when none are stored.
func (s *Store) ChatSettings(ctx context.Context, chatID int64) (cs ChatSettings, ok bool, err error) {
	err = s.db.QueryRowContext(ctx,
		`select strength, fry_strength, distort_audio, lang from chat_settings where chat_id = ?`, chatID).
		Scan(&cs.Strength, &cs.FryStrength, &cs.DistortAudio, &cs.Lang)
	if errors.Is(err, sql.ErrNoRows) {
		return cs, false, nil
	}
	return cs, err == nil, err
}

// SaveChatSettings upserts preferences for a chat.
func (s *Store) SaveChatSettings(ctx context.Context, chatID int64, cs ChatSettings) error {
	_, err := s.db.ExecContext(ctx, `
		insert into chat_settings (chat_id, strength, fry_strength, distort_audio, lang) values (?, ?, ?, ?, ?)
		on conflict (chat_id) do update set strength = excluded.strength, fry_strength = excluded.fry_strength,
			distort_audio = excluded.distort_audio, lang = excluded.lang`,
		chatID, cs.Strength, cs.FryStrength, cs.DistortAudio, cs.Lang)
	return err
}

// ChatSet names a table holding a set of chat ids.
type ChatSet string

const (
	Bans          ChatSet = "bans"
	PriorityChats ChatSet = "priority_chats"
)

// chatSetQueries returns the statements for a set. Table names cannot be
// query parameters, so each set has its own literal statements.
func chatSetQueries(set ChatSet) (insert, remove, list string) {
	if set == PriorityChats {
		return `insert into priority_chats (chat_id, created_at) values (?, ?) on conflict do nothing`,
			`delete from priority_chats where chat_id = ?`,
			`select chat_id from priority_chats order by created_at desc, chat_id`
	}
	return `insert into bans (chat_id, created_at) values (?, ?) on conflict do nothing`,
		`delete from bans where chat_id = ?`,
		`select chat_id from bans order by created_at desc, chat_id`
}

// AddChat inserts a chat id into the set; adding twice is not an error.
func (s *Store) AddChat(ctx context.Context, set ChatSet, chatID int64) error {
	q, _, _ := chatSetQueries(set)
	_, err := s.db.ExecContext(ctx, q, chatID, time.Now().Unix())
	return err
}

// RemoveChat deletes a chat id from the set.
func (s *Store) RemoveChat(ctx context.Context, set ChatSet, chatID int64) error {
	_, q, _ := chatSetQueries(set)
	_, err := s.db.ExecContext(ctx, q, chatID)
	return err
}

// Chats lists every chat id in the set, newest first.
func (s *Store) Chats(ctx context.Context, set ChatSet) ([]int64, error) {
	_, _, q := chatSetQueries(set)
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Settings returns every stored runtime setting.
func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `select key, value from settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// SetSetting upserts one runtime setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`insert into settings (key, value) values (?, ?) on conflict (key) do update set value = excluded.value`,
		key, value)
	return err
}

// Failure is a failed job kept for debugging.
type Failure struct {
	ID        int64
	ChatID    int64
	Kind      string
	Error     string
	InputPath string
	At        time.Time
}

// AddFailure stores a failure and returns its id.
func (s *Store) AddFailure(ctx context.Context, f Failure) (int64, error) {
	if f.At.IsZero() {
		f.At = time.Now()
	}
	res, err := s.db.ExecContext(ctx,
		`insert into failures (chat_id, kind, error, input_path, created_at) values (?, ?, ?, ?, ?)`,
		f.ChatID, f.Kind, f.Error, f.InputPath, f.At.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Failures returns the most recent failures.
func (s *Store) Failures(ctx context.Context, limit int) ([]Failure, error) {
	return s.queryFailures(ctx, `select id, chat_id, kind, error, input_path, created_at
		from failures order by id desc limit ?`, limit)
}

// Failure returns a single failure by id.
func (s *Store) Failure(ctx context.Context, id int64) (Failure, error) {
	fs, err := s.queryFailures(ctx, `select id, chat_id, kind, error, input_path, created_at
		from failures where id = ?`, id)
	if err != nil {
		return Failure{}, err
	}
	if len(fs) == 0 {
		return Failure{}, sql.ErrNoRows
	}
	return fs[0], nil
}

// FailureInputs returns failures that still reference a kept input file, oldest first.
func (s *Store) FailureInputs(ctx context.Context) ([]Failure, error) {
	return s.queryFailures(ctx, `select id, chat_id, kind, error, input_path, created_at
		from failures where input_path != '' order by id`)
}

// ForgetFailureInput clears the input path after the file was deleted.
func (s *Store) ForgetFailureInput(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `update failures set input_path = '' where id = ?`, id)
	return err
}

// PruneFailures deletes failures older than before.
func (s *Store) PruneFailures(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `delete from failures where created_at < ? and input_path = ''`, before.Unix())
	return err
}

func (s *Store) queryFailures(ctx context.Context, q string, args ...any) ([]Failure, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Failure
	for rows.Next() {
		var f Failure
		var at int64
		if err := rows.Scan(&f.ID, &f.ChatID, &f.Kind, &f.Error, &f.InputPath, &at); err != nil {
			return nil, err
		}
		f.At = time.Unix(at, 0)
		out = append(out, f)
	}
	return out, rows.Err()
}
