package database

import (
	"context"
	"time"

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

func (d *Database) SortingRules() *mongo.Collection {
	return d.DB.Collection("sorting_rules")
}

func (d *Database) Labels() *mongo.Collection {
	return d.DB.Collection("labels")
}

func (d *Database) GmailConfig() *mongo.Collection {
	return d.DB.Collection("gmail_config")
}
