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

const storageSettingsID = "storage"

type storageSettingsDoc struct {
	ID                string `bson:"_id"`
	MaxSessions       int    `bson:"maxSessions"`
	MinDiskSpaceBytes int64  `bson:"minDiskSpaceBytes"`
	UpdatedAt         int64  `bson:"updatedAt"`
}

type StorageSettingsRepository struct {
	collection *mongo.Collection
}

func NewStorageSettingsRepository(client *mongo.Client, dbName string) *StorageSettingsRepository {
	return &StorageSettingsRepository{collection: client.Database(dbName).Collection("settings")}
}

func (r *StorageSettingsRepository) GetStorageSettings(ctx context.Context) (app.StorageSettings, bool, error) {
	var doc storageSettingsDoc
	err := r.collection.FindOne(ctx, bson.M{"_id": storageSettingsID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return app.StorageSettings{}, false, nil
		}
		return app.StorageSettings{}, false, err
	}
	return app.StorageSettings{
		MaxSessions:       doc.MaxSessions,
		MinDiskSpaceBytes: doc.MinDiskSpaceBytes,
	}, true, nil
}

func (r *StorageSettingsRepository) SetStorageSettings(ctx context.Context, settings app.StorageSettings) error {
	update := bson.M{
		"$set": bson.M{
			"maxSessions":       settings.MaxSessions,
			"minDiskSpaceBytes": settings.MinDiskSpaceBytes,
			"updatedAt":         time.Now().Unix(),
		},
	}
	_, err := r.collection.UpdateOne(
		ctx,
		bson.M{"_id": storageSettingsID},
		update,
		options.Update().SetUpsert(true),
	)
	return err
}
