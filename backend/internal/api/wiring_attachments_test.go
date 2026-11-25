//go:build acceptance

package api

import (
	"bytes"
	"context"
	"testing"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/nohe-sohbi/mailsorter/backend/internal/attachment"
	"go.mongodb.org/mongo-driver/bson"
)

// notesMail carries a text file named in its Content-Type alone, without a
// disposition: the first rule of the filter "Pièces jointes" did not count
// it (a named text part), the reader's rule lists it, and so does the filter
// now.
const notesMail = "From: Ami <ami@exemple.fr>\r\n" +
	"Subject: Notes\r\n" +
	"Message-ID: <notes@wiring.test>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"b\"\r\n" +
	"\r\n" +
	"--b\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Voici mes notes.\r\n" +
	"--b\r\n" +
	"Content-Type: text/plain; charset=utf-8; name=\"notes.txt\"\r\n" +
	"\r\n" +
	"Des notes.\r\n" +
	"--b--\r\n"

// attachmentsRow is what the sync stored of a message's attachments.
type attachmentsRow struct {
	HasAttachments *bool `bson:"hasAttachments"`
	Rule           int   `bson:"hasAttachmentsRule"`
}

// A message counted by an older rule of the filter is counted again by the
// current one, once: the next sync reads its structure again, and stores the
// count with the rule's version; the one after does not read it again.
func TestAcceptance_SyncCountsAgainWhatAnOlderRuleCounted(t *testing.T) {
	app := newTestApp(t, &scenarioResult{})
	moi := app.imapPerson(imapInboxOwner)
	if _, err := moi.mailbox.Append("INBOX", bytes.NewReader([]byte(notesMail)), &imapv2.AppendOptions{Time: time.Now().Add(-time.Minute)}); err != nil {
		app.fatalf("dépôt du mail Notes dans la boîte de %s : %v", moi.email, err)
	}
	moi.receive(incoming{From: "Ami <ami@exemple.fr>", Subject: "Texte"})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	row := func(subject string) attachmentsRow {
		t.Helper()
		var r attachmentsRow
		if err := app.h.db.Emails().FindOne(ctx, bson.M{"userId": moi.email, "subject": subject}).Decode(&r); err != nil {
			app.fatalf("lecture du mail %q dans la copie de %s : %v", subject, moi.email, err)
		}
		return r
	}
	set := func(subject string, update bson.M) {
		t.Helper()
		if _, err := app.h.db.Emails().UpdateOne(ctx, bson.M{"userId": moi.email, "subject": subject}, update); err != nil {
			app.fatalf("écriture du mail %q dans la copie de %s : %v", subject, moi.email, err)
		}
	}
	expect := func(when, subject string, has bool) {
		t.Helper()
		r := row(subject)
		switch {
		case r.HasAttachments == nil:
			app.failf("%s, la copie de %s ne sait pas si le mail %q a une pièce jointe", when, moi.email, subject)
		case *r.HasAttachments != has:
			app.failf("%s, la copie de %s dit hasAttachments = %v pour le mail %q, attendu %v", when, moi.email, *r.HasAttachments, subject, has)
		case r.Rule != attachment.WithoutHTMLVersion:
			app.failf("%s, la copie de %s garde la règle %d pour le mail %q, attendu %d", when, moi.email, r.Rule, subject, attachment.WithoutHTMLVersion)
		}
	}

	moi.sync()
	expect("après la première synchronisation", "Notes", true)
	expect("après la première synchronisation", "Texte", false)

	// As the first rule left them: counted, without a version.
	set("Notes", bson.M{"$set": bson.M{"hasAttachments": false}, "$unset": bson.M{"hasAttachmentsRule": ""}})
	set("Texte", bson.M{"$unset": bson.M{"hasAttachmentsRule": ""}})
	moi.sync()
	expect("après une synchronisation qui relit ce que l'ancienne règle avait compté", "Notes", true)
	expect("après une synchronisation qui relit ce que l'ancienne règle avait compté", "Texte", false)

	// Counted by the current rule: not read again. A value the sync does
	// not overwrite is what proves it.
	set("Notes", bson.M{"$set": bson.M{"hasAttachments": false}})
	moi.sync()
	if r := row("Notes"); r.HasAttachments == nil || *r.HasAttachments {
		app.failf("la synchronisation suivante a relu le mail Notes de %s, déjà compté par la règle actuelle (hasAttachments = %v)", moi.email, r.HasAttachments)
	}
}
