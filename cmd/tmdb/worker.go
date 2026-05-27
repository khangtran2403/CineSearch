package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/khangtran2403/searchengine/internal/embedding"
	"github.com/khangtran2403/searchengine/internal/models"
	"github.com/khangtran2403/searchengine/internal/repository"
)

// SeedJob — một phim cần được embed và insert
type SeedJob struct {
	Movie     tmdbMovie
	Keywords  []string
	Genres    []string
}

// WorkerPool — xử lý concurrent embedding và upsert
// dùng 5 workers × batch 20 = 100 req/batch, delay 300ms giữa batches
type WorkerPool struct {
	workers    int
	jobs       chan SeedJob
	embedder   *embedding.GeminiClient
	movieRepo  *repository.MovieRepository
	state      *SeedState
	progress   *Progress
	wg         sync.WaitGroup
	rateTicker *time.Ticker
	rateLimit  chan struct{}
}

func NewWorkerPool(
	workers int,
	bufSize int,
	embedder *embedding.GeminiClient,
	movieRepo *repository.MovieRepository,
	state *SeedState,
	progress *Progress,
) *WorkerPool {
	rateTicker := time.NewTicker(time.Second / 20)
	rateLimit := make(chan struct{}, workers)

	// Fill token bucket
	go func() {
		for range rateTicker.C {
			select {
			case rateLimit <- struct{}{}:
			default:
			}
		}
	}()

	return &WorkerPool{
		workers:    workers,
		jobs:       make(chan SeedJob, bufSize),
		embedder:   embedder,
		movieRepo:  movieRepo,
		state:      state,
		progress:   progress,
		rateTicker: rateTicker,
		rateLimit:  rateLimit,
	}
}

// Start — khởi động worker goroutines
func (p *WorkerPool) Start(ctx context.Context) {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}
}

// Submit — gửi job vào queue (blocking nếu queue đầy)
func (p *WorkerPool) Submit(job SeedJob) {
	p.jobs <- job
}

// Close — đóng queue và chờ tất cả workers xong
func (p *WorkerPool) Close() {
	close(p.jobs)
	p.wg.Wait()
	p.rateTicker.Stop()
}

// worker — process jobs từ channel
func (p *WorkerPool) worker(ctx context.Context, id int) {
	defer p.wg.Done()

	for job := range p.jobs {
		if ctx.Err() != nil {
			return
		}

		if err := p.processJob(ctx, job); err != nil {
			fmt.Printf("\n  ⚠️  worker %d: %s (tmdb %d): %v\n",
				id, job.Movie.Title, job.Movie.ID, err)
			p.state.MarkFailed()
		}

		p.progress.Inc()
	}
}

// processJob — embed một phim và upsert vào MongoDB
func (p *WorkerPool) processJob(ctx context.Context, job SeedJob) error {
	// Wait for rate limit token
	select {
	case <-p.rateLimit:
	case <-ctx.Done():
		return ctx.Err()
	}

	// Build rich text cho embedding
	embedText := BuildEmbedText(
		job.Movie.Title,
		job.Genres,
		job.Movie.Overview,
		job.Keywords,
	)

	// Embed với retry (Gemini có thể timeout đôi khi)
	vec, err := embedWithRetry(ctx, p.embedder, embedText, 3)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}

	// Build movie document
	movie := &models.Movie{
		TMDBID:    job.Movie.ID,
		Title:     job.Movie.Title,
		Overview:  job.Movie.Overview,
		Genres:    job.Genres,
		Year:      ExtractYear(job.Movie.ReleaseDate),
		Rating:    job.Movie.VoteAverage,
		PosterURL: tmdbImgBase + job.Movie.PosterPath,
		Embedding: vec,
	}

	// Upsert vào MongoDB (tmdb_id là filter key trong repo.Upsert)
	if err := p.movieRepo.Upsert(ctx, movie); err != nil {
		return err
	}

	p.state.MarkSeeded(job.Movie.ID)

	// Save state mỗi 50 phim để resume an toàn
	if p.state.TotalSeeded%50 == 0 {
		_ = p.state.Save()
	}

	return nil
}

// embedWithRetry — retry với exponential backoff khi gặp rate limit hoặc timeout
func embedWithRetry(ctx context.Context, client *embedding.GeminiClient, text string, maxRetries int) ([]float32, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(1<<attempt) * 500 * time.Millisecond // 1s, 2s, 4s
			fmt.Printf("\n  ↩️  retry %d/%d sau %s...", attempt, maxRetries, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		vec, err := client.Embed(ctx, text, "RETRIEVAL_DOCUMENT")
		if err == nil {
			return vec, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("sau %d retries: %w", maxRetries, lastErr)
}