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

const encodingSettingsID = "encoding"

type encodingSettingsDoc struct {
	ID           string `bson:"_id"`
	Preset       string `bson:"preset"`
	CRF          int    `bson:"crf"`
	AudioBitrate string `bson:"audioBitrate"`
	UpdatedAt    int64  `bson:"updatedAt"`
}

type EncodingSettingsRepository struct {
	collection *mongo.Collection
}

func NewEncodingSettingsRepository(client *mongo.Client, dbName string) *EncodingSettingsRepository {
	return &EncodingSettingsRepository{collection: client.Database(dbName).Collection("settings")}
}

func (r *EncodingSettingsRepository) GetEncodingSettings(ctx context.Context) (app.EncodingSettings, bool, error) {
	var doc encodingSettingsDoc
	err := r.collection.FindOne(ctx, bson.M{"_id": encodingSettingsID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return app.EncodingSettings{}, false, nil
		}
		return app.EncodingSettings{}, false, err
	}
	return app.EncodingSettings{
		Preset:       doc.Preset,
		CRF:          doc.CRF,
		AudioBitrate: doc.AudioBitrate,
	}, true, nil
}

func (r *EncodingSettingsRepository) SetEncodingSettings(ctx context.Context, settings app.EncodingSettings) error {
	update := bson.M{
		"$set": bson.M{
			"preset":       settings.Preset,
			"crf":          settings.CRF,
			"audioBitrate": settings.AudioBitrate,
			"updatedAt":    time.Now().Unix(),
		},
	}
	_, err := r.collection.UpdateOne(
		ctx,
		bson.M{"_id": encodingSettingsID},
		update,
		options.Update().SetUpsert(true),
	)
	return err
}
