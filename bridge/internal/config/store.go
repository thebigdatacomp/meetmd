package config

import (
	"context"
	"os"
	"sync"
	"time"
)

// Store holds the current config behind a lock so it can be hot-reloaded while
// the bridge runs. Components read it live via Get().
type Store struct {
	mu  sync.RWMutex
	cfg Config
}

// NewStore wraps an initial config.
func NewStore(cfg Config) *Store {
	return &Store{cfg: cfg}
}

// Get returns the current config snapshot.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Set replaces the current config.
func (s *Store) Set(cfg Config) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

const watchInterval = 2 * time.Second

// Watch polls the config file and reloads the store when it changes. onReload,
// if set, runs after each successful reload. Note: Port and the audio capturer
// are bound at startup and are NOT hot-reloaded (they still need a restart).
func Watch(ctx context.Context, store *Store, onReload func(Config)) {
	path := DefaultPath()
	last := modTime(path)
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := modTime(path)
			if now.Equal(last) {
				continue
			}
			last = now
			cfg, err := Load()
			if err != nil {
				continue // keep the previous config on a bad file
			}
			store.Set(cfg)
			if onReload != nil {
				onReload(cfg)
			}
		}
	}
}

func modTime(path string) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}
