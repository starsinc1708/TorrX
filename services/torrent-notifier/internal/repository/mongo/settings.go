package mongorepo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/notifier/internal/domain"
)

const docID = "integrations"

type SettingsRepository struct {
	col *mongo.Collection
}

func NewSettingsRepository(db *mongo.Database) *SettingsRepository {
	return &SettingsRepository{col: db.Collection("settings")}
}

// Get returns current settings, or defaults if the document does not exist.
func (r *SettingsRepository) Get(ctx context.Context) (domain.IntegrationSettings, error) {
	var doc struct {
		domain.IntegrationSettings `bson:",inline"`
	}
	err := r.col.FindOne(ctx, bson.M{"_id": docID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.IntegrationSettings{}, nil
	}
	if err != nil {
		return domain.IntegrationSettings{}, err
	}
	return doc.IntegrationSettings, nil
}

// Upsert saves settings, setting UpdatedAt to now.
func (r *SettingsRepository) Upsert(ctx context.Context, s domain.IntegrationSettings) error {
	_, err := r.col.UpdateOne(
		ctx,
		bson.M{"_id": docID},
		bson.M{"$set": bson.M{
			"jellyfin":  s.Jellyfin,
			"emby":      s.Emby,
			"qbt":       s.QBT,
			"updatedAt": time.Now().UnixMilli(),
		}},
		options.Update().SetUpsert(true),
	)
	return err
}
