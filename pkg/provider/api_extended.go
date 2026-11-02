package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/slack-go/slack"
)

// ExtendedSlackAPI covers the Web API methods this fork adds on top of
// upstream: editing and deleting messages, scheduled messages, file uploads,
// user profiles, canvases, and a raw JSON call for methods slack-go lacks
// (conversations.canvases.create, slackLists.*).
type ExtendedSlackAPI interface {
	UpdateMessageContext(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error)
	DeleteMessageContext(ctx context.Context, channel, messageTimestamp string) (string, string, error)
	ScheduleMessageContext(ctx context.Context, channelID, postAt string, options ...slack.MsgOption) (string, string, error)
	GetScheduledMessagesContext(ctx context.Context, params *slack.GetScheduledMessagesParameters) ([]slack.ScheduledMessage, string, error)
	DeleteScheduledMessageContext(ctx context.Context, params *slack.DeleteScheduledMessageParameters) (bool, error)
	GetPermalinkContext(ctx context.Context, params *slack.PermalinkParameters) (string, error)
	UploadFileContext(ctx context.Context, params slack.UploadFileParameters) (*slack.FileSummary, error)
	GetUserInfoContext(ctx context.Context, user string) (*slack.User, error)
	GetUserProfileContext(ctx context.Context, params *slack.GetUserProfileParameters) (*slack.UserProfile, error)
	CreateCanvasContext(ctx context.Context, title string, documentContent slack.DocumentContent) (string, error)
	EditCanvasContext(ctx context.Context, params slack.EditCanvasParams) error
	DeleteCanvasContext(ctx context.Context, canvasID string) error
	LookupCanvasSectionsContext(ctx context.Context, params slack.LookupCanvasSectionsParams) ([]slack.CanvasSection, error)
	APICall(ctx context.Context, method string, body any, out any) error
}

func (c *MCPSlackClient) UpdateMessageContext(ctx context.Context, channelID, timestamp string, options ...slack.MsgOption) (string, string, string, error) {
	return c.slackClient.UpdateMessageContext(ctx, channelID, timestamp, options...)
}

func (c *MCPSlackClient) DeleteMessageContext(ctx context.Context, channel, messageTimestamp string) (string, string, error) {
	return c.slackClient.DeleteMessageContext(ctx, channel, messageTimestamp)
}

func (c *MCPSlackClient) ScheduleMessageContext(ctx context.Context, channelID, postAt string, options ...slack.MsgOption) (string, string, error) {
	return c.slackClient.ScheduleMessageContext(ctx, channelID, postAt, options...)
}

func (c *MCPSlackClient) GetScheduledMessagesContext(ctx context.Context, params *slack.GetScheduledMessagesParameters) ([]slack.ScheduledMessage, string, error) {
	return c.slackClient.GetScheduledMessagesContext(ctx, params)
}

func (c *MCPSlackClient) DeleteScheduledMessageContext(ctx context.Context, params *slack.DeleteScheduledMessageParameters) (bool, error) {
	return c.slackClient.DeleteScheduledMessageContext(ctx, params)
}

func (c *MCPSlackClient) GetPermalinkContext(ctx context.Context, params *slack.PermalinkParameters) (string, error) {
	return c.slackClient.GetPermalinkContext(ctx, params)
}

func (c *MCPSlackClient) UploadFileContext(ctx context.Context, params slack.UploadFileParameters) (*slack.FileSummary, error) {
	return c.slackClient.UploadFileContext(ctx, params)
}

func (c *MCPSlackClient) GetUserInfoContext(ctx context.Context, user string) (*slack.User, error) {
	return c.slackClient.GetUserInfoContext(ctx, user)
}

func (c *MCPSlackClient) GetUserProfileContext(ctx context.Context, params *slack.GetUserProfileParameters) (*slack.UserProfile, error) {
	return c.slackClient.GetUserProfileContext(ctx, params)
}

func (c *MCPSlackClient) CreateCanvasContext(ctx context.Context, title string, documentContent slack.DocumentContent) (string, error) {
	return c.slackClient.CreateCanvasContext(ctx, title, documentContent)
}

func (c *MCPSlackClient) EditCanvasContext(ctx context.Context, params slack.EditCanvasParams) error {
	return c.slackClient.EditCanvasContext(ctx, params)
}

func (c *MCPSlackClient) DeleteCanvasContext(ctx context.Context, canvasID string) error {
	return c.slackClient.DeleteCanvasContext(ctx, canvasID)
}

func (c *MCPSlackClient) LookupCanvasSectionsContext(ctx context.Context, params slack.LookupCanvasSectionsParams) ([]slack.CanvasSection, error) {
	return c.slackClient.LookupCanvasSectionsContext(ctx, params)
}

// APICall posts body as JSON to a Web API method on the account's own
// workspace endpoint and decodes the response into out. A response with
// "ok": false becomes an error carrying Slack's error code.
func (c *MCPSlackClient) APICall(ctx context.Context, method string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%s: encoding request: %w", method, err)
	}

	endpoint := strings.TrimSuffix(c.teamEndpoint, "/") + "/api/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.authProvider.SlackToken())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: reading response: %w", method, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%s: rate limited, retry after %s seconds", method, resp.Header.Get("Retry-After"))
	}

	var status struct {
		OK       bool   `json:"ok"`
		Error    string `json:"error"`
		Needed   string `json:"needed"`
		Metadata struct {
			Messages []string `json:"messages"`
		} `json:"response_metadata"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return fmt.Errorf("%s: HTTP %d, unreadable response: %w", method, resp.StatusCode, err)
	}
	if !status.OK {
		msg := status.Error
		if status.Needed != "" {
			msg += " (missing scope: " + status.Needed + ")"
		}
		if len(status.Metadata.Messages) > 0 {
			msg += ": " + strings.Join(status.Metadata.Messages, "; ")
		}
		return fmt.Errorf("%s: %s", method, msg)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
