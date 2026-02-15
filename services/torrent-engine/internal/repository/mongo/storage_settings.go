package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const storageSettingsID = "storage"

type storageSettingsDoc struct {
	ID               string `bson:"_id"`
	MemoryLimitBytes int64  `bson:"memoryLimitBytes"`
	UpdatedAt        int64  `bson:"updatedAt"`
}

type StorageSettingsRepository struct {
	collection *mongo.Collection
}

func NewStorageSettingsRepository(client *mongo.Client, dbName string) *StorageSettingsRepository {
	return &StorageSettingsRepository{collection: client.Database(dbName).Collection("settings")}
}

func (r *StorageSettingsRepository) GetMemoryLimitBytes(ctx context.Context) (int64, bool, error) {
	var doc storageSettingsDoc
	err := r.collection.FindOne(ctx, bson.M{"_id": storageSettingsID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return doc.MemoryLimitBytes, true, nil
}

func (r *StorageSettingsRepository) SetMemoryLimitBytes(ctx context.Context, limit int64) error {
	update := bson.M{
		"$set": bson.M{
			"memoryLimitBytes": limit,
			"updatedAt":        time.Now().Unix(),
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
