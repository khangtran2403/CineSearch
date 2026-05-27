package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/khangtran2403/searchengine/internal/embedding"
)

const (
	tmdbBase    = "https://api.themoviedb.org/3"
	tmdbImgBase = "https://image.tmdb.org/t/p/w500"
)

// ─── TMDB API Types ───────────────────────────────────────────────────────────

type tmdbGenre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type tmdbMovie struct {
	ID               int         `json:"id"`
	Title            string      `json:"title"`
	Overview         string      `json:"overview"`
	ReleaseDate      string      `json:"release_date"`
	VoteAverage      float64     `json:"vote_average"`
	VoteCount        int         `json:"vote_count"`
	PosterPath       string      `json:"poster_path"`
	GenreIDs         []int       `json:"genre_ids"`
	Genres           []tmdbGenre `json:"genres"` // chỉ có khi fetch detail
	OriginalLanguage string      `json:"original_language"`
	Popularity       float64     `json:"popularity"`
}

type tmdbPageResult struct {
	Page         int         `json:"page"`
	Results      []tmdbMovie `json:"results"`
	TotalPages   int         `json:"total_pages"`
	TotalResults int         `json:"total_results"`
}

type tmdbKeywords struct {
	Keywords []struct {
		Name string `json:"name"`
	} `json:"keywords"`
}

// ─── TMDB Client ──────────────────────────────────────────────────────────────

type TMDBClient struct {
	apiKey     string
	httpClient *http.Client
	genreMap   map[int]string // id → name, populated once
}

func NewTMDBClient(apiKey string) *TMDBClient {
	return &TMDBClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		genreMap: make(map[int]string),
	}
}

// LoadGenres — fetch genre list một lần, cache vào genreMap
func (c *TMDBClient) LoadGenres(ctx context.Context) error {
	url := fmt.Sprintf("%s/genre/movie/list?api_key=%s&language=en-US", tmdbBase, c.apiKey)
	var result struct {
		Genres []tmdbGenre `json:"genres"`
	}
	if err := c.get(ctx, url, &result); err != nil {
		return fmt.Errorf("load genres: %w", err)
	}
	for _, g := range result.Genres {
		c.genreMap[g.ID] = g.Name
	}
	return nil
}

// FetchPopular — lấy popular movies, nhiều trang.
// Dùng để seed dataset ban đầu (~500–1000 phim).
func (c *TMDBClient) FetchPopular(ctx context.Context, page int) (*tmdbPageResult, error) {
	url := fmt.Sprintf(
		"%s/movie/popular?api_key=%s&language=en-US&page=%d",
		tmdbBase, c.apiKey, page,
	)
	var result tmdbPageResult
	return &result, c.get(ctx, url, &result)
}

// FetchTopRated — top rated movies (chất lượng hơn popular)
func (c *TMDBClient) FetchTopRated(ctx context.Context, page int) (*tmdbPageResult, error) {
	url := fmt.Sprintf(
		"%s/movie/top_rated?api_key=%s&language=en-US&page=%d",
		tmdbBase, c.apiKey, page,
	)
	var result tmdbPageResult
	return &result, c.get(ctx, url, &result)
}

// FetchByGenre — lấy phim theo genre cụ thể (để đảm bảo diversity)
// genreID: 28=Action, 35=Comedy, 27=Horror, 878=Sci-Fi, 18=Drama, 53=Thriller
func (c *TMDBClient) FetchByGenre(ctx context.Context, genreID, page int) (*tmdbPageResult, error) {
	url := fmt.Sprintf(
		"%s/discover/movie?api_key=%s&language=en-US&with_genres=%d&sort_by=vote_count.desc&vote_count.gte=500&page=%d",
		tmdbBase, c.apiKey, genreID, page,
	)
	var result tmdbPageResult
	return &result, c.get(ctx, url, &result)
}

// FetchKeywords — lấy keywords của phim (dùng để enrich embedding text)
func (c *TMDBClient) FetchKeywords(ctx context.Context, movieID int) ([]string, error) {
	url := fmt.Sprintf("%s/movie/%d/keywords?api_key=%s", tmdbBase, movieID, c.apiKey)
	var result tmdbKeywords
	if err := c.get(ctx, url, &result); err != nil {
		return nil, err
	}
	words := make([]string, 0, len(result.Keywords))
	for _, k := range result.Keywords {
		words = append(words, k.Name)
	}
	return words, nil
}

// ResolveGenres — chuyển genre IDs → names dùng genreMap
func (c *TMDBClient) ResolveGenres(ids []int) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := c.genreMap[id]; ok {
			names = append(names, name)
		}
	}
	return names
}

// PosterURL — build full poster URL
func (c *TMDBClient) PosterURL(path string) string {
	if path == "" {
		return ""
	}
	return tmdbImgBase + path
}

// ExtractYear — "2023-07-15" → 2023
func ExtractYear(releaseDate string) int {
	if len(releaseDate) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(releaseDate[:4])
	return y
}

// BuildEmbedText — Re-use logic from central embedding package for consistency
func BuildEmbedText(title string, genres []string, overview string, keywords []string) string {
	return embedding.MovieText(title, genres, overview, keywords)
}

// ─── HTTP helper with Retry Logic ───────────────────────────────────────────

func (c *TMDBClient) get(ctx context.Context, url string, out interface{}) error {
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("http get: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			// Exponential backoff or simple sleep for hackathon
			time.Sleep(2 * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("tmdb status %d for %s", resp.StatusCode, url)
		}

		return json.NewDecoder(resp.Body).Decode(out)
	}
	return fmt.Errorf("failed after %d retries due to rate limit", maxRetries)
}