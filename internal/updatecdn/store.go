package updatecdn

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Store atomically reloads the catalog when the manifest changes. A broken new
// manifest is never mixed with the previous snapshot.
type Store struct {
	manifestPath string
	filesDir     string

	mu       sync.RWMutex
	catalog  *Catalog
	modTime  time.Time
	fileSize int64
}

func NewStore(manifestPath, filesDir string) (*Store, error) {
	store := &Store{manifestPath: manifestPath, filesDir: filesDir}
	if _, err := store.Snapshot(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Snapshot() (*Catalog, error) {
	info, err := os.Stat(s.manifestPath)
	if err != nil {
		return nil, fmt.Errorf("stat manifest: %w", err)
	}
	s.mu.RLock()
	if s.catalog != nil && info.ModTime().Equal(s.modTime) && info.Size() == s.fileSize {
		catalog := s.catalog
		s.mu.RUnlock()
		return catalog, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	info, err = os.Stat(s.manifestPath)
	if err != nil {
		return nil, fmt.Errorf("stat manifest: %w", err)
	}
	if s.catalog != nil && info.ModTime().Equal(s.modTime) && info.Size() == s.fileSize {
		return s.catalog, nil
	}
	catalog, err := LoadCatalog(s.manifestPath, s.filesDir)
	if err != nil {
		return nil, err
	}
	s.catalog = catalog
	s.modTime = info.ModTime()
	s.fileSize = info.Size()
	return catalog, nil
}
