// Initialize mailsorter database
db = db.getSiblingDB('mailsorter');

// Create collections
db.createCollection('emails');
db.createCollection('sorting_rules');
db.createCollection('labels');
db.createCollection('users');

// Create indexes
// Not unique on messageId alone: over IMAP a message id is a UID, which every
// mailbox numbers from 1, so a unique index refused the second person's mail.
// EnsureIndexes (backend/internal/database/database.go) is the source of truth
// and drops that old index from databases this script created before.
db.emails.createIndex({ "userId": 1, "messageId": 1 });
db.emails.createIndex({ "userId": 1, "receivedDate": -1 });
db.emails.createIndex({ "userId": 1, "labelIds": 1 });

db.sorting_rules.createIndex({ "userId": 1, "priority": 1 });
db.sorting_rules.createIndex({ "userId": 1, "enabled": 1 });

db.labels.createIndex({ "userId": 1, "name": 1 }, { unique: true });

db.users.createIndex({ "email": 1 }, { unique: true });

print('Database initialized successfully');
