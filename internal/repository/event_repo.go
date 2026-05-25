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

type EventRepository struct {
	col *mongo.Collection
}

func NewEventRepository(db *mongo.Database) *EventRepository {
	return &EventRepository{col: db.Collection("events")}
}

func (r *EventRepository) Insert(ctx context.Context, e *models.UserEvent) error {
	e.CreatedAt = time.Now()
	_, err := r.col.InsertOne(ctx, e)
	return err
}

// RecentMovieIDs — get movieID list that user has interacted with (to exclude from recommendations)
func (r *EventRepository) RecentMovieIDs(
	ctx context.Context,
	userID string,
	days int,
) ([]primitive.ObjectID, error) {
	since := time.Now().AddDate(0, 0, -days)
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.D{
			{Key: "user_id", Value: userID},
			{Key: "created_at", Value: bson.D{{Key: "$gte", Value: since}}},
		}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$movie_id"},
		}}},
	}

	cursor, err := r.col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}

	ids := make([]primitive.ObjectID, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	return ids, nil
}

// CollaborativeFilter — Aggregation Pipeline finds similar users and collects their watched movies.

func (r *EventRepository) CollaborativeFilter(
	ctx context.Context,
	userID string,
	excludeIDs []primitive.ObjectID,
	limit int,
) ([]primitive.ObjectID, []CollabScore, error) {

	pipeline := mongo.Pipeline{
		// Step 1: get movies that target user has rated >= 0.5 (liked movies)
		{{Key: "$match", Value: bson.D{
			{Key: "user_id", Value: userID},
			{Key: "score", Value: bson.D{{Key: "$gte", Value: 0.5}}},
		}}},

		// Step 2: group into a set of movie_ids for the target user
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$user_id"},
			{Key: "liked_movies", Value: bson.D{{Key: "$addToSet", Value: "$movie_id"}}},
		}}},

		// Step 3: find other users who have watched those movies
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: "events"},
			{Key: "localField", Value: "liked_movies"},
			{Key: "foreignField", Value: "movie_id"},
			{Key: "as", Value: "similar_events"},
			{Key: "pipeline", Value: mongo.Pipeline{
				{{Key: "$match", Value: bson.D{
					{Key: "user_id", Value: bson.D{{Key: "$ne", Value: userID}}},
					{Key: "score", Value: bson.D{{Key: "$gte", Value: 0.5}}},
					// only consider recent interactions to optimize perfomance
					{Key: "created_at", Value: bson.D{{Key: "$gte",Value: time.Now().AddDate(0, 0, -30)}}},
				}}},
			}},
		}}},

		// Step 4: unwind events of similar users
		{{Key: "$unwind", Value: "$similar_events"}},

		// Step 5: group by similar user → compute similarity score (overlap count)
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$similar_events.user_id"},
			{Key: "similarity", Value: bson.D{{Key: "$sum", Value: 1}}},
			{Key: "their_movies", Value: bson.D{
				{Key: "$addToSet", Value: "$similar_events.movie_id"},
			}},
		}}},

		// Step 6:only getting users with enough overlap (avoid noise)
		{{Key: "$match", Value: bson.D{
			{Key: "similarity", Value: bson.D{{Key: "$gte", Value: 3}}},
		}}},

		// Step 7: sort by similarity
		{{Key: "$sort", Value: bson.D{{Key: "similarity", Value: -1}}}},
		{{Key: "$limit", Value: 20}}, // top 20 similar users

		// Step 8: unwind movie list of each similar user
		{{Key: "$unwind", Value: "$their_movies"}},

		// Step 9: exclude movies already watched by target user
		{{Key: "$match", Value: bson.D{
			{Key: "their_movies", Value: bson.D{{Key: "$nin", Value: excludeIDs}}},
		}}},

		// Step 10: group by movieID, calculate similarity score
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$their_movies"},
			{Key: "collab_score", Value: bson.D{{Key: "$sum", Value: "$similarity"}}},
			{Key: "recommender_count", Value: bson.D{{Key: "$sum", Value: 1}}},
		}}},

		{{Key: "$sort", Value: bson.D{{Key: "collab_score", Value: -1}}}},
		{{Key: "$limit", Value: limit}},
	}

	cursor, err := r.col.Aggregate(ctx, pipeline,
		options.Aggregate().SetAllowDiskUse(true), 
	)
	if err != nil {
		return nil, nil, err
	}
	defer cursor.Close(ctx)

	type result struct {
		ID               primitive.ObjectID `bson:"_id"`
		CollabScore      float64            `bson:"collab_score"`
		RecommenderCount int                `bson:"recommender_count"`
	}

	var rows []result
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, nil, err
	}

	ids := make([]primitive.ObjectID, len(rows))
	scores := make([]CollabScore, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		scores[i] = CollabScore{
			MovieID:          row.ID,
			Score:            row.CollabScore,
			RecommenderCount: row.RecommenderCount,
		}
	}
	return ids, scores, nil
}

type CollabScore struct {
	MovieID          primitive.ObjectID
	Score            float64
	RecommenderCount int
}