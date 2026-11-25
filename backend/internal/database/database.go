package database

import (
	"context"
	"errors"
	"fmt"
	"log"
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

// UnsubscribeMails holds, per account and per address, when Mailsorter last
// wrote an unsubscribe mail there on the person's behalf (over IMAP): one
// document per (userId, address), unique, which is what refuses a second
// mail to that address for 24 hours, whichever list asks for it.
func (d *Database) UnsubscribeMails() *mongo.Collection {
	return d.DB.Collection("unsubscribe_mails")
}

func (d *Database) SortingRules() *mongo.Collection {
	return d.DB.Collection("sorting_rules")
}

func (d *Database) ProtectedSenders() *mongo.Collection {
	return d.DB.Collection("protected_senders")
}

func (d *Database) Snoozes() *mongo.Collection {
	return d.DB.Collection("snoozes")
}

func (d *Database) ActionLog() *mongo.Collection {
	return d.DB.Collection("action_log")
}

// SavedSearches holds the Gmail queries a user kept as one-click filters.
func (d *Database) SavedSearches() *mongo.Collection {
	return d.DB.Collection("saved_searches")
}

// Waitlist holds the people who asked to be told when Pro opens. It is the only
// collection not scoped by userId: signups come from logged-out visitors too,
// and the email address is the identity.
func (d *Database) Waitlist() *mongo.Collection {
	return d.DB.Collection("waitlist")
}

// MailAccounts holds the mailbox each user connected: provider, transport,
// username and a sealed app password. One row per user, enforced by a unique
// index on userId.
func (d *Database) MailAccounts() *mongo.Collection {
	return d.DB.Collection("mail_accounts")
}

// AISettings stores per-user custom AI provider configuration (BYOK).
func (d *Database) AISettings() *mongo.Collection {
	return d.DB.Collection("ai_settings")
}

// legacyEmailIndexes are indexes an older mongo-init/init-db.js created and the
// code must not live with. That script only runs on an empty volume, so a
// database created before it was fixed keeps them until something drops them.
//
// messageId_1 was UNIQUE on messageId alone. A Gmail id names a message
// worldwide, so that held as long as every mailbox was a Gmail one. An IMAP UID
// only names a message inside one folder of one mailbox: two people both have a
// message 1, and the index refused the second person's, every time, while the
// sync reported success.
var legacyEmailIndexes = []string{"messageId_1"}

// AnalysisCacheVersion is the version every entry of analysis_cache carries
// (and its key starts with, in internal/api). It changes when what a verdict
// is made from changes: v2 is the prompt that lists no label of the user. The
// cache is shared by every account and never expires, so entries of another
// version are deleted at boot (dropStaleAnalysisCache): a verdict from the
// prompt before could hold the name of one account's label or folder.
const AnalysisCacheVersion = "v2"

// dropStaleAnalysisCache deletes, in one request, every cached verdict that
// does not carry AnalysisCacheVersion, those written before entries carried a
// version included. They are never read again; this is what stops them from
// being kept. A failure is logged: the boot goes on, and the next one tries
// again.
func (d *Database) dropStaleAnalysisCache(ctx context.Context) {
	res, err := d.AnalysisCache().DeleteMany(ctx, bson.M{"version": bson.M{"$ne": AnalysisCacheVersion}})
	if err != nil {
		log.Printf("database: delete the analysis cache entries of other versions than %s: %v", AnalysisCacheVersion, err)
		return
	}
	if res.DeletedCount > 0 {
		log.Printf("database: deleted %d analysis cache entries of other versions than %s", res.DeletedCount, AnalysisCacheVersion)
	}
}

// EnsureIndexes creates the indexes that keep hot queries fast at scale, and
// drops from the shared analysis cache the verdicts of another version.
// It is best-effort: a failure on one index does not block the others.
func (d *Database) EnsureIndexes(ctx context.Context) error {
	// Last, under its own time: on a large cache that never expired, the
	// deletion must not eat the time the indexes are given.
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		d.dropStaleAnalysisCache(dctx)
	}()
	var firstErr error
	for _, name := range legacyEmailIndexes {
		if _, err := d.Emails().Indexes().DropOne(ctx, name); err != nil && !isMissingIndex(err) && firstErr == nil {
			firstErr = err
		}
	}
	if err := d.ensureEmailIdentity(ctx); err != nil && firstErr == nil {
		firstErr = err
	}

	specs := []struct {
		coll  *mongo.Collection
		model mongo.IndexModel
	}{
		{d.Emails(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "from", Value: 1}}}},
		{d.AISuggestions(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "status", Value: 1}}}},
		{d.SenderPreferences(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "senderEmail", Value: 1}}}},
		{d.AnalysisCache(), mongo.IndexModel{Keys: bson.D{{Key: "key", Value: 1}}, Options: options.Index().SetUnique(true)}},
		{d.AnalysisJobs(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "createdAt", Value: -1}}}},
		{d.Usage(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "period", Value: 1}}, Options: options.Index().SetUnique(true)}},
		{d.Unsubscribes(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "senderEmail", Value: 1}}, Options: options.Index().SetUnique(true)}},
		{d.UnsubscribeMails(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "address", Value: 1}}, Options: options.Index().SetUnique(true)}},
		{d.Users(), mongo.IndexModel{Keys: bson.D{{Key: "stripeSubscriptionId", Value: 1}}, Options: options.Index().SetSparse(true)}},
		// One account per address. Register checks then inserts, so this index
		// is what actually stops two concurrent sign-ups from creating twins.
		{d.Users(), mongo.IndexModel{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)}},
		{d.Emails(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "receivedDate", Value: -1}}}},
		{d.SortingRules(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "priority", Value: 1}}}},
		{d.ProtectedSenders(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "value", Value: 1}}, Options: options.Index().SetUnique(true)}},
		{d.Snoozes(), mongo.IndexModel{Keys: bson.D{{Key: "status", Value: 1}, {Key: "wakeAt", Value: 1}}}},
		{d.Snoozes(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "status", Value: 1}}}},
		{d.ActionLog(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "createdAt", Value: -1}}}},
		// Unique on the normalized query: re-saving a query the user already
		// kept renames that chip instead of creating a second identical one.
		{d.SavedSearches(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "key", Value: 1}}, Options: options.Index().SetUnique(true)}},
		// Unique on email so a visitor clicking twice (or across devices) is one
		// signup, not two: the count has to mean something.
		{d.Waitlist(), mongo.IndexModel{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)}},
		// Unique on userId: connecting a second mailbox replaces the first
		// rather than leaving two rows, one of which would be picked at random
		// every time the account is read.
		{d.MailAccounts(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}}, Options: options.Index().SetUnique(true)}},
		{d.AISettings(), mongo.IndexModel{Keys: bson.D{{Key: "userId", Value: 1}}, Options: options.Index().SetUnique(true)}},
	}

	for _, s := range specs {
		if _, err := s.coll.Indexes().CreateOne(ctx, s.model); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// emailIdentityKeys is what names a stored message: whose mailbox, and the id
// that mailbox gives it.
var emailIdentityKeys = bson.D{{Key: "userId", Value: 1}, {Key: "messageId", Value: 1}}

// ensureEmailIdentity makes (userId, messageId) unique: one stored copy of a
// message per person.
//
// It replaces the guarantee messageId_1 gave, scoped to its owner. Without it,
// a manual sync and the background one storing the same new message at the
// same moment could each insert it, and over IMAP, where the listing is read
// from this collection, the mail would show twice. With it, the second upsert
// hits the unique key and MongoDB retries it as an update.
//
// Earlier boots created the same keys WITHOUT unique, and MongoDB refuses a
// second index on identical keys, so that one is dropped and rebuilt. Should
// the rebuild fail (a person already has duplicates, which only a database
// that never had messageId_1 can hold), the plain index is put back so the
// lookups stay fast, and the failure is returned for the boot log.
func (d *Database) ensureEmailIdentity(ctx context.Context) error {
	idx := d.Emails().Indexes()
	unique := mongo.IndexModel{Keys: emailIdentityKeys, Options: options.Index().SetUnique(true)}
	_, err := idx.CreateOne(ctx, unique)
	if err == nil || !isIndexConflict(err) {
		return err
	}
	if _, err := idx.DropOne(ctx, "userId_1_messageId_1"); err != nil && !isMissingIndex(err) {
		return err
	}
	if _, err := idx.CreateOne(ctx, unique); err != nil {
		if _, plainErr := idx.CreateOne(ctx, mongo.IndexModel{Keys: emailIdentityKeys}); plainErr != nil {
			return fmt.Errorf("emails: per-person unique index not built (%v), and the plain one not restored: %w", err, plainErr)
		}
		return fmt.Errorf("emails: per-person unique index not built, plain index kept: %w", err)
	}
	return nil
}

// isIndexConflict reports whether an index could not be created because one on
// the same keys exists with other options (IndexOptionsConflict, 85, or
// IndexKeySpecsConflict, 86).
func isIndexConflict(err error) bool {
	var cmdErr mongo.CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.Code == 85 || cmdErr.Code == 86
	}
	return false
}

// isMissingIndex reports whether dropping an index failed only because there
// was nothing to drop: the index is gone already (IndexNotFound, 27), or the
// collection does not exist yet on a fresh database (NamespaceNotFound, 26).
func isMissingIndex(err error) bool {
	var cmdErr mongo.CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.Code == 27 || cmdErr.Code == 26
	}
	return false
}
