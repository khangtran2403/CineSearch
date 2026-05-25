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

type ProfileRepository struct {
	col *mongo.Collection
}

func NewProfileRepository(db *mongo.Database) *ProfileRepository {
	return &ProfileRepository{col: db.Collection("user_profiles")}
}

func (r *ProfileRepository) FindByUserID(ctx context.Context, userID string) (*models.UserProfile, error) {
	var p models.UserProfile
	err := r.col.FindOne(ctx, bson.D{{Key: "user_id", Value: userID}}).Decode(&p)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &p, err
}

func (r *ProfileRepository) Upsert(ctx context.Context, p *models.UserProfile) error {
	p.UpdatedAt = time.Now()
	filter := bson.D{{Key: "user_id", Value: p.UserID}}
	update := bson.D{{Key: "$set", Value: p}}
	opts := options.Update().SetUpsert(true)
	_, err := r.col.UpdateOne(ctx, filter, update, opts)
	return err
}

// AppendWatchHistory — push movieID into history, limit the most recent 500 items.
func (r *ProfileRepository) AppendWatchHistory(
	ctx context.Context,
	userID string,
	movieID primitive.ObjectID,
) error {
	filter := bson.D{{Key: "user_id", Value: userID}}
	update := bson.D{
		{Key: "$push", Value: bson.D{
			{Key: "watch_history", Value: bson.D{
				{Key: "$each", Value: bson.A{movieID}},
				{Key: "$slice", Value: -500}, // keep only the most recent 500 items
			}},
		}},
		{Key: "$set", Value: bson.D{{Key: "updated_at", Value: time.Now()}}},
	}
	opts := options.Update().SetUpsert(true)
	_, err := r.col.UpdateOne(ctx, filter, update, opts)
	return err
}