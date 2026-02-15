package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/internal/domain"
)

type watchPositionDoc struct {
	ID          string  `bson:"_id"`
	TorrentID   string  `bson:"torrentId"`
	FileIndex   int     `bson:"fileIndex"`
	Position    float64 `bson:"position"`
	Duration    float64 `bson:"duration"`
	TorrentName string  `bson:"torrentName"`
	FilePath    string  `bson:"filePath"`
	UpdatedAt   int64   `bson:"updatedAt"`
}

type WatchHistoryRepository struct {
	collection *mongo.Collection
}

func NewWatchHistoryRepository(client *mongo.Client, dbName string) *WatchHistoryRepository {
	return &WatchHistoryRepository{collection: client.Database(dbName).Collection("watch_history")}
}

func watchDocID(torrentID domain.TorrentID, fileIndex int) string {
	return fmt.Sprintf("%s:%d", string(torrentID), fileIndex)
}

func (r *WatchHistoryRepository) Upsert(ctx context.Context, wp domain.WatchPosition) error {
	update := bson.M{
		"$set": bson.M{
			"torrentId":   string(wp.TorrentID),
			"fileIndex":   wp.FileIndex,
			"position":    wp.Position,
			"duration":    wp.Duration,
			"torrentName": wp.TorrentName,
			"filePath":    wp.FilePath,
			"updatedAt":   time.Now().Unix(),
		},
	}
	_, err := r.collection.UpdateOne(
		ctx,
		bson.M{"_id": watchDocID(wp.TorrentID, wp.FileIndex)},
		update,
		options.Update().SetUpsert(true),
	)
	return err
}

func (r *WatchHistoryRepository) Get(ctx context.Context, torrentID domain.TorrentID, fileIndex int) (domain.WatchPosition, error) {
	var doc watchPositionDoc
	err := r.collection.FindOne(ctx, bson.M{"_id": watchDocID(torrentID, fileIndex)}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.WatchPosition{}, domain.ErrNotFound
		}
		return domain.WatchPosition{}, err
	}
	return watchDocToPosition(doc), nil
}

func (r *WatchHistoryRepository) ListRecent(ctx context.Context, limit int) ([]domain.WatchPosition, error) {
	if limit <= 0 {
		limit = 20
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "updatedAt", Value: -1}}).
		SetLimit(int64(limit))

	cursor, err := r.collection.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []watchPositionDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	positions := make([]domain.WatchPosition, 0, len(docs))
	for _, doc := range docs {
		positions = append(positions, watchDocToPosition(doc))
	}
	return positions, nil
}

func watchDocToPosition(doc watchPositionDoc) domain.WatchPosition {
	return domain.WatchPosition{
		TorrentID:   domain.TorrentID(doc.TorrentID),
		FileIndex:   doc.FileIndex,
		Position:    doc.Position,
		Duration:    doc.Duration,
		TorrentName: doc.TorrentName,
		FilePath:    doc.FilePath,
		UpdatedAt:   time.Unix(doc.UpdatedAt, 0).UTC(),
	}
}
