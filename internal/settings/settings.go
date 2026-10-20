// Package settings holds the runtime-tunable state that admins change from
// the admin panel: limits, maintenance mode, bans and priority chats. Every
// change is persisted, and reads are served from memory.
package settings

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/graynk/distortioner/internal/store"
)

// Key names an integer setting.
type Key string

const (
	MaxVideoSeconds Key = "max_video_seconds"
	MaxJobsPerChat  Key = "max_jobs_per_chat"
	MaxQueue        Key = "max_queue"
	RateLimit       Key = "rate_limit"
	RateWindow      Key = "rate_window_seconds"
	DefaultStrength Key = "default_strength"
	MaxFileMB       Key = "max_file_mb"
	MaxFPS          Key = "max_fps"
	ProcessScale    Key = "process_scale"
)

// Spec describes the default and valid range of an integer setting.
type Spec struct {
	Key      Key
	Default  int
	Min, Max int
}

// defaultSpecs lists every integer setting in display order. Some bounds
// depend on the deployment and are adjusted by Load.
var defaultSpecs = []Spec{
	{MaxFileMB, PublicAPIMaxFileMB, 1, PublicAPIMaxFileMB},
	{MaxVideoSeconds, 60, 5, 600},
	{MaxFPS, 30, 5, 60},
	{ProcessScale, 100, 25, 100},
	{MaxJobsPerChat, 3, 1, 50},
	{MaxQueue, 500, 10, 10000},
	{RateLimit, 5, 1, 100},
	{RateWindow, 300, 10, 3600},
	{DefaultStrength, 50, MinStrength, MaxStrength},
}

// File size limits of the Bot API: the public server refuses to serve
// larger files to bots, a self-hosted one allows up to 2 GB.
const (
	PublicAPIMaxFileMB = 20
	LocalAPIMaxFileMB  = 2000
)

// Strength bounds, in percent of the image removed by seam carving.
const (
	MinStrength = 20
	MaxStrength = 80
)

const (
	keyMaintenance     = "maintenance"
	keyMaintenanceText = "maintenance_text"
)

// Options adjust the defaults to the deployment.
type Options struct {
	// PriorityChats (from the environment) are added to the priority list.
	PriorityChats []int64
	// MaxFileMB is the default file size limit; MaxFileLimitMB caps what
	// admins may set (it depends on which Bot API server is used).
	MaxFileMB      int
	MaxFileLimitMB int
}

// Service is safe for concurrent use.
type Service struct {
	store *store.Store
	specs []Spec

	mu              sync.RWMutex
	ints            map[Key]int
	maintenance     bool
	maintenanceText string
	sets            map[store.ChatSet]map[int64]struct{}
}

// Load reads persisted settings; stored values outside the valid range
// are replaced by defaults.
func Load(ctx context.Context, st *store.Store, opt Options) (*Service, error) {
	s := &Service{store: st, ints: map[Key]int{}, sets: map[store.ChatSet]map[int64]struct{}{}}
	s.specs = append([]Spec(nil), defaultSpecs...)
	for i := range s.specs {
		if s.specs[i].Key != MaxFileMB {
			continue
		}
		if opt.MaxFileLimitMB > 0 {
			s.specs[i].Max = opt.MaxFileLimitMB
		}
		if opt.MaxFileMB > 0 {
			s.specs[i].Default = min(opt.MaxFileMB, s.specs[i].Max)
		}
	}

	raw, err := st.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	for _, spec := range s.specs {
		s.ints[spec.Key] = spec.Default
		if v, ok := raw[string(spec.Key)]; ok {
			if n, err := strconv.Atoi(v); err == nil && n >= spec.Min && n <= spec.Max {
				s.ints[spec.Key] = n
			}
		}
	}
	s.maintenance = raw[keyMaintenance] == "1"
	s.maintenanceText = raw[keyMaintenanceText]

	for _, id := range opt.PriorityChats {
		if err := st.AddChat(ctx, store.PriorityChats, id); err != nil {
			return nil, err
		}
	}
	for _, set := range []store.ChatSet{store.Bans, store.PriorityChats} {
		ids, err := st.Chats(ctx, set)
		if err != nil {
			return nil, err
		}
		m := make(map[int64]struct{}, len(ids))
		for _, id := range ids {
			m[id] = struct{}{}
		}
		s.sets[set] = m
	}
	return s, nil
}

// Specs lists the integer settings in display order.
func (s *Service) Specs() []Spec { return s.specs }

// Spec returns the spec of a key.
func (s *Service) Spec(k Key) (Spec, bool) {
	for _, spec := range s.specs {
		if spec.Key == k {
			return spec, true
		}
	}
	return Spec{}, false
}

// Int returns the current value of an integer setting.
func (s *Service) Int(k Key) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ints[k]
}

// SetInt validates and persists an integer setting.
func (s *Service) SetInt(ctx context.Context, k Key, v int) error {
	spec, ok := s.Spec(k)
	if !ok {
		return fmt.Errorf("unknown setting %q", k)
	}
	if v < spec.Min || v > spec.Max {
		return fmt.Errorf("%s must be between %d and %d", k, spec.Min, spec.Max)
	}
	if err := s.store.SetSetting(ctx, string(k), strconv.Itoa(v)); err != nil {
		return err
	}
	s.mu.Lock()
	s.ints[k] = v
	s.mu.Unlock()
	return nil
}

// Maintenance reports whether maintenance mode is on and its custom message.
func (s *Service) Maintenance() (bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maintenance, s.maintenanceText
}

// SetMaintenance toggles maintenance mode.
func (s *Service) SetMaintenance(ctx context.Context, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	if err := s.store.SetSetting(ctx, keyMaintenance, v); err != nil {
		return err
	}
	s.mu.Lock()
	s.maintenance = on
	s.mu.Unlock()
	return nil
}

// SetMaintenanceText changes the message shown during maintenance.
func (s *Service) SetMaintenanceText(ctx context.Context, text string) error {
	if err := s.store.SetSetting(ctx, keyMaintenanceText, text); err != nil {
		return err
	}
	s.mu.Lock()
	s.maintenanceText = text
	s.mu.Unlock()
	return nil
}

// Has reports whether chatID is in the set.
func (s *Service) Has(set store.ChatSet, chatID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.sets[set][chatID]
	return ok
}

// Add persists chatID into the set.
func (s *Service) Add(ctx context.Context, set store.ChatSet, chatID int64) error {
	if err := s.store.AddChat(ctx, set, chatID); err != nil {
		return err
	}
	s.mu.Lock()
	s.sets[set][chatID] = struct{}{}
	s.mu.Unlock()
	return nil
}

// Remove deletes chatID from the set.
func (s *Service) Remove(ctx context.Context, set store.ChatSet, chatID int64) error {
	if err := s.store.RemoveChat(ctx, set, chatID); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.sets[set], chatID)
	s.mu.Unlock()
	return nil
}

// List returns the chats in the set in a stable order.
func (s *Service) List(set store.ChatSet) []int64 {
	s.mu.RLock()
	ids := make([]int64, 0, len(s.sets[set]))
	for id := range s.sets[set] {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	slices.Sort(ids)
	return ids
}

// IsBanned is a shorthand for Has(store.Bans, chatID).
func (s *Service) IsBanned(chatID int64) bool { return s.Has(store.Bans, chatID) }

// IsPriority is a shorthand for Has(store.PriorityChats, chatID).
func (s *Service) IsPriority(chatID int64) bool { return s.Has(store.PriorityChats, chatID) }
