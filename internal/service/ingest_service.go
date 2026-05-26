package service

import (
	"context"
	"fmt"
	"time"

	"github.com/khangtran2403/searchengine/internal/embedding"
	"github.com/khangtran2403/searchengine/internal/models"
	"github.com/khangtran2403/searchengine/internal/repository"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
)

// eventScoreMap — chuyển EventType thành weight cho taste vector update
var eventScoreMap = map[models.EventType]float64{
	models.EventView:   0.7,
	models.EventRating: 0.0, 
	models.EventLike:   1.0,
	models.EventSkip:   -0.3, 
	models.EventSearch: 0.4,
}

type IngestService struct {
	events   *repository.EventRepository
	movies   *repository.MovieRepository
	profiles *repository.ProfileRepository
	embedder *embedding.GeminiClient
	log      *zap.Logger
}

func NewIngestService(
	events *repository.EventRepository,
	movies *repository.MovieRepository,
	profiles *repository.ProfileRepository,
	embedder *embedding.GeminiClient,
	log *zap.Logger,
) *IngestService {
	return &IngestService{events, movies, profiles, embedder, log}
}

// IngestRequest — payload từ client
type IngestRequest struct {
	UserID    string            `json:"user_id"  binding:"required"`
	MovieID   string            `json:"movie_id" binding:"required"`
	EventType models.EventType  `json:"type"     binding:"required"`
	// Chỉ dùng khi type = "rating" (1.0–5.0)
	RatingVal float64           `json:"rating_value"`
	// Context
	Device    string            `json:"device"`
}

// Ingest — lưu event và async update taste vector
func (s *IngestService) Ingest(ctx context.Context, req *IngestRequest) error {
	movieOID, err := primitive.ObjectIDFromHex(req.MovieID)
	if err != nil {
		return fmt.Errorf("invalid movie_id: %w", err)
	}

	// Tính score chuẩn hóa (0.0 - 1.0)
	score := eventScoreMap[req.EventType]
	if req.EventType == models.EventRating && req.RatingVal > 0 {
		score = req.RatingVal / 5.0 
	}

	event := &models.UserEvent{
		UserID:    req.UserID,
		MovieID:   &movieOID,
		Type:      req.EventType,
		Score:     score,
		Device:    models.DeviceType(req.Device),
	}
	
	if req.EventType == models.EventRating {
		val := req.RatingVal
		event.RatingValue = &val
	}

	if err := s.events.Insert(ctx, event); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}

	// Async update taste vector — không block response
	go s.updateTasteVecAsync(req.UserID, movieOID, score)

	return nil
}

// updateTasteVecAsync — chạy trong goroutine riêng sau khi event được lưu.
func (s *IngestService) updateTasteVecAsync(userID string, movieID primitive.ObjectID, weight float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Skip negative events (skip) hoặc tương tác quá yếu — không cần update vector
	if weight < 0.3 {
		s.log.Debug("skip taste update for low weight event", zap.String("user", userID))
		return
	}

	// 1. Lấy thông tin phim để có Embedding và Genres
	movie, err := s.movies.FindByIDs(ctx, []primitive.ObjectID{movieID})
	if err != nil || len(movie) == 0 {
		s.log.Error("movie not found for taste update", zap.Error(err))
		return
	}
	targetMovie := movie[0]

	// 2. Load profile hiện tại
	profile, err := s.profiles.FindByUserID(ctx, userID)
	if err != nil {
		s.log.Error("load profile failed", zap.Error(err))
		return
	}
	if profile == nil {
		profile = &models.UserProfile{
			UserID:       userID,
			GenreWeights: make(map[string]float64),
		}
	}

	// 3. Cập nhật Taste Vector (Moving Average)
	if len(targetMovie.Embedding) > 0 {
		profile.TasteVec = embedding.UpdateTasteVec(profile.TasteVec, targetMovie.Embedding, weight)
	}

	// 4. Cập nhật Genre Weights (Sáng tạo: tăng điểm cộng cho thể loại phim vừa xem)
	for _, g := range targetMovie.Genres {
		profile.GenreWeights[g] += weight * 0.1
		// Normalize/Cap weight
		if profile.GenreWeights[g] > 1.0 {
			profile.GenreWeights[g] = 1.0
		}
	}

	// 5. Lưu profile
	profile.UpdatedAt = time.Now()
	if err := s.profiles.Upsert(ctx, profile); err != nil {
		s.log.Error("update profile failed", zap.Error(err))
		return
	}

	s.log.Info("taste vector and genre weights updated async",
		zap.String("user", userID),
		zap.String("movie", movieID.Hex()),
		zap.Float64("weight", weight),
	)
}