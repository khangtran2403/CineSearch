package repository

import (
	"context"
	"time"

	"github.com/khangtran2403/searchengine/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MovieRepository struct {
	col *mongo.Collection
}

func NewMovieRepository(db *mongo.Database) *MovieRepository {
	return &MovieRepository{col: db.Collection("movies")}
}

// VectorSearch — tìm phim gần nhất với taste vector của user.
// Index "movie_embedding_index" phải được tạo trên Atlas UI hoặc qua Atlas CLI:
//
//   {
//     "fields": [{
//       "type": "vector",
//       "path": "embedding",
//       "numDimensions": 1536,
//       "similarity": "cosine"
//     }]
//   }
func (r *MovieRepository) VectorSearch(
	ctx context.Context,
	queryVec []float32,
	excludeIDs []primitive.ObjectID,
	limit int,
) ([]models.Movie, error) {
	pipeline := mongo.Pipeline{
		// Stage 1: $vectorSearch — Atlas Vector Search
		{{Key: "$vectorSearch", Value: bson.D{
			{Key: "index", Value: "movie_embedding_index"},
			{Key: "path", Value: "embedding"},
			{Key: "queryVector", Value: queryVec},
			{Key: "numCandidates", Value: limit * 10}, // over-fetch để filter
			{Key: "limit", Value: limit * 2},
			// filter loại trừ phim đã xem
			{Key: "filter", Value: bson.D{
				{Key: "_id", Value: bson.D{
					{Key: "$nin", Value: excludeIDs},
				}},
			}},
		}}},

		// Stage 2: Thêm score từ vector search
		{{Key: "$addFields", Value: bson.D{
			{Key: "vector_score", Value: bson.D{
				{Key: "$meta", Value: "vectorSearchScore"},
			}},
		}}},

		// Stage 3: Chỉ lấy phim có score cao
		{{Key: "$match", Value: bson.D{
			{Key: "vector_score", Value: bson.D{{Key: "$gte", Value: 0.75}}},
		}}},

		{{Key: "$limit", Value: limit}},
	}

	cursor, err := r.col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []models.Movie
	return results, cursor.All(ctx, &results)
}

// FindByIDs — batch fetch movies theo list IDs (dùng sau collaborative filtering)
func (r *MovieRepository) FindByIDs(
	ctx context.Context,
	ids []primitive.ObjectID,
) ([]models.Movie, error) {
	filter := bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}
	cursor, err := r.col.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var movies []models.Movie
	return movies, cursor.All(ctx, &movies)
}

// Upsert — thêm hoặc cập nhật movie (dùng khi seed data từ TMDB)
func (r *MovieRepository) Upsert(ctx context.Context, m *models.Movie) error {
	filter := bson.D{{Key: "tmdb_id", Value: m.TMDBID}}
	update := bson.D{{Key: "$set", Value: m}}
	opts := options.Update().SetUpsert(true)
	_, err := r.col.UpdateOne(ctx, filter, update, opts)
	return err
}

// Trending — top phim theo rating x số lượt xem trong 7 ngày
func (r *MovieRepository) Trending(ctx context.Context, limit int) ([]models.Movie, error) {
	sevenDaysAgo := time.Now().AddDate(0, 0, -7)
	pipeline := mongo.Pipeline{
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: "events"},
			{Key: "localField", Value: "_id"},
			{Key: "foreignField", Value: "movie_id"},
			{Key: "as", Value: "recent_events"},
			{Key: "pipeline", Value: mongo.Pipeline{
				{{Key: "$match", Value: bson.D{
					{Key: "created_at", Value: bson.D{{Key: "$gte", Value: sevenDaysAgo}}},
					{Key: "type", Value: bson.D{{Key: "$in", Value: []string{"view", "rating", "like"}}}},
				}}},
			}},
		}}},
		{{Key: "$addFields", Value: bson.D{
			{Key: "trend_score", Value: bson.D{
				{Key: "$add", Value: bson.A{
					bson.D{{Key: "$multiply", Value: bson.A{
						bson.D{{Key: "$size", Value: "$recent_events"}}, 0.4,
					}}},
					bson.D{{Key: "$multiply", Value: bson.A{"$rating", 0.6}}},
				}},
			}},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "trend_score", Value: -1}}}},
		{{Key: "$limit", Value: limit}},
	}

	cursor, err := r.col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var movies []models.Movie
	return movies, cursor.All(ctx, &movies)
}