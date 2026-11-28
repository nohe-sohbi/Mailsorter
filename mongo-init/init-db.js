// Runs once, on an empty MongoDB volume (docker-entrypoint-initdb.d).
//
// It creates nothing on purpose: every collection and index the app relies on
// is created by the backend at startup, in EnsureIndexes
// (backend/internal/database/database.go), the single source of truth. A
// second list here only drifted from it, and never ran on databases created
// any other way (CI, the acceptance scenarios, an existing volume).
db = db.getSiblingDB('mailsorter');
print('Mailsorter: indexes are created by the backend at startup.');
