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

const hlsSettingsID = "hls"

type hlsSettingsDoc struct {
	ID              string `bson:"_id"`
	SegmentDuration int    `bson:"segmentDuration"`
	RAMBufSizeMB    int    `bson:"ramBufSizeMB"`
	PrebufferMB     int    `bson:"prebufferMB"`
	WindowBeforeMB  int    `bson:"windowBeforeMB"`
	WindowAfterMB   int    `bson:"windowAfterMB"`
	UpdatedAt       int64  `bson:"updatedAt"`
}

type HLSSettingsRepository struct {
	collection *mongo.Collection
}

func NewHLSSettingsRepository(client *mongo.Client, dbName string) *HLSSettingsRepository {
	return &HLSSettingsRepository{collection: client.Database(dbName).Collection("settings")}
}

func (r *HLSSettingsRepository) GetHLSSettings(ctx context.Context) (app.HLSSettings, bool, error) {
	var doc hlsSettingsDoc
	err := r.collection.FindOne(ctx, bson.M{"_id": hlsSettingsID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return app.HLSSettings{}, false, nil
		}
		return app.HLSSettings{}, false, err
	}
	return app.HLSSettings{
		SegmentDuration: doc.SegmentDuration,
		RAMBufSizeMB:    doc.RAMBufSizeMB,
		PrebufferMB:     doc.PrebufferMB,
		WindowBeforeMB:  doc.WindowBeforeMB,
		WindowAfterMB:   doc.WindowAfterMB,
	}, true, nil
}

func (r *HLSSettingsRepository) SetHLSSettings(ctx context.Context, settings app.HLSSettings) error {
	update := bson.M{
		"$set": bson.M{
			"segmentDuration": settings.SegmentDuration,
			"ramBufSizeMB":    settings.RAMBufSizeMB,
			"prebufferMB":     settings.PrebufferMB,
			"windowBeforeMB":  settings.WindowBeforeMB,
			"windowAfterMB":   settings.WindowAfterMB,
			"updatedAt":       time.Now().Unix(),
		},
	}
	_, err := r.collection.UpdateOne(
		ctx,
		bson.M{"_id": hlsSettingsID},
		update,
		options.Update().SetUpsert(true),
	)
	return err
}
