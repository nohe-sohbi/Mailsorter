package api

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/search"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Serving a listing from the stored mailbox rather than from the provider.
//
// Over the Gmail API a listing is a call to Google: the query goes out as
// written and the answer comes back paginated. Over IMAP there is no such call.
// The protocol has SEARCH, but it is a per-connection round trip on a stateful
// socket with a folder selected, and a listing runs on every page load, every
// filter chip and every keystroke of a search. So the IMAP listing is served
// from the mirror that the sync writes, which is the same data a moment older.
//
// That is a real tradeoff and it is worth stating: a message that arrived since
// the last sync is not in the answer. The sync runs before the listing on the
// SPA's own load path and every 30 minutes in the background, so the window is
// the same one the inbox already had.

// mirrorPageLimit bounds one page. The Gmail listing caps at 500; the mirror
// answers from a local query, but the page still travels to a browser and gets
// rendered, so the same ceiling applies for the same reason.
const mirrorPageLimit = 500

// errUnsupportedQuery names the terms of a query that the stored mailbox cannot
// answer. It is not a refusal of the request so much as of the pretence: the
// alternative is running the query without them, which returns a full inbox
// under a filter that says otherwise.
type errUnsupportedQuery struct {
	terms []string
}

func (e errUnsupportedQuery) Error() string {
	return "api: query terms a stored mailbox cannot answer: " + strings.Join(e.terms, " ")
}

// mirrorFilter turns a parsed query into the lookup that answers it.
//
// Anything the query could not express is the caller's problem, not this
// function's: it is handed a Criteria that is already only the answerable part.
func mirrorFilter(userEmail string, crit search.Criteria, now time.Time) bson.M {
	filter := bson.M{"userId": userEmail}

	if crit.Folder != "" {
		filter["folder"] = crit.Folder
	}
	if crit.Unread != nil {
		filter["isRead"] = *crit.Unread
	}
	for _, v := range crit.From {
		filter["from"] = containsInsensitive(v)
	}
	for _, v := range crit.To {
		filter["to"] = containsInsensitive(v)
	}
	for _, v := range crit.Subject {
		filter["subject"] = containsInsensitive(v)
	}

	// An age is a bound on the received date, and the clock belongs here rather
	// than in the pure parser.
	dateBounds := bson.M{}
	if crit.NewerThan > 0 {
		dateBounds["$gte"] = now.Add(-crit.NewerThan)
	}
	if crit.OlderThan > 0 {
		dateBounds["$lte"] = now.Add(-crit.OlderThan)
	}
	if len(dateBounds) > 0 {
		filter["receivedDate"] = dateBounds
	}

	// Bare words match anywhere a human would expect to find them. They are
	// ANDed with each other, like every other term, so two words narrow rather
	// than widen.
	var textClauses []bson.M
	for _, word := range crit.Text {
		rx := containsInsensitive(word)
		textClauses = append(textClauses, bson.M{"$or": []bson.M{
			{"subject": rx},
			{"from": rx},
			{"snippet": rx},
		}})
	}
	if len(textClauses) > 0 {
		filter["$and"] = textClauses
	}
	return filter
}

// containsInsensitive is a substring match, with the user's text escaped.
//
// QuoteMeta is load-bearing: a query is free text a user typed, and a subject
// line with a "(" in it would otherwise be an invalid expression that makes the
// whole listing fail, while a "." would quietly match any character.
func containsInsensitive(value string) bson.M {
	return bson.M{"$regex": regexp.QuoteMeta(value), "$options": "i"}
}

// listFromMirror answers a listing out of the stored mailbox.
//
// The page token is an offset. Gmail's is opaque and so is this one as far as
// the client is concerned; it only ever echoes back what it was handed.
func (h *Handler) listFromMirror(ctx context.Context, userEmail, query string, maxResults int64, pageToken string) (map[string]interface{}, error) {
	crit, unsupported := search.Parse(query)
	if len(unsupported) > 0 {
		return nil, errUnsupportedQuery{terms: unsupported}
	}

	offset, err := parseOffsetToken(pageToken)
	if err != nil {
		return nil, err
	}
	if maxResults <= 0 || maxResults > mirrorPageLimit {
		maxResults = mirrorPageLimit
	}

	filter := mirrorFilter(userEmail, crit, time.Now())

	total, err := h.db.Emails().CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("count stored messages: %w", err)
	}

	cursor, err := h.db.Emails().Find(ctx, filter,
		options.Find().
			SetSort(bson.D{{Key: "receivedDate", Value: -1}}).
			SetSkip(offset).
			SetLimit(maxResults))
	if err != nil {
		return nil, fmt.Errorf("list stored messages: %w", err)
	}
	defer cursor.Close(ctx)

	emails := make([]models.Email, 0, maxResults)
	if err := cursor.All(ctx, &emails); err != nil {
		return nil, fmt.Errorf("read stored messages: %w", err)
	}

	// A next token only when there is a next page. Handing one out at the end
	// of the list makes the client fetch an empty page to find out.
	nextToken := ""
	if offset+int64(len(emails)) < total {
		nextToken = strconv.FormatInt(offset+int64(len(emails)), 10)
	}

	return map[string]interface{}{
		"emails":             emails,
		"nextPageToken":      nextToken,
		"resultSizeEstimate": total,
	}, nil
}

// parseOffsetToken reads back a token this server handed out.
//
// A token it did not hand out is refused rather than read as zero. The one way
// to get here with a foreign token is a client holding a Gmail page token from
// a previous session, and silently restarting that user at page one would look
// like the list had simply forgotten where they were.
func parseOffsetToken(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	offset, err := strconv.ParseInt(token, 10, 64)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("unusable page token %q", token)
	}
	return offset, nil
}

// statsFromMirror counts what the stored mailbox knows.
//
// It reports what it can and leaves the rest at zero rather than inventing it:
// there is no sent, draft, spam or trash mirror, and LabelStats is Gmail label
// vocabulary that means nothing on a transport where a message is in one
// folder. The frontend renders a counter it has no value for as a dash.
func (h *Handler) statsFromMirror(ctx context.Context, userEmail string) (*gmail.MailboxStats, error) {
	inbox := bson.M{"userId": userEmail, "folder": string(mailbox.FolderInbox)}

	total, err := h.db.Emails().CountDocuments(ctx, inbox)
	if err != nil {
		return nil, fmt.Errorf("count stored messages: %w", err)
	}
	unread, err := h.db.Emails().CountDocuments(ctx, bson.M{
		"userId": userEmail,
		"folder": string(mailbox.FolderInbox),
		"isRead": false,
	})
	if err != nil {
		return nil, fmt.Errorf("count unread stored messages: %w", err)
	}

	return &gmail.MailboxStats{
		TotalMessages: total,
		InboxCount:    uint64(total),
		UnreadCount:   uint64(unread),
		LabelStats:    make([]gmail.LabelStat, 0),
	}, nil
}
