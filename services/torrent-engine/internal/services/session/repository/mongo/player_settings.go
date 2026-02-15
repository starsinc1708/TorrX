package mongo

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/internal/domain"
)

const playerSettingsID = "player"

type playerSettingsDoc struct {
	ID               string `bson:"_id"`
	CurrentTorrentID string `bson:"currentTorrentId"`
	UpdatedAt        int64  `bson:"updatedAt"`
}

type PlayerSettingsRepository struct {
	collection *mongo.Collection
}

func NewPlayerSettingsRepository(client *mongo.Client, dbName string) *PlayerSettingsRepository {
	return &PlayerSettingsRepository{collection: client.Database(dbName).Collection("settings")}
}

func (r *PlayerSettingsRepository) GetCurrentTorrentID(ctx context.Context) (domain.TorrentID, bool, error) {
	var doc playerSettingsDoc
	err := r.collection.FindOne(ctx, bson.M{"_id": playerSettingsID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", false, nil
		}
		return "", false, err
	}
	id := domain.TorrentID(strings.TrimSpace(doc.CurrentTorrentID))
	if id == "" {
		return "", false, nil
	}
	return id, true, nil
}

func (r *PlayerSettingsRepository) SetCurrentTorrentID(ctx context.Context, id domain.TorrentID) error {
	update := bson.M{
		"$set": bson.M{
			"currentTorrentId": strings.TrimSpace(string(id)),
			"updatedAt":        time.Now().Unix(),
		},
	}
	_, err := r.collection.UpdateOne(
		ctx,
		bson.M{"_id": playerSettingsID},
		update,
		options.Update().SetUpsert(true),
	)
	return err
}
