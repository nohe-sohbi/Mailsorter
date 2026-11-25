package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
//
// userId_1_messageId_1, unique on (userId, messageId), is replaced too (a person
// with two IMAP mailboxes one after the other has a message 1 in each), but by
// ensureEmailIdentity, and only once its replacement holds.
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

// EnsureIndexes creates the indexes that keep hot queries fast at scale,
// gives what was kept of the IMAP mailboxes connected their generation
// (stampGenerations), and drops from the shared analysis cache the verdicts of
// another version. It is best-effort: a failure on one index does not block
// the others.
func (d *Database) EnsureIndexes(ctx context.Context) error {
	// Last, under its own time: on a large cache that never expired, the
	// deletion must not eat the time the indexes are given.
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		d.dropStaleAnalysisCache(dctx)
	}()
	// After the indexes, under its own time too, and before the cache: cut
	// short by the indexes' deadline, it would leave rows without their
	// generation, which the next sync erases, and "Annuler" with them.
	defer func() {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stampTimeout)
		defer cancel()
		d.stampGenerations(sctx)
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

// emailIdentityKeys is what names a stored message: whose mailbox, the
// generation of that mailbox (the mail_accounts row an IMAP copy was read
// through, none over the Gmail API), and the id that mailbox gives it.
var emailIdentityKeys = bson.D{{Key: "userId", Value: 1}, {Key: "messageId", Value: 1}, {Key: "generation", Value: 1}}

// The names MongoDB gives the index on emailIdentityKeys, and the one it
// replaces, unique on (userId, messageId) alone.
const (
	emailIdentityIndex    = "userId_1_messageId_1_generation_1"
	oldEmailIdentityIndex = "userId_1_messageId_1"
)

// ensureEmailIdentity makes (userId, messageId, generation) unique: one stored
// copy of a message per person and mailbox.
//
// It replaces the guarantee messageId_1 gave, scoped to its owner and to the
// mailbox: a sync of a mailbox since replaced writes its message 1 beside the
// next mailbox's, not over it. Without it, a manual sync and the background
// one storing the same new message at the same moment could each insert it,
// and over IMAP, where the listing is read from this collection, the mail
// would show twice. With it, the second upsert hits the unique key and
// MongoDB retries it as an update.
//
// The old key (userId, messageId) is dropped only once the new one is built:
// until then it still keeps one row per number. A boot that could not build
// the new one unique left a plain index on the same keys, and MongoDB refuses
// a second index on identical keys, so that one is dropped and rebuilt. Any
// other failure (a person already has duplicates, error 11000, or the boot's
// deadline) leaves a plain index on the new keys, so the lookups stay fast,
// keeps the old key, and is logged and returned.
func (d *Database) ensureEmailIdentity(ctx context.Context) error {
	idx := d.Emails().Indexes()
	unique := mongo.IndexModel{Keys: emailIdentityKeys, Options: options.Index().SetUnique(true)}
	_, err := idx.CreateOne(ctx, unique)
	if err != nil && isIndexConflict(err) {
		if _, dropErr := idx.DropOne(ctx, emailIdentityIndex); dropErr != nil && !isMissingIndex(dropErr) {
			return fmt.Errorf("emails: drop the plain index on the per-mailbox key to build it unique: %w", dropErr)
		}
		_, err = idx.CreateOne(ctx, unique)
	}
	if err != nil {
		log.Printf("database: emails: the per-mailbox unique index is not built, the per-person one is kept: %v", err)
		if _, plainErr := idx.CreateOne(ctx, mongo.IndexModel{Keys: emailIdentityKeys}); plainErr != nil {
			log.Printf("database: emails: the plain index on the per-mailbox key is not built either: %v", plainErr)
			return fmt.Errorf("emails: per-mailbox unique index not built (%v), nor the plain one: %w", err, plainErr)
		}
		return fmt.Errorf("emails: per-mailbox unique index not built, plain index built, per-person one kept: %w", err)
	}
	if _, err := idx.DropOne(ctx, oldEmailIdentityIndex); err != nil && !isMissingIndex(err) {
		return fmt.Errorf("emails: drop the per-person unique index the per-mailbox one replaces: %w", err)
	}
	return nil
}

// stampTimeout bounds stampGenerations at boot: a few updates per IMAP
// mailbox, done once, then finding nothing.
const stampTimeout = 5 * time.Minute

// stampGenerations gives what was kept of an IMAP mailbox before it carried a
// generation the generation of the mailbox connected now: the _id of its
// mail_accounts row (see api/generation.go). At each boot, and it finds
// nothing once done. It logs how many rows it stamped and erased, and its
// failures: the boot goes on, and the next one does what is left.
//
// Before generations, connecting another address wrote over the same row and
// erased nothing: the row's last connection (updatedAt) is the one moment
// from which what was kept is known to be the connected mailbox's, the
// moment of its first one (createdAt) is not.
//
//   - The copy of the inbox (the rows that carry a folder): a row still in
//     the inbox is of the connected mailbox (each sync rewrites or drops it
//     against the inbox listed); a row kept for "Annuler" only if the mail
//     left the inbox since that connection (leftAt).
//   - The AI's proposals and the history entries made since that connection.
//
// What comes before stays without a generation: final in the history, and
// erased from the copy here. So are the rows of the copy of the accounts with
// no IMAP mailbox connected (disconnected, or gone to Gmail, before
// generations), which the senders, the subscriptions and the sweeps of the
// Gmail API would read. Not those of a mailbox whose stamping failed: the
// next boot stamps them.
func (d *Database) stampGenerations(ctx context.Context) {
	cur, err := d.MailAccounts().Find(ctx, bson.M{"transport": "imap"},
		options.Find().SetProjection(bson.M{"_id": 1, "userId": 1, "updatedAt": 1}))
	if err != nil {
		log.Printf("database: read the IMAP mailboxes to stamp their generation, done at the next boot: %v", err)
		return
	}
	var accounts []struct {
		ID        primitive.ObjectID `bson:"_id"`
		UserID    string             `bson:"userId"`
		UpdatedAt time.Time          `bson:"updatedAt"`
	}
	if err := cur.All(ctx, &accounts); err != nil {
		log.Printf("database: read the IMAP mailboxes to stamp their generation, done at the next boot: %v", err)
		return
	}
	var copies, proposals, entries int64
	failed := 0
	// Their copies are not erased: the next boot stamps them.
	unstamped := make([]string, 0)
	none := bson.M{"$exists": false}
	for _, a := range accounts {
		stamp := bson.M{"$set": bson.M{"generation": a.ID.Hex()}}
		// A row that never says when it was last connected: nothing kept
		// before now is known to be its mailbox's.
		connected := a.UpdatedAt
		if connected.IsZero() {
			connected = time.Now()
		}
		since := bson.M{"$gte": connected}
		ok := true
		for _, step := range []struct {
			coll   *mongo.Collection
			filter bson.M
			count  *int64
		}{
			{d.Emails(), bson.M{"userId": a.UserID, "folder": bson.M{"$exists": true}, "generation": none,
				"$or": []bson.M{{"leftInbox": bson.M{"$ne": true}}, {"leftAt": since}}}, &copies},
			{d.AISuggestions(), bson.M{"userId": a.UserID, "generation": none, "createdAt": since}, &proposals},
			{d.ActionLog(), bson.M{"userId": a.UserID, "generation": none, "createdAt": since}, &entries},
		} {
			res, err := step.coll.UpdateMany(ctx, step.filter, stamp)
			if err != nil {
				failed++
				ok = false
				log.Printf("database: stamp the generation of the IMAP mailbox of %s in %s, done at the next boot: %v", a.UserID, step.coll.Name(), err)
				continue
			}
			*step.count += res.ModifiedCount
		}
		if !ok {
			unstamped = append(unstamped, a.UserID)
		}
	}
	var erased int64
	res, err := d.Emails().DeleteMany(ctx, bson.M{"folder": bson.M{"$exists": true}, "generation": none, "userId": bson.M{"$nin": unstamped}})
	if err != nil {
		failed++
		log.Printf("database: erase the rows of copies of no IMAP mailbox connected, done at the next boot: %v", err)
	} else {
		erased = res.DeletedCount
	}
	log.Printf("database: generations stamped for %d IMAP mailboxes: %d rows of their copy, %d AI proposals, %d history entries; %d rows of copies of no mailbox connected erased (%d failures)",
		len(accounts), copies, proposals, entries, erased, failed)
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
