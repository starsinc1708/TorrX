package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/internal/app"
)

const subtitleSettingsID = "subtitle_settings"

type subtitleSettingsDoc struct {
	ID         string   `bson:"_id"`
	Enabled    bool     `bson:"enabled"`
	APIKey     string   `bson:"apiKey"`
	Languages  []string `bson:"languages"`
	AutoSearch bool     `bson:"autoSearch"`
	UpdatedAt  int64    `bson:"updatedAt"`
}

type SubtitleSettingsRepository struct {
	collection *mongo.Collection
}

func NewSubtitleSettingsRepository(client *mongo.Client, dbName string) *SubtitleSettingsRepository {
	return &SubtitleSettingsRepository{
		collection: client.Database(dbName).Collection("settings"),
	}
}

func (r *SubtitleSettingsRepository) GetSubtitleSettings(ctx context.Context) (app.SubtitleSettings, bool, error) {
	var doc subtitleSettingsDoc
	err := r.collection.FindOne(ctx, bson.M{"_id": subtitleSettingsID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return app.SubtitleSettings{}, false, nil
		}
		return app.SubtitleSettings{}, false, err
	}
	return app.SubtitleSettings{
		Enabled:    doc.Enabled,
		APIKey:     doc.APIKey,
		Languages:  doc.Languages,
		AutoSearch: doc.AutoSearch,
	}, true, nil
}

func (r *SubtitleSettingsRepository) SetSubtitleSettings(ctx context.Context, s app.SubtitleSettings) error {
	_, err := r.collection.UpdateOne(
		ctx,
		bson.M{"_id": subtitleSettingsID},
		bson.M{"$set": bson.M{
			"enabled":    s.Enabled,
			"apiKey":     s.APIKey,
			"languages":  s.Languages,
			"autoSearch": s.AutoSearch,
			"updatedAt":  time.Now().Unix(),
		}},
		options.Update().SetUpsert(true),
	)
	return err
}
