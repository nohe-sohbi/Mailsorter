package imap

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// refusedSizes is a server session that, when refuse is set, refuses whole
// every FETCH asking for a part's decoded size (BINARY.SIZE), the way a
// server answers "NO [UNKNOWN-CTE]" or a parse error when one part does not
// decode. It counts the FETCH commands asking for sizes, and all of them.
type refusedSizes struct {
	imapserver.SessionIMAP4rev2
	refuse         bool
	withSizes, all *atomic.Int64
}

func (s refusedSizes) Fetch(w *imapserver.FetchWriter, numSet imapv2.NumSet, options *imapv2.FetchOptions) error {
	s.all.Add(1)
	if len(options.BinarySectionSize) > 0 {
		s.withSizes.Add(1)
		if s.refuse {
			return &imapv2.Error{
				Type: imapv2.StatusResponseTypeNo,
				Code: imapv2.ResponseCodeUnknownCTE,
				Text: "Cannot decode a part",
			}
		}
	}
	return s.SessionIMAP4rev2.Fetch(w, numSet, options)
}

// sizesServer is a client on a server whose FETCH commands are counted, and
// refused when they ask for sizes if refuse is set.
func sizesServer(t *testing.T, refuse bool) (c *Client, withSizes, all *atomic.Int64) {
	t.Helper()
	withSizes, all = new(atomic.Int64), new(atomic.Int64)
	addr := serveTestServer(t, standardMailboxes(), func(s imapserver.SessionIMAP4rev2) imapserver.Session {
		return refusedSizes{SessionIMAP4rev2: s, refuse: refuse, withSizes: withSizes, all: all}
	})
	return dialTestServer(t, addr), withSizes, all
}

// A server that refuses the whole command over the sizes of the parts does
// not keep the mail from opening: the reader asks again without them, and
// shows the mail with sizes estimated from what is stored. The download goes
// through the same reading, and gives the file.
func TestReadPartsAsksAgainWithoutSizesWhenTheServerRefuses(t *testing.T) {
	c, withSizes, all := sizesServer(t, true)
	uid := stored(t, c, mixed(textPart, pdfPart()))

	withSizes.Store(0)
	all.Store(0)
	msg, err := c.Fetch(context.Background(), "INBOX", uid)
	if err != nil {
		t.Fatalf("Fetch on a server that refuses the sizes: %v, want the mail", err)
	}
	// The description, the refused command, the same without sizes.
	if got, gotAll := withSizes.Load(), all.Load(); got != 1 || gotAll != 3 {
		t.Errorf("Fetch sent %d FETCH commands, %d of them asking for sizes, want 3 and 1: asked once, then not again", gotAll, got)
	}
	if msg.Body == "" || len(msg.Attachments) != 1 || msg.Attachments[0].Filename != "Facture décembre.pdf" {
		t.Fatalf("Fetch(...) = body %q, files %+v, want the text and the PDF", msg.Body, msg.Attachments)
	}
	if size, want := msg.Attachments[0].Size, int64(len(everyByte())); size < want-3 || size > want+3 {
		t.Errorf("the PDF is listed at %d bytes, want about %d, estimated from its stored size", size, want)
	}

	withSizes.Store(0)
	_, data, err := c.Attachment(context.Background(), "INBOX", uid, "2", testLimit)
	if err != nil || !bytes.Equal(data, everyByte()) {
		t.Errorf("Attachment on a server that refuses the sizes = %d bytes, %v, want the PDF", len(data), err)
	}
	if got := withSizes.Load(); got != 1 {
		t.Errorf("the download asked for sizes in %d commands, want 1", got)
	}
}

// A mail that is gone is not asked for again without the sizes: the answer
// is about the mail, and asking again would only get it twice.
func TestReadPartsDoesNotAskAgainForAMailThatIsGone(t *testing.T) {
	c, withSizes, all := sizesServer(t, false)
	uid := stored(t, c, mixed(textPart, pdfPart()))
	described, err := c.fetchOne("INBOX", uid, &imapv2.FetchOptions{UID: true, BodyStructure: &imapv2.FetchItemBodyStructure{Extended: true}})
	if err != nil {
		t.Fatalf("fetchOne: %v", err)
	}

	// The same description, for a number the folder does not hold.
	withSizes.Store(0)
	all.Store(0)
	if _, err := c.readParts("INBOX", "99999", described.BodyStructure, true); !errors.Is(err, ErrMessageGone) {
		t.Fatalf("readParts of a number the folder does not hold = %v, want ErrMessageGone", err)
	}
	if got, gotAll := withSizes.Load(), all.Load(); got != 1 || gotAll != 1 {
		t.Errorf("readParts sent %d FETCH commands (%d asking for sizes) for a mail that is not there, want 1", gotAll, got)
	}
}
