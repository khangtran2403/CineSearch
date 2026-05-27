package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)
// SeedState — lưu trạng thái seeding vào file JSON.
type SeedState struct {
	mu sync.Mutex

	StartedAt    time.Time      `json:"started_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	TotalFetched int            `json:"total_fetched"`
	TotalSeeded  int            `json:"total_seeded"`
	TotalFailed  int            `json:"total_failed"`
	// tmdb_id → true nếu đã seed thành công
	SeededIDs    map[int]bool   `json:"seeded_ids"`
	// source_name → true nếu đã hoàn thành
	FinishedSources map[string]bool `json:"finished_sources"`

	filePath string
}

func LoadOrCreateState(path string) (*SeedState, error) {
	s := &SeedState{
		filePath:        path,
		SeededIDs:       make(map[int]bool),
		FinishedSources: make(map[string]bool),
		StartedAt:       time.Now(),
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil // fresh start
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}

	fmt.Printf("🔄 Resuming from previous run — %d phim đã seed\n", s.TotalSeeded)
	return s, nil
}

func (s *SeedState) IsSeeded(tmdbID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.SeededIDs[tmdbID]
}

func (s *SeedState) MarkSeeded(tmdbID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.SeededIDs[tmdbID] {
		s.SeededIDs[tmdbID] = true
		s.TotalSeeded++
		s.TotalFetched++
		s.UpdatedAt = time.Now()
	}
}

func (s *SeedState) MarkFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalFailed++
	s.TotalFetched++
}

func (s *SeedState) MarkSourceDone(source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FinishedSources[source] = true
}

func (s *SeedState) IsSourceDone(source string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.FinishedSources[source]
}
func (s *SeedState) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}