package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/khangtran2403/searchengine/internal/models"
	"github.com/khangtran2403/searchengine/internal/repository"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const (
	cacheTTL       = 1 * time.Hour
	vectorWeight   = 0.6
	collabWeight   = 0.4
	diversityBoost = 0.15 // bonus cho genres chưa xuất hiện
)

type RecommendService struct {
	movies   *repository.MovieRepository
	events   *repository.EventRepository
	profiles *repository.ProfileRepository
	cache    *redis.Client
	log      *zap.Logger
}

func NewRecommendService(
	movies *repository.MovieRepository,
	events *repository.EventRepository,
	profiles *repository.ProfileRepository,
	cache *redis.Client,
	log *zap.Logger,
) *RecommendService {
	return &RecommendService{movies, events, profiles, cache, log}
}

// Recommend —kết hợp vector + collaborative + context
func (s *RecommendService) Recommend(
	ctx context.Context,
	userID string,
	limit int,
	device string,
) (*models.RecommendResponse, error) {

	// ── 1. Check Redis cache ──────────────────────────────────────────────────
	cacheKey := fmt.Sprintf("recs:%s:%s", userID, device)
	if cached, err := s.cache.Get(ctx, cacheKey).Bytes(); err == nil {
		var resp models.RecommendResponse
		if json.Unmarshal(cached, &resp) == nil {
			s.log.Debug("cache hit", zap.String("user", userID))
			return &resp, nil
		}
	}

	// ── 2. Load user profile ──────────────────────────────────────────────────
	profile, err := s.profiles.FindByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}

	// Phim đã xem → exclude
	excludeIDs, _ := s.events.RecentMovieIDs(ctx, userID, 90)

	var vectorRecs []models.RecommendedMovie
	var collabRecs []models.RecommendedMovie

	// ── 3a. Vector Search (nếu user có taste vector) ──────────────────────────
	if profile != nil && len(profile.TasteVec) > 0 {
		movies, err := s.movies.VectorSearch(ctx, profile.TasteVec, excludeIDs, limit)
		if err != nil {
			s.log.Error("vector search failed", zap.Error(err))
		} else {
			for _, m := range movies {
				reason := buildVectorReason(m, profile)
				vectorRecs = append(vectorRecs, models.RecommendedMovie{
					MovieID:    m.ID,
					Title:      m.Title,
					Genres:     m.Genres,
					PosterPath: m.PosterURL,
					Score:      vectorWeight,
					Source:     []models.RecommendSource{models.SourceVector},
					Reason:     reason,
				})
			}
		}
	}

	// ── 3b. Collaborative Filtering ──────────────────────────────────────────
	collabIDs, collabScores, err := s.events.CollaborativeFilter(ctx, userID, excludeIDs, limit)
	if err != nil {
		s.log.Error("collaborative filter failed", zap.Error(err))
	} else if len(collabIDs) > 0 {
		movieMap := make(map[string]repository.CollabScore)
		for _, cs := range collabScores {
			movieMap[cs.MovieID.Hex()] = cs
		}

		movies, _ := s.movies.FindByIDs(ctx, collabIDs)
		for _, m := range movies {
			cs := movieMap[m.ID.Hex()]
			normalizedScore := min(cs.Score/100.0, 1.0) * collabWeight
			collabRecs = append(collabRecs, models.RecommendedMovie{
				MovieID:    m.ID,
				Title:      m.Title,
				Genres:     m.Genres,
				PosterPath: m.PosterURL,
				Score:      normalizedScore,
				Source:     []models.RecommendSource{models.SourceCollaborative},
				Reason:     fmt.Sprintf("%d users với taste tương tự bạn đều yêu thích phim này", cs.RecommenderCount),
			})
		}
	}

	// ── 4. Cold start fallback: trending ─────────────────────────────────────
	if len(vectorRecs)+len(collabRecs) < limit/2 {
		trending, _ := s.movies.Trending(ctx, limit)
		for _, m := range trending {
			collabRecs = append(collabRecs, models.RecommendedMovie{
				MovieID:    m.ID,
				Title:      m.Title,
				Genres:     m.Genres,
				PosterPath: m.PosterURL,
				Score:      0.3,
				Source:     []models.RecommendSource{models.SourceTrending},
				Reason:     "Đang được nhiều người xem trong tuần này",
			})
		}
	}

	// ── 5. Fusion + Re-ranking ────────────────────────────────────────────────
	merged := fusionRank(vectorRecs, collabRecs, limit, profile)

	// ── 6. Context adjustments ────────────────────────────────────────────────
	hour := time.Now().Hour()
	moodHint := inferMood(hour, profile)
	merged = applyContextBoost(merged, hour, device)

	resp := &models.RecommendResponse{
		UserID: userID,
		Items:  merged,
		Context: models.RecommendContext{
			LocalTime: time.Now().Format("15:04"),
			Device:    device,
			MoodHint:  moodHint,
		},
	}

	// ── 7. Cache kết quả ─────────────────────────────────────────────────────
	if data, err := json.Marshal(resp); err == nil {
		s.cache.Set(ctx, cacheKey, data, cacheTTL)
	}

	return resp, nil
}

// fusionRank — merge vector + collab, dedup, diversity boost
func fusionRank(
	vectorRecs, collabRecs []models.RecommendedMovie,
	limit int,
	profile *models.UserProfile,
) []models.RecommendedMovie {

	seen := make(map[string]bool)
	seenGenres := make(map[string]int)
	var merged []models.RecommendedMovie

	// Nếu cùng một phim xuất hiện ở cả 2 nguồn → boost score
	scoreMap := make(map[string]float64)
	sourceMap := make(map[string][]models.RecommendSource)
	
	allRaw := append(vectorRecs, collabRecs...)
	for _, r := range allRaw {
		id := r.MovieID.Hex()
		scoreMap[id] += r.Score
		sourceMap[id] = append(sourceMap[id], r.Source...)
	}

	// Loại bỏ trùng lặp và áp dụng boost cho phim từ nhiều nguồn
	var deduped []models.RecommendedMovie
	tempSeen := make(map[string]bool)
	for _, r := range allRaw {
		id := r.MovieID.Hex()
		if tempSeen[id] {
			continue
		}
		tempSeen[id] = true
		
		finalScore := scoreMap[id]
		if len(sourceMap[id]) > 1 {
			finalScore *= 1.2 // cross-source boost
			r.Reason += " · Khớp cả hai thuật toán gợi ý"
		}
		r.Score = finalScore
		r.Source = sourceMap[id]
		deduped = append(deduped, r)
	}

	// Sort by score
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Score > deduped[j].Score
	})

	for _, rec := range deduped {
		id := rec.MovieID.Hex()
		if seen[id] {
			continue
		}

		// Diversity boost: ưu tiên genres chưa xuất hiện nhiều
		for _, g := range rec.Genres {
			if seenGenres[g] == 0 {
				rec.Score += diversityBoost
			}
			seenGenres[g]++
		}

		seen[id] = true
		merged = append(merged, rec)

		if len(merged) >= limit {
			break
		}
	}

	return merged
}

// applyContextBoost — điều chỉnh score dựa trên giờ và thiết bị
func applyContextBoost(recs []models.RecommendedMovie, hour int, device string) []models.RecommendedMovie {
	for i := range recs {
		for _, g := range recs[i].Genres {
			// Tối khuya → horror/thriller lên
			if hour >= 22 || hour <= 2 {
				if g == "Horror" || g == "Thriller" {
					recs[i].Score += 0.1
				}
			}
			// Buổi sáng → comedy/family lên
			if hour >= 7 && hour <= 10 {
				if g == "Comedy" || g == "Family" || g == "Animation" {
					recs[i].Score += 0.1
				}
			}
		}
		// Mobile boost cho phim có score cao (thay vì rating)
		if device == "mobile" && recs[i].Score >= 0.8 {
			recs[i].Score += 0.05
		}
	}
	return recs
}

// inferMood — đoán tâm trạng từ giờ xem
func inferMood(hour int, profile *models.UserProfile) string {
	if hour >= 22 || hour <= 2 {
		return "Night owl — thử thriller hoặc mystery?"
	}
	if hour >= 7 && hour <= 10 {
		return "Buổi sáng — comedy hay documentary nhẹ nhàng?"
	}
	return "Giờ vàng — bộ phim hay đang chờ bạn"
}

// buildVectorReason — giải thích tại sao vector search trả về phim này
func buildVectorReason(m models.Movie, profile *models.UserProfile) string {
	if profile == nil || len(profile.GenreWeights) == 0 {
		return fmt.Sprintf("Phù hợp với gu xem phim của bạn")
	}
	topGenre := ""
	topWeight := 0.0
	for g, w := range profile.GenreWeights {
		if w > topWeight {
			topWeight = w
			topGenre = g
		}
	}
	if topGenre != "" {
		return fmt.Sprintf("Vì bạn thích %s (%.0f%% affinity)", topGenre, topWeight*100)
	}
	return "Phù hợp với lịch sử xem phim của bạn"
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}