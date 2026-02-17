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
	ID               string `bson:"_id"`
	MemBufSizeMB     int    `bson:"memBufSizeMB"`
	CacheSizeMB      int    `bson:"cacheSizeMB"`
	CacheMaxAgeHours int    `bson:"cacheMaxAgeHours"`
	SegmentDuration  int    `bson:"segmentDuration"`
	UpdatedAt        int64  `bson:"updatedAt"`
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
		MemBufSizeMB:     doc.MemBufSizeMB,
		CacheSizeMB:      doc.CacheSizeMB,
		CacheMaxAgeHours: doc.CacheMaxAgeHours,
		SegmentDuration:  doc.SegmentDuration,
	}, true, nil
}

func (r *HLSSettingsRepository) SetHLSSettings(ctx context.Context, settings app.HLSSettings) error {
	update := bson.M{
		"$set": bson.M{
			"memBufSizeMB":     settings.MemBufSizeMB,
			"cacheSizeMB":      settings.CacheSizeMB,
			"cacheMaxAgeHours": settings.CacheMaxAgeHours,
			"segmentDuration":  settings.SegmentDuration,
			"updatedAt":        time.Now().Unix(),
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
