// Initialize mailsorter database
db = db.getSiblingDB('mailsorter');

// Create collections
db.createCollection('emails');
db.createCollection('sorting_rules');
db.createCollection('labels');
db.createCollection('users');

// Create indexes
db.emails.createIndex({ "messageId": 1 }, { unique: true });
db.emails.createIndex({ "userId": 1, "receivedDate": -1 });
db.emails.createIndex({ "userId": 1, "labelIds": 1 });

db.sorting_rules.createIndex({ "userId": 1, "priority": 1 });
db.sorting_rules.createIndex({ "userId": 1, "enabled": 1 });

db.labels.createIndex({ "userId": 1, "name": 1 }, { unique: true });

db.users.createIndex({ "email": 1 }, { unique: true });

print('Database initialized successfully');
