package mongo

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"torrentstream/internal/domain"
)

type Repository struct {
	collection *mongo.Collection
}

type fileDoc struct {
	Index          int    `bson:"index"`
	Path           string `bson:"path"`
	Length         int64  `bson:"length"`
	BytesCompleted int64  `bson:"bytesCompleted,omitempty"`
}

type torrentDoc struct {
	ID         string    `bson:"_id"`
	Name       string    `bson:"name"`
	Status     string    `bson:"status"`
	InfoHash   string    `bson:"infoHash"`
	Magnet     string    `bson:"magnet"`
	Torrent    string    `bson:"torrent"`
	Files      []fileDoc `bson:"files"`
	TotalBytes int64     `bson:"totalBytes"`
	DoneBytes  int64     `bson:"doneBytes"`
	Progress   float64   `bson:"progress"` // Cached progress for efficient sorting (0.0-1.0).
	CreatedAt  int64     `bson:"createdAt"`
	UpdatedAt  int64     `bson:"updatedAt"`
	Tags       []string  `bson:"tags,omitempty"`
}

type torrentUpdateDoc struct {
	Name       string    `bson:"name"`
	Status     string    `bson:"status"`
	InfoHash   string    `bson:"infoHash"`
	Magnet     string    `bson:"magnet"`
	Torrent    string    `bson:"torrent"`
	Files      []fileDoc `bson:"files"`
	TotalBytes int64     `bson:"totalBytes"`
	DoneBytes  int64     `bson:"doneBytes"`
	Progress   float64   `bson:"progress"`
	CreatedAt  int64     `bson:"createdAt"`
	UpdatedAt  int64     `bson:"updatedAt"`
	Tags       []string  `bson:"tags,omitempty"`
}

func NewRepository(client *mongo.Client, dbName, collectionName string) *Repository {
	return &Repository{collection: client.Database(dbName).Collection(collectionName)}
}

func Connect(ctx context.Context, uri string, extra ...*options.ClientOptions) (*mongo.Client, error) {
	opts := append([]*options.ClientOptions{options.Client().ApplyURI(uri)}, extra...)
	client, err := mongo.Connect(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (r *Repository) EnsureIndexes(ctx context.Context) error {
	if r == nil || r.collection == nil {
		return nil
	}
	models := []mongo.IndexModel{
		{Keys: bson.D{{Key: "name", Value: "text"}}},
		{Keys: bson.D{{Key: "tags", Value: 1}}},
		{Keys: bson.D{{Key: "createdAt", Value: -1}}},
		{Keys: bson.D{{Key: "updatedAt", Value: -1}}},
		{Keys: bson.D{{Key: "progress", Value: -1}}},
	}
	_, err := r.collection.Indexes().CreateMany(ctx, models)
	return err
}

func (r *Repository) Create(ctx context.Context, t domain.TorrentRecord) error {
	doc := toDoc(t)
	_, err := r.collection.InsertOne(ctx, doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.ErrAlreadyExists
		}
	}
	return err
}

func (r *Repository) Update(ctx context.Context, t domain.TorrentRecord) error {
	doc := toUpdateDoc(t)
	filter := bson.M{"_id": string(t.ID)}
	res, err := r.collection.UpdateOne(ctx, filter, bson.M{"$set": doc})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *Repository) UpdateTags(ctx context.Context, id domain.TorrentID, tags []string) error {
	clean := normalizeTags(tags)
	res, err := r.collection.UpdateOne(
		ctx,
		bson.M{"_id": string(id)},
		bson.M{"$set": bson.M{
			"tags":      clean,
			"updatedAt": time.Now().UTC().Unix(),
		}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *Repository) Get(ctx context.Context, id domain.TorrentID) (domain.TorrentRecord, error) {
	var doc torrentDoc
	if err := r.collection.FindOne(ctx, bson.M{"_id": string(id)}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.TorrentRecord{}, domain.ErrNotFound
		}
		return domain.TorrentRecord{}, err
	}
	return fromDoc(doc), nil
}

func (r *Repository) List(ctx context.Context, filter domain.TorrentFilter) ([]domain.TorrentRecord, error) {
	query := bson.M{}
	if filter.Status != nil {
		query["status"] = string(*filter.Status)
	}

	search := strings.TrimSpace(filter.Search)
	if search != "" {
		query["name"] = bson.M{
			"$regex":   regexp.QuoteMeta(search),
			"$options": "i",
		}
	}

	tags := normalizeTags(filter.Tags)
	if len(tags) > 0 {
		query["tags"] = bson.M{"$all": tags}
	}

	sortBy := strings.TrimSpace(filter.SortBy)
	if sortBy == "" {
		sortBy = "updatedAt"
	}
	sortOrder := filter.SortOrder
	if sortOrder != domain.SortAsc && sortOrder != domain.SortDesc {
		sortOrder = domain.SortDesc
	}

	opts := options.Find()
	field, ok := mongoSortField(sortBy)
	if !ok {
		field = "updatedAt"
	}
	direction := -1
	if sortOrder == domain.SortAsc {
		direction = 1
	}
	opts.SetSort(bson.D{{Key: field, Value: direction}})
	if filter.Offset > 0 {
		opts.SetSkip(int64(filter.Offset))
	}
	if filter.Limit > 0 {
		opts.SetLimit(int64(filter.Limit))
	}

	cursor, err := r.collection.Find(ctx, query, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []torrentDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	return fromDocs(docs), nil
}

func (r *Repository) GetMany(ctx context.Context, ids []domain.TorrentID) ([]domain.TorrentRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, string(id))
	}

	cursor, err := r.collection.Find(ctx, bson.M{"_id": bson.M{"$in": values}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []torrentDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	return fromDocs(docs), nil
}

func (r *Repository) Delete(ctx context.Context, id domain.TorrentID) error {
	res, err := r.collection.DeleteOne(ctx, bson.M{"_id": string(id)})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func toDoc(t domain.TorrentRecord) torrentDoc {
	files := make([]fileDoc, 0, len(t.Files))
	for _, f := range t.Files {
		files = append(files, fileDoc{
			Index:          f.Index,
			Path:           f.Path,
			Length:         f.Length,
			BytesCompleted: f.BytesCompleted,
		})
	}

	progress := 0.0
	if t.TotalBytes > 0 {
		progress = float64(t.DoneBytes) / float64(t.TotalBytes)
	}

	return torrentDoc{
		ID:         string(t.ID),
		Name:       t.Name,
		Status:     string(t.Status),
		InfoHash:   string(t.InfoHash),
		Magnet:     t.Source.Magnet,
		Torrent:    t.Source.Torrent,
		Files:      files,
		TotalBytes: t.TotalBytes,
		DoneBytes:  t.DoneBytes,
		Progress:   progress,
		CreatedAt:  t.CreatedAt.Unix(),
		UpdatedAt:  t.UpdatedAt.Unix(),
		Tags:       normalizeTags(t.Tags),
	}
}

func toUpdateDoc(t domain.TorrentRecord) torrentUpdateDoc {
	files := make([]fileDoc, 0, len(t.Files))
	for _, f := range t.Files {
		files = append(files, fileDoc{
			Index:          f.Index,
			Path:           f.Path,
			Length:         f.Length,
			BytesCompleted: f.BytesCompleted,
		})
	}

	// Calculate progress for efficient DB sorting (0.0-1.0).
	progress := 0.0
	if t.TotalBytes > 0 {
		progress = float64(t.DoneBytes) / float64(t.TotalBytes)
	}

	return torrentUpdateDoc{
		Name:       t.Name,
		Status:     string(t.Status),
		InfoHash:   string(t.InfoHash),
		Magnet:     t.Source.Magnet,
		Torrent:    t.Source.Torrent,
		Files:      files,
		TotalBytes: t.TotalBytes,
		DoneBytes:  t.DoneBytes,
		Progress:   progress,
		CreatedAt:  t.CreatedAt.Unix(),
		UpdatedAt:  t.UpdatedAt.Unix(),
		Tags:       normalizeTags(t.Tags),
	}
}

func fromDoc(doc torrentDoc) domain.TorrentRecord {
	files := make([]domain.FileRef, 0, len(doc.Files))
	for _, f := range doc.Files {
		files = append(files, domain.FileRef{
			Index:          f.Index,
			Path:           f.Path,
			Length:         f.Length,
			BytesCompleted: f.BytesCompleted,
		})
	}

	return domain.TorrentRecord{
		ID:         domain.TorrentID(doc.ID),
		Name:       doc.Name,
		Status:     domain.TorrentStatus(doc.Status),
		InfoHash:   domain.InfoHash(doc.InfoHash),
		Source:     domain.TorrentSource{Magnet: doc.Magnet, Torrent: doc.Torrent},
		Files:      files,
		TotalBytes: doc.TotalBytes,
		DoneBytes:  doc.DoneBytes,
		CreatedAt:  timeFromUnix(doc.CreatedAt),
		UpdatedAt:  timeFromUnix(doc.UpdatedAt),
		Tags:       normalizeTags(doc.Tags),
	}
}

func fromDocs(docs []torrentDoc) []domain.TorrentRecord {
	records := make([]domain.TorrentRecord, 0, len(docs))
	for _, doc := range docs {
		records = append(records, fromDoc(doc))
	}
	return records
}

func timeFromUnix(value int64) time.Time {
	return time.Unix(value, 0).UTC()
}

func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	clean := make([]string, 0, len(tags))
	for _, tag := range tags {
		t := strings.TrimSpace(tag)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		clean = append(clean, t)
	}
	return clean
}

func progressOfRecord(r domain.TorrentRecord) float64 {
	if r.TotalBytes <= 0 {
		return 0
	}
	p := float64(r.DoneBytes) / float64(r.TotalBytes)
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

func mongoSortField(sortBy string) (string, bool) {
	switch sortBy {
	case "name":
		return "name", true
	case "createdAt":
		return "createdAt", true
	case "updatedAt":
		return "updatedAt", true
	case "totalBytes":
		return "totalBytes", true
	case "progress":
		return "progress", true
	default:
		return "", false
	}
}
