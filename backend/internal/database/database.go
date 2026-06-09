package database

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Database struct {
	Client *mongo.Client
	DB     *mongo.Database
}

func NewDatabase(uri string) (*Database, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientOptions := options.Client().ApplyURI(uri)
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, err
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, err
	}

	db := client.Database("mailsorter")

	return &Database{
		Client: client,
		DB:     db,
	}, nil
}

func (d *Database) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return d.Client.Disconnect(ctx)
}

func (d *Database) Users() *mongo.Collection {
	return d.DB.Collection("users")
}

func (d *Database) Emails() *mongo.Collection {
	return d.DB.Collection("emails")
}

func (d *Database) Labels() *mongo.Collection {
	return d.DB.Collection("labels")
}

func (d *Database) GmailConfig() *mongo.Collection {
	return d.DB.Collection("gmail_config")
}

// AI Sorting Collections

func (d *Database) AISuggestions() *mongo.Collection {
	return d.DB.Collection("ai_suggestions")
}

func (d *Database) SenderPreferences() *mongo.Collection {
	return d.DB.Collection("sender_preferences")
}

func (d *Database) SmartLabels() *mongo.Collection {
	return d.DB.Collection("smart_labels")
}

func (d *Database) AnalysisJobs() *mongo.Collection {
	return d.DB.Collection("analysis_jobs")
}

func (d *Database) AnalysisCache() *mongo.Collection {
	return d.DB.Collection("analysis_cache")
}

func (d *Database) Usage() *mongo.Collection {
	return d.DB.Collection("usage")
}

func (d *Database) Unsubscribes() *mongo.Collection {
	return d.DB.Collection("unsubscribes")
}

// EnsureIndexes creates the indexes that keep hot queries fast at scale.
// It is best-effort: a failure on one index does not block the others.
func (d *Database) EnsureIndexes(ctx context.Context) error {
	specs := []struct {
		coll  *mongo.Collection
		model mongo.IndexModel
	}{
		{d.Emails(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "messageId", Value: 1}}}},
		{d.Emails(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "from", Value: 1}}}},
		{d.AISuggestions(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "status", Value: 1}}}},
		{d.SenderPreferences(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "senderEmail", Value: 1}}}},
		{d.AnalysisCache(), mongo.IndexModel{Keys: bson.D{{Key: "key", Value: 1}}, Options: options.Index().SetUnique(true)}},
		{d.AnalysisJobs(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "createdAt", Value: -1}}}},
		{d.Usage(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "period", Value: 1}}, Options: options.Index().SetUnique(true)}},
		{d.Unsubscribes(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "senderEmail", Value: 1}}, Options: options.Index().SetUnique(true)}},
		{d.Users(), mongo.IndexModel{Keys: bson.D{{Key: "stripeSubscriptionId", Value: 1}}, Options: options.Index().SetSparse(true)}},
	}

	var firstErr error
	for _, s := range specs {
		if _, err := s.coll.Indexes().CreateOne(ctx, s.model); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
