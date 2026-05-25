package models
import(
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Movie struct {
	ID          primitive.ObjectID `bson:"_id,omitempty"    json:"id"`
	TMDBID      int                `bson:"tmdb_id"          json:"tmdb_id"`
	Title       string             `bson:"title"            json:"title"`
	Overview    string             `bson:"overview"         json:"overview"`
	ReleaseDate time.Time          `bson:"release_date,omitempty" json:"release_date,omitempty"`
	Genres      []string           `bson:"genres"           json:"genres"`
	Year        int                `bson:"year"             json:"year"`
	Rating      float64            `bson:"rating"           json:"rating"`
	PosterURL   string             `bson:"poster_url"       json:"poster_url"`
	// 1536-dim vector
	Embedding   []float32          `bson:"embedding"        json:"-"`
	SearchText string              `bson:"search_text,omitempty" json:"-"`
	CreatedAt   time.Time          `bson:"created_at"       json:"created_at"`
	UpdatedAt time.Time            `bson:"updated_at"       json:"updated_at"`
}
// UserEvent
// Mọi hành vi đều được ghi lại — view, rating, skip, search, like
 
type EventType string
 
const (
	EventView   EventType = "view"    // watched ≥ 70%
	EventRating EventType = "rating"  // explicit star rating
	EventSkip   EventType = "skip"    // skipped < 10 min
	EventSearch EventType = "search"
	EventLike   EventType = "like"
)
type DeviceType string

const (
	DeviceMobile  DeviceType = "mobile"
	DeviceDesktop DeviceType = "desktop"
	DeviceTV      DeviceType = "tv"
)

 
type UserEvent struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID    string             `bson:"user_id"       json:"user_id"`
	MovieID   *primitive.ObjectID `bson:"movie_id,omitempty"      json:"movie_id,omitempty"`
	Type      EventType          `bson:"type"          json:"type"`
	RatingValue *float64         `bson:"rating_value,omitempty"`
    WatchSeconds    int          `bson:"watch_seconds,omitempty"`
	DurationSeconds int          `bson:"duration_seconds,omitempty"`
	SearchQuery string           `bson:"search_query,omitempty" json:"search_query,omitempty"`
	Device    DeviceType         `bson:"device"        json:"device"` // mobile|desktop|tv
	SessionID string             `bson:"session_id,omitempty"`
	CreatedAt time.Time          `bson:"created_at"    json:"created_at"`
}
//User Profile
type UserProfile struct {
	ID     primitive.ObjectID `bson:"_id,omitempty"`
	UserID string             `bson:"user_id"`
	// User embedding
	TasteVec []float32 `bson:"taste_vec,omitempty" json:"-"`
	EmbeddingModel string `bson:"embedding_model,omitempty"`
	EmbeddingDim   int    `bson:"embedding_dim,omitempty"`
	GenreWeights map[string]float64 `bson:"genre_weights,omitempty"`
	FavoriteGenres []string `bson:"favorite_genres,omitempty"`
	RecentMovieIDs []primitive.ObjectID `bson:"recent_movie_ids,omitempty"`
	TotalWatchTime int `bson:"total_watch_time,omitempty"`
	LastActiveAt time.Time `bson:"last_active_at,omitempty"`
	UpdatedAt time.Time `bson:"updated_at"`
}
//Recommendation
 
type RecommendSource string
 
const (
	SourceVector        RecommendSource = "vector_search"
	SourceCollaborative RecommendSource = "collaborative"
	SourceTrending      RecommendSource = "trending"
)
 
type RecommendedMovie struct {
    MovieID primitive.ObjectID `json:"movie_id"`
	Title      string   `json:"title"`
	Genres     []string `json:"genres"`
	PosterPath string   `json:"poster_path"`
	Score       float64         `json:"score"`
	Source      []RecommendSource `json:"source"`
	// "Explain layer" — tại sao phim này được gợi ý
	Reason      string          `json:"reason"`
}
 
type RecommendResponse struct {
	UserID  string             `json:"user_id"`
	Items   []RecommendedMovie `json:"items"`
	Context RecommendContext   `json:"context"`
}
 
type RecommendContext struct {
	LocalTime string    `json:"local_time"`
	Device    string `json:"device"`
	MoodHint  string `json:"mood_hint"` // infer từ lịch sử gần nhất
}

