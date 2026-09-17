package api

import (
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/account"
)

// The whole point of account.Datasets is that one list drives both the RGPD
// export and the account deletion. That only holds if every entry actually
// resolves to a collection here: a dataset with no mapping is skipped by both,
// silently, which is exactly the failure mode the catalog exists to prevent.
func TestEveryDatasetHasABackingCollection(t *testing.T) {
	h := newTestHandler(t)

	for _, ds := range account.Datasets() {
		if h.datasetCollection(ds) == nil {
			t.Errorf("dataset %q maps to no collection: export and erasure both skip it", ds)
		}
	}
}

// The mailbox mirror holds the decoded body of every synced message. It was
// absent from the catalog, so "supprimer definitivement mon compte" removed the
// profile and the settings and left the full text of the user's mail in the
// database. Pin it: the catalog must carry the collections that hold mail.
func TestCatalogCoversTheMailboxMirror(t *testing.T) {
	h := newTestHandler(t)

	wantSame := map[account.Dataset]string{
		account.DatasetEmails: h.db.Emails().Name(),
		account.DatasetLabels: h.db.Labels().Name(),
	}

	present := map[account.Dataset]bool{}
	for _, ds := range account.Datasets() {
		present[ds] = true
	}

	for ds, collName := range wantSame {
		if !present[ds] {
			t.Errorf("dataset %q missing from account.Datasets(): export and erasure skip it", ds)
			continue
		}
		if got := h.datasetCollection(ds); got == nil || got.Name() != collName {
			t.Errorf("datasetCollection(%q) does not resolve to the %q collection", ds, collName)
		}
	}
}
