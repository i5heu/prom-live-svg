package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"prom-live-svg/internal/config"
	promapi "prom-live-svg/internal/prometheus"
)

type requestStatStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]requestStatEntry
}

type requestStatEntry struct {
	Total          float64 `json:"total"`
	LastObserved   float64 `json:"last_observed"`
	LastObservedAt int64   `json:"last_observed_at"`
}

type requestStatSnapshot struct {
	Entries map[string]requestStatEntry `json:"entries"`
}

func newRequestStatStore(dataDir string) (*requestStatStore, error) { // A
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, fmt.Errorf("storage data dir must not be empty")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create storage data dir %q: %w", dataDir, err)
	}

	store := &requestStatStore{
		path:    filepath.Join(dataDir, "request_stats.json"),
		entries: map[string]requestStatEntry{},
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *requestStatStore) load() error { // A
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read request stat store %q: %w", s.path, err)
	}

	var snapshot requestStatSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode request stat store %q: %w", s.path, err)
	}
	if snapshot.Entries == nil {
		snapshot.Entries = map[string]requestStatEntry{}
	}

	s.entries = snapshot.Entries
	return nil
}

func (s *requestStatStore) InitializeEntry(chartName string, statName string, total float64, lastObserved float64, observedAt time.Time) (requestStatEntry, error) { // A
	if err := validateFiniteNonNegative("persistent stat total", total); err != nil {
		return requestStatEntry{}, err
	}
	if err := validateFiniteNonNegative("persistent stat last observed", lastObserved); err != nil {
		return requestStatEntry{}, err
	}
	if observedAt.IsZero() {
		return requestStatEntry{}, fmt.Errorf("persistent stat observed time must not be zero")
	}

	entry := requestStatEntry{
		Total:          total,
		LastObserved:   lastObserved,
		LastObservedAt: observedAt.UTC().Unix(),
	}

	if err := s.setEntry(chartName, statName, entry); err != nil {
		return requestStatEntry{}, err
	}
	return entry, nil
}

func (s *requestStatStore) AddDelta(chartName string, statName string, delta float64, lastObserved float64, observedAt time.Time) (requestStatEntry, error) { // A
	if err := validateFiniteNonNegative("persistent stat delta", delta); err != nil {
		return requestStatEntry{}, err
	}
	if err := validateFiniteNonNegative("persistent stat last observed", lastObserved); err != nil {
		return requestStatEntry{}, err
	}
	if observedAt.IsZero() {
		return requestStatEntry{}, fmt.Errorf("persistent stat observed time must not be zero")
	}

	key := requestStatKey(chartName, statName)

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[key]
	if !ok {
		return requestStatEntry{}, fmt.Errorf("persistent stat entry %q does not exist", key)
	}

	entry.Total += delta
	entry.LastObserved = lastObserved
	entry.LastObservedAt = observedAt.UTC().Unix()
	s.entries[key] = entry
	if err := s.saveLocked(); err != nil {
		return requestStatEntry{}, err
	}

	return entry, nil
}

func (s *requestStatStore) GetEntry(chartName string, statName string) (requestStatEntry, bool) { // A
	key := requestStatKey(chartName, statName)

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[key]
	return entry, ok
}

func (s *requestStatStore) HasEntry(chartName string, statName string) bool { // A
	_, ok := s.GetEntry(chartName, statName)
	return ok
}

func (s *requestStatStore) setEntry(chartName string, statName string, entry requestStatEntry) error { // A
	key := requestStatKey(chartName, statName)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries[key] = entry
	if err := s.saveLocked(); err != nil {
		return err
	}
	return nil
}

func (s *requestStatStore) saveLocked() error { // A
	payload := requestStatSnapshot{Entries: s.entries}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode request stat store %q: %w", s.path, err)
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write request stat store %q: %w", s.path, err)
	}
	return nil
}

func hasPersistentStats(cfg config.Config) bool { // A
	for _, chart := range cfg.Charts {
		for _, stat := range chart.Stats {
			if stat.Persist {
				return true
			}
		}
	}
	return false
}

func latestAggregateMatrixValue(matrix promapi.Matrix) (float64, bool) { // A
	total := 0.0
	hasData := false

	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		total += series.Values[len(series.Values)-1].Value
		hasData = true
	}

	return total, hasData
}

func counterDeltaFromMatrix(matrix promapi.Matrix) (float64, float64, bool) { // A
	totalDelta := 0.0
	lastObserved := 0.0
	hasData := false

	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}

		hasData = true
		prev := series.Values[0].Value
		for i := 1; i < len(series.Values); i++ {
			current := series.Values[i].Value
			if current >= prev {
				totalDelta += current - prev
			} else {
				totalDelta += current
			}
			prev = current
		}

		lastObserved += series.Values[len(series.Values)-1].Value
	}

	return totalDelta, lastObserved, hasData
}

func validateFiniteNonNegative(label string, value float64) error { // A
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be finite", label)
	}
	if value < 0 {
		return fmt.Errorf("%s must be non-negative", label)
	}
	return nil
}

func requestStatKey(chartName string, statName string) string { // A
	return strings.TrimSpace(chartName) + "/" + strings.TrimSpace(statName)
}
