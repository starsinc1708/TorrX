package watcher

import (
	"context"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ChangeEvent represents a simplified MongoDB change stream event.
type ChangeEvent struct {
	OperationType string
	UpdatedFields map[string]interface{}
}

// IsCompletionEvent returns true if the event represents a torrent reaching "completed" status.
func IsCompletionEvent(e ChangeEvent) bool {
	if e.OperationType != "update" {
		return false
	}
	status, ok := e.UpdatedFields["status"]
	if !ok {
		return false
	}
	return status == "completed"
}

// NotifyFunc is called with the torrent name when completion is detected.
type NotifyFunc func(ctx context.Context, torrentName string)

// Watcher watches the MongoDB torrents collection for completion events.
type Watcher struct {
	col    *mongo.Collection
	notify NotifyFunc
}

func New(db *mongo.Database, notify NotifyFunc) *Watcher {
	return &Watcher{
		col:    db.Collection("torrents"),
		notify: notify,
	}
}

// Run starts the change stream loop. Blocks until ctx is cancelled.
// Reconnects automatically on transient errors.
func (w *Watcher) Run(ctx context.Context) {
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{
			{Key: "operationType", Value: "update"},
			{Key: "updateDescription.updatedFields.status", Value: "completed"},
		}}},
	}
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)

	for {
		if err := w.watch(ctx, pipeline, opts); err != nil {
			if ctx.Err() != nil {
				return // context cancelled — normal shutdown
			}
			log.Printf("watcher: change stream error, retrying in 5s: %v", err)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return
			}
		}
		// nil return means cursor closed by server — retry immediately
		if ctx.Err() != nil {
			return
		}
	}
}

func (w *Watcher) watch(ctx context.Context, pipeline mongo.Pipeline, opts *options.ChangeStreamOptions) error {
	cs, err := w.col.Watch(ctx, pipeline, opts)
	if err != nil {
		return err
	}
	defer cs.Close(ctx)

	for cs.Next(ctx) {
		var raw struct {
			OperationType string `bson:"operationType"`
			UpdateDesc    struct {
				UpdatedFields bson.M `bson:"updatedFields"`
			} `bson:"updateDescription"`
			FullDocument struct {
				Name string `bson:"name"`
			} `bson:"fullDocument"`
		}
		if err := cs.Decode(&raw); err != nil {
			log.Printf("watcher: decode error: %v", err)
			continue
		}
		event := ChangeEvent{
			OperationType: raw.OperationType,
			UpdatedFields: raw.UpdateDesc.UpdatedFields,
		}
		if IsCompletionEvent(event) {
			w.notify(ctx, raw.FullDocument.Name)
		}
	}
	return cs.Err()
}
