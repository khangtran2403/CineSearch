package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/khangtran2403/searchengine/internal/embedding"
	"github.com/khangtran2403/searchengine/internal/repository"
	"github.com/joho/godotenv"
)

// seedSources — danh sách nguồn và genre để đảm bảo diversity
var seedSources = []struct {
	name    string
	genreID int 
	pages   int
}{
	{name: "popular",   genreID: 0,   pages: 10},
	{name: "top_rated", genreID: 0,   pages: 10},
	{name: "action",    genreID: 28,  pages: 5},
	{name: "comedy",    genreID: 35,  pages: 5},
	{name: "drama",     genreID: 18,  pages: 5},
	{name: "thriller",  genreID: 53,  pages: 5},
	{name: "horror",    genreID: 27,  pages: 3},
	{name: "scifi",     genreID: 878, pages: 4},
	{name: "romance",   genreID: 10749, pages: 3},
	{name: "animation", genreID: 16,  pages: 3},
}

func main() {
	// ─── Flags ────────────────────────────────────────────────────────────────
	var (
		targetCount  = flag.Int("n", 500, "Số phim muốn seed")
		workers      = flag.Int("workers", 5, "Số concurrent workers")
		statePath    = flag.String("state", ".seed_state.json", "Resume state file")
		withKeywords = flag.Bool("keywords", true, "Fetch keywords từ TMDB (enrich embedding)")
		dryRun       = flag.Bool("dry-run", false, "Chỉ fetch TMDB, không embed/insert")
	)
	flag.Parse()

	godotenv.Load()

	// ─── Validate env ─────────────────────────────────────────────────────────
	tmdbKey   := mustEnv("TMDB_API_KEY")
	geminiKey := mustEnv("GEMINI_API_KEY")
	mongoURI  := mustEnv("MONGO_URI")

	printBanner(*targetCount)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown trên Ctrl+C
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		fmt.Println("\n\n Interrupted — saving state...")
		cancel()
	}()

	tmdbClient := NewTMDBClient(tmdbKey)

	mongoClient, err := repository.NewMongoClient(mongoURI)
	if err != nil {
		fatalf("connect MongoDB: %v", err)
	}
	defer mongoClient.Disconnect(context.Background())

	db         := mongoClient.Database(mustEnv("DB_NAME"))
	movieRepo  := repository.NewMovieRepository(db)
	embedder   := embedding.NewGeminiClient(geminiKey)

	// ─── Load resume state ────────────────────────────────────────────────────
	state, err := LoadOrCreateState(*statePath)
	if err != nil {
		fatalf("load state: %v", err)
	}

	// ─── Load genres từ TMDB một lần ─────────────────────────────────────────
	fmt.Print("📋 Loading TMDB genres... ")
	if err := tmdbClient.LoadGenres(ctx); err != nil {
		fatalf("load genres: %v", err)
	}
	fmt.Printf("✅ %d genres loaded\n\n", len(tmdbClient.genreMap))

	// ─── Setup worker pool ────────────────────────────────────────────────────
	progress := NewProgress(*targetCount, "Seeding")
	// Cập nhật progress với số phim đã seed trước đó
	for i := 0; i < state.TotalSeeded && i < *targetCount; i++ {
		progress.current.Add(1)
	}

	pool := NewWorkerPool(*workers, *workers*3, embedder, movieRepo, state, progress)
	if !*dryRun {
		pool.Start(ctx)
	}

	// ─── Fetch loop ───────────────────────────────────────────────────────────
	seeded := state.TotalSeeded
	stop   := false

	for _, src := range seedSources {
		if stop || ctx.Err() != nil {
			break
		}
		if seeded >= *targetCount {
			break
		}
		if state.IsSourceDone(src.name) {
			fmt.Printf("  ⏭  Skipping %s (already done)\n", src.name)
			continue
		}

		fmt.Printf("\n📥 Source: %-12s ", src.name)

		for page := 1; page <= src.pages; page++ {
			if ctx.Err() != nil {
				stop = true
				break
			}
			if seeded >= *targetCount {
				stop = true
				break
			}

			// Fetch page từ TMDB
			result, err := fetchPage(ctx, tmdbClient, src.genreID, page)
			if err != nil {
				fmt.Printf("\n  ❌ fetch page %d: %v\n", page, err)
				time.Sleep(2 * time.Second) // backoff
				continue
			}
			state.TotalFetched += len(result.Results)

			for _, m := range result.Results {
				if seeded >= *targetCount {
					stop = true
					break
				}
				if state.IsSeeded(m.ID) {
					continue // đã seed rồi, bỏ qua
				}
				if m.Overview == "" || m.VoteCount < 100 {
					continue // quality filter
				}

				genres := tmdbClient.ResolveGenres(m.GenreIDs)

				// Fetch keywords nếu được bật
				var keywords []string
				if *withKeywords && !*dryRun {
					kw, err := tmdbClient.FetchKeywords(ctx, m.ID)
					if err == nil {
						keywords = kw
					}
					time.Sleep(50 * time.Millisecond)
				}

				if *dryRun {
					fmt.Printf("\n  [DRY RUN] %s (%d) genres=%v kw=%d",
						m.Title, ExtractYear(m.ReleaseDate), genres, len(keywords))
					seeded++
					continue
				}

				// Gửi vào worker pool
				pool.Submit(SeedJob{
					Movie:    m,
					Keywords: keywords,
					Genres:   genres,
				})
				seeded++
			}
			time.Sleep(250 * time.Millisecond)
		}

		if !stop {
			state.MarkSourceDone(src.name)
			_ = state.Save()
		}
	}

	// ─── Shutdown ─────────────────────────────────────────────────────────────
	if !*dryRun {
		fmt.Print("\n\n⏳ Waiting for workers to finish...")
		pool.Close()
		progress.Done()
	}

	_ = state.Save()
	printStats(state)

	if ctx.Err() != nil {
		fmt.Printf("💾 State saved to %s — chạy lại để tiếp tục\n\n", *statePath)
	} else {
		fmt.Printf("🎬 Database sẵn sàng! %d phim với embeddings trong MongoDB\n\n", state.TotalSeeded)
		// Clean up state file sau khi hoàn thành
		os.Remove(*statePath)
	}
}

// fetchPage — wrapper gọi đúng endpoint tùy genreID
func fetchPage(ctx context.Context, c *TMDBClient, genreID, page int) (*tmdbPageResult, error) {
	switch genreID {
	case 0:
		// Xen kẽ popular và top_rated để đa dạng hơn
		if page%2 == 0 {
			return c.FetchTopRated(ctx, page/2+1)
		}
		return c.FetchPopular(ctx, (page+1)/2)
	default:
		return c.FetchByGenre(ctx, genreID, page)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fatalf("env %s is required", key)
	}
	return v
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "❌ "+format+"\n", args...)
	os.Exit(1)
}