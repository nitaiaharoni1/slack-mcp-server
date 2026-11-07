package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/rusq/slackdump/v3/auth"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// DraftCaller calls Slack's drafts.* methods. They are internal to Slack's own
// clients: undocumented, form-encoded, and refused for OAuth tokens with
// not_allowed_token_type, so they need browser session tokens (xoxc + xoxd).
type DraftCaller interface {
	DraftCall(ctx context.Context, method string, form url.Values, out any) error
}

// DraftCall posts form to a drafts.* method with the client's token.
func (c *MCPSlackClient) DraftCall(ctx context.Context, method string, form url.Values, out any) error {
	body := url.Values{"token": {c.authProvider.SlackToken()}}
	for k, v := range form {
		body[k] = v
	}
	endpoint := strings.TrimSuffix(c.teamEndpoint, "/") + "/api/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body.Encode()))
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	return decodeAPIResponse(method, resp, out)
}

// sessionAuth returns the session tokens set next to an OAuth token, or nil.
// A broken pair must not stop the account's other tools, so it only warns here
// and the draft tools report it when called.
func (c Credentials) sessionAuth(logger *zap.Logger) auth.Provider {
	if c.XOXP == "" && c.XOXB == "" {
		return nil // session tokens, if any, are already the account's main auth
	}
	if c.XOXC == "" && c.XOXD == "" {
		return nil
	}
	if c.XOXC == "" || c.XOXD == "" {
		logger.Warn("Only one of the xoxc and xoxd tokens is set; drafts need both.", zap.String("context", "console"))
		return nil
	}
	session, err := auth.NewValueAuth(c.XOXC, c.XOXD)
	if err != nil {
		logger.Warn("Session tokens are unusable; drafts are unavailable.", zap.String("context", "console"), zap.Error(err))
		return nil
	}
	return session
}

// Drafts returns the client for the drafts.* methods: the account's own client
// when it runs on session tokens, else one built on its session tokens the
// first time drafts are used. A failure is not kept, so a later call retries.
func (ap *ApiProvider) Drafts() (DraftCaller, error) {
	primary, ok := ap.client.(*MCPSlackClient)
	if !ok || primary == nil {
		return nil, fmt.Errorf("account %q has no live Slack client", ap.name)
	}
	if primary.IsBotToken() {
		return nil, fmt.Errorf("account %q uses a bot token, and drafts belong to a user", ap.name)
	}
	if !primary.IsOAuth() {
		return primary, nil
	}

	ap.draftsMu.Lock()
	defer ap.draftsMu.Unlock()
	if ap.draftsClient != nil {
		return ap.draftsClient, nil
	}
	if ap.sessionAuth == nil {
		return nil, fmt.Errorf("account %q has no session tokens: Slack refuses drafts for OAuth tokens. Set both %sXOXC_TOKEN and %sXOXD_TOKEN",
			ap.name, AccountEnvPrefix(ap.name), AccountEnvPrefix(ap.name))
	}
	session, err := NewMCPSlackClient(ap.sessionAuth, ap.logger)
	if err != nil {
		return nil, fmt.Errorf("account %q: its session tokens failed, they may have expired: %w", ap.name, err)
	}
	if err := sameUser(ap.name, primary.AuthResponse(), session.AuthResponse()); err != nil {
		return nil, err
	}
	ap.draftsClient = session
	return session, nil
}

// sameUser refuses session tokens of another user or workspace, which would
// save drafts somewhere other than the account the caller named.
func sameUser(account string, primary, session *slack.AuthTestResponse) error {
	if primary == nil || session == nil {
		return errors.New("account " + account + ": cannot compare its token owners")
	}
	if primary.TeamID != session.TeamID || primary.UserID != session.UserID {
		return fmt.Errorf("account %q: its session tokens belong to user %s in workspace %s, its main token to user %s in workspace %s",
			account, session.UserID, session.TeamID, primary.UserID, primary.TeamID)
	}
	return nil
}
