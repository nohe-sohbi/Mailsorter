package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type Service struct {
	config *oauth2.Config
	mu     sync.RWMutex
}

func NewService(clientID, clientSecret, redirectURL string) *Service {
	var config *oauth2.Config
	if clientID != "" && clientSecret != "" {
		config = &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes: []string{
				gmail.GmailReadonlyScope,
				gmail.GmailModifyScope,
				gmail.GmailLabelsScope,
				gmail.GmailSendScope, // send the daily recap digest as the user
			},
			Endpoint: google.Endpoint,
		}
	}

	return &Service{
		config: config,
	}
}

// UpdateConfig updates the OAuth configuration at runtime (hot reload)
func (s *Service) UpdateConfig(clientID, clientSecret, redirectURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes: []string{
			gmail.GmailReadonlyScope,
			gmail.GmailModifyScope,
			gmail.GmailLabelsScope,
			gmail.GmailSendScope, // send the daily recap digest as the user
		},
		Endpoint: google.Endpoint,
	}
}

// IsConfigured returns true if OAuth credentials are set
func (s *Service) IsConfigured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config != nil && s.config.ClientID != "" && s.config.ClientSecret != ""
}

func (s *Service) GetAuthURL(state string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (s *Service) ExchangeCode(code string) (*oauth2.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Exchange(context.Background(), code)
}

func (s *Service) GetClient(token *oauth2.Token) *gmail.Service {
	s.mu.RLock()
	defer s.mu.RUnlock()
	client := s.config.Client(context.Background(), token)
	srv, _ := gmail.NewService(context.Background(), option.WithHTTPClient(client))
	return srv
}

func (s *Service) RefreshToken(refreshToken string) (*oauth2.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	token := &oauth2.Token{
		RefreshToken: refreshToken,
	}
	tokenSource := s.config.TokenSource(context.Background(), token)
	return tokenSource.Token()
}

// ListMessagesResponse contains messages and pagination info
type ListMessagesResponse struct {
	Messages           []*gmail.Message
	NextPageToken      string
	ResultSizeEstimate int64
}

func (s *Service) ListMessages(gmailService *gmail.Service, query string, maxResults int64) ([]*gmail.Message, error) {
	resp, err := s.ListMessagesWithPagination(gmailService, query, maxResults, "")
	if err != nil {
		return nil, err
	}
	return resp.Messages, nil
}

func (s *Service) ListMessagesWithPagination(gmailService *gmail.Service, query string, maxResults int64, pageToken string) (*ListMessagesResponse, error) {
	user := "me"

	call := gmailService.Users.Messages.List(user).Q(query)
	if maxResults > 0 {
		call = call.MaxResults(maxResults)
	}
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}

	response, err := call.Do()
	if err != nil {
		return nil, err
	}

	messages := make([]*gmail.Message, 0, len(response.Messages))
	for _, m := range response.Messages {
		msg, err := gmailService.Users.Messages.Get(user, m.Id).Format("full").Do()
		if err != nil {
			continue
		}
		messages = append(messages, msg)
	}

	return &ListMessagesResponse{
		Messages:           messages,
		NextPageToken:      response.NextPageToken,
		ResultSizeEstimate: response.ResultSizeEstimate,
	}, nil
}

func (s *Service) GetMessage(gmailService *gmail.Service, messageID string) (*gmail.Message, error) {
	return gmailService.Users.Messages.Get("me", messageID).Format("full").Do()
}

func (s *Service) ModifyMessage(gmailService *gmail.Service, messageID string, addLabels, removeLabels []string) error {
	modifyRequest := &gmail.ModifyMessageRequest{
		AddLabelIds:    addLabels,
		RemoveLabelIds: removeLabels,
	}
	_, err := gmailService.Users.Messages.Modify("me", messageID, modifyRequest).Do()
	return err
}

// SendMessage sends a pre-built RFC 2822, base64url-encoded message (see
// internal/mailer.BuildRaw) as the authenticated user. Used for the daily digest.
func (s *Service) SendMessage(gmailService *gmail.Service, raw string) error {
	_, err := gmailService.Users.Messages.Send("me", &gmail.Message{Raw: raw}).Do()
	return err
}

func (s *Service) ListLabels(gmailService *gmail.Service) ([]*gmail.Label, error) {
	response, err := gmailService.Users.Labels.List("me").Do()
	if err != nil {
		return nil, err
	}
	return response.Labels, nil
}

func (s *Service) CreateLabel(gmailService interface{}, name string) (string, error) {
	srv, ok := gmailService.(*gmail.Service)
	if !ok {
		return "", fmt.Errorf("invalid gmail service")
	}

	// First check if label already exists
	existingLabels, err := s.ListLabels(srv)
	if err == nil {
		for _, label := range existingLabels {
			if label.Name == name {
				return label.Id, nil
			}
		}
	}

	// Create new label
	label := &gmail.Label{
		Name:                  name,
		LabelListVisibility:   "labelShow",
		MessageListVisibility: "show",
	}

	created, err := srv.Users.Labels.Create("me", label).Do()
	if err != nil {
		return "", err
	}

	return created.Id, nil
}

func (s *Service) GetUserProfile(gmailService *gmail.Service) (string, error) {
	profile, err := gmailService.Users.GetProfile("me").Do()
	if err != nil {
		return "", err
	}
	return profile.EmailAddress, nil
}

// MailboxStats contains statistics about the user's mailbox
type MailboxStats struct {
	TotalMessages int64       `json:"totalMessages"`
	TotalThreads  int64       `json:"totalThreads"`
	UnreadCount   uint64      `json:"unreadCount"`
	InboxCount    uint64      `json:"inboxCount"`
	SentCount     uint64      `json:"sentCount"`
	DraftCount    uint64      `json:"draftCount"`
	SpamCount     uint64      `json:"spamCount"`
	TrashCount    uint64      `json:"trashCount"`
	LabelStats    []LabelStat `json:"labelStats"`
}

// LabelStat contains message count for a specific label
type LabelStat struct {
	LabelID        string `json:"labelId"`
	LabelName      string `json:"labelName"`
	MessagesTotal  int64  `json:"messagesTotal"`
	MessagesUnread int64  `json:"messagesUnread"`
	ThreadsTotal   int64  `json:"threadsTotal"`
	Type           string `json:"type"`
}

// GetMailboxStats retrieves comprehensive mailbox statistics
func (s *Service) GetMailboxStats(gmailService *gmail.Service) (*MailboxStats, error) {
	user := "me"

	// Get user profile for total counts
	profile, err := gmailService.Users.GetProfile(user).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get profile: %w", err)
	}

	stats := &MailboxStats{
		TotalMessages: profile.MessagesTotal,
		TotalThreads:  profile.ThreadsTotal,
		LabelStats:    make([]LabelStat, 0),
	}

	// Get all labels with their stats
	labels, err := gmailService.Users.Labels.List(user).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list labels: %w", err)
	}

	for _, label := range labels.Labels {
		// Get detailed label info including message counts
		labelDetail, err := gmailService.Users.Labels.Get(user, label.Id).Do()
		if err != nil {
			continue
		}

		labelStat := LabelStat{
			LabelID:        labelDetail.Id,
			LabelName:      labelDetail.Name,
			MessagesTotal:  labelDetail.MessagesTotal,
			MessagesUnread: labelDetail.MessagesUnread,
			ThreadsTotal:   labelDetail.ThreadsTotal,
			Type:           labelDetail.Type,
		}
		stats.LabelStats = append(stats.LabelStats, labelStat)

		// Extract key counts
		switch labelDetail.Id {
		case "INBOX":
			stats.InboxCount = uint64(labelDetail.MessagesTotal)
		case "UNREAD":
			stats.UnreadCount = uint64(labelDetail.MessagesTotal)
		case "SENT":
			stats.SentCount = uint64(labelDetail.MessagesTotal)
		case "DRAFT":
			stats.DraftCount = uint64(labelDetail.MessagesTotal)
		case "SPAM":
			stats.SpamCount = uint64(labelDetail.MessagesTotal)
		case "TRASH":
			stats.TrashCount = uint64(labelDetail.MessagesTotal)
		}
	}

	return stats, nil
}

func ParseEmailHeaders(message *gmail.Message) (from, subject string, to []string, date time.Time) {
	for _, header := range message.Payload.Headers {
		switch header.Name {
		case "From":
			from = header.Value
		case "Subject":
			subject = header.Value
		case "To":
			to = append(to, header.Value)
		case "Date":
			date, _ = time.Parse(time.RFC1123Z, header.Value)
		}
	}
	return
}

func GetEmailBody(message *gmail.Message) string {
	if message.Payload.Body.Data != "" {
		return message.Payload.Body.Data
	}

	for _, part := range message.Payload.Parts {
		if part.MimeType == "text/plain" || part.MimeType == "text/html" {
			if part.Body.Data != "" {
				return part.Body.Data
			}
		}
	}

	return ""
}

// ParseUnsubscribe extracts the unsubscribe affordances a sender advertises via
// the RFC 2369 `List-Unsubscribe` and RFC 8058 `List-Unsubscribe-Post` headers.
// httpURL is the preferred https endpoint, mailto the fallback address, and
// oneClick reports whether the sender supports a silent POST-based unsubscribe.
func ParseUnsubscribe(message *gmail.Message) (httpURL, mailto string, oneClick bool) {
	if message == nil || message.Payload == nil {
		return
	}
	var listUnsub, listUnsubPost string
	for _, hdr := range message.Payload.Headers {
		switch strings.ToLower(hdr.Name) {
		case "list-unsubscribe":
			listUnsub = hdr.Value
		case "list-unsubscribe-post":
			listUnsubPost = hdr.Value
		}
	}
	for _, token := range splitAngleList(listUnsub) {
		low := strings.ToLower(token)
		switch {
		case (strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://")) && httpURL == "":
			httpURL = token
		case strings.HasPrefix(low, "mailto:") && mailto == "":
			mailto = token
		}
	}
	oneClick = httpURL != "" && strings.Contains(strings.ToLower(listUnsubPost), "one-click")
	return
}

// splitAngleList parses a comma-separated list of <...>-wrapped URIs.
func splitAngleList(v string) []string {
	out := make([]string, 0, 2)
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "<")
		part = strings.TrimSuffix(part, ">")
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// OneClickUnsubscribe performs an RFC 8058 one-click unsubscribe: an HTTP POST to
// the sender's https endpoint with the body `List-Unsubscribe=One-Click`. It must
// only be used when ParseUnsubscribe reported oneClick == true.
func (s *Service) OneClickUnsubscribe(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("unsupported unsubscribe scheme: %s", u.Scheme)
	}

	client := &http.Client{Timeout: 12 * time.Second}
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader("List-Unsubscribe=One-Click"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mailsorter/1.0 (+unsubscribe)")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("unsubscribe endpoint returned %d", resp.StatusCode)
	}
	return nil
}

func TokenToJSON(token *oauth2.Token) (string, error) {
	data, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func TokenFromJSON(data string) (*oauth2.Token, error) {
	var token oauth2.Token
	err := json.Unmarshal([]byte(data), &token)
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func ValidateToken(token *oauth2.Token) error {
	if token.AccessToken == "" {
		return fmt.Errorf("access token is empty")
	}
	if token.Expiry.Before(time.Now()) {
		return fmt.Errorf("token expired")
	}
	return nil
}
