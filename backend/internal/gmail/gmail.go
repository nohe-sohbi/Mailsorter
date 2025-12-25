package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type Service struct {
	config *oauth2.Config
}

func NewService(clientID, clientSecret, redirectURL string) *Service {
	config := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes: []string{
			gmail.GmailReadonlyScope,
			gmail.GmailModifyScope,
			gmail.GmailLabelsScope,
		},
		Endpoint: google.Endpoint,
	}

	return &Service{
		config: config,
	}
}

func (s *Service) GetAuthURL(state string) string {
	return s.config.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (s *Service) ExchangeCode(code string) (*oauth2.Token, error) {
	return s.config.Exchange(context.Background(), code)
}

func (s *Service) GetClient(token *oauth2.Token) *gmail.Service {
	client := s.config.Client(context.Background(), token)
	srv, _ := gmail.NewService(context.Background(), option.WithHTTPClient(client))
	return srv
}

func (s *Service) RefreshToken(refreshToken string) (*oauth2.Token, error) {
	token := &oauth2.Token{
		RefreshToken: refreshToken,
	}
	tokenSource := s.config.TokenSource(context.Background(), token)
	return tokenSource.Token()
}

func (s *Service) ListMessages(gmailService *gmail.Service, query string, maxResults int64) ([]*gmail.Message, error) {
	user := "me"
	
	call := gmailService.Users.Messages.List(user).Q(query)
	if maxResults > 0 {
		call = call.MaxResults(maxResults)
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

	return messages, nil
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

func (s *Service) ListLabels(gmailService *gmail.Service) ([]*gmail.Label, error) {
	response, err := gmailService.Users.Labels.List("me").Do()
	if err != nil {
		return nil, err
	}
	return response.Labels, nil
}

func (s *Service) GetUserProfile(gmailService *gmail.Service) (string, error) {
	profile, err := gmailService.Users.GetProfile("me").Do()
	if err != nil {
		return "", err
	}
	return profile.EmailAddress, nil
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
