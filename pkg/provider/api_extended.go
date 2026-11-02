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
	CreateConversationContext(ctx context.Context, params slack.CreateConversationParams) (*slack.Channel, error)
	OpenConversationContext(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error)
	InviteUsersToConversationContext(ctx context.Context, channelID string, users ...string) (*slack.Channel, error)
	SetTopicOfConversationContext(ctx context.Context, channelID, topic string) (*slack.Channel, error)
	SetPurposeOfConversationContext(ctx context.Context, channelID, purpose string) (*slack.Channel, error)
	GetUsersInConversationContext(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error)
	GetReactionsContext(ctx context.Context, item slack.ItemRef, params slack.GetReactionsParameters) (slack.ReactedItem, error)
	GetEmojiContext(ctx context.Context) (map[string]string, error)
	AddPinContext(ctx context.Context, channel string, item slack.ItemRef) error
	RemovePinContext(ctx context.Context, channel string, item slack.ItemRef) error
	ListPinsContext(ctx context.Context, channel string) ([]slack.Item, *slack.Paging, error)
	AddUserReminderContext(ctx context.Context, userID, text, time string) (*slack.Reminder, error)
	SetUserCustomStatusContext(ctx context.Context, statusText, statusEmoji string, statusExpiration int64) error
	GetFilesContext(ctx context.Context, params slack.GetFilesParameters) ([]slack.File, *slack.Paging, error)
	DeleteFileContext(ctx context.Context, fileID string) error
	ArchiveConversationContext(ctx context.Context, channelID string) error
	UnArchiveConversationContext(ctx context.Context, channelID string) error
	RenameConversationContext(ctx context.Context, channelID, channelName string) (*slack.Channel, error)
	KickUserFromConversationContext(ctx context.Context, channelID string, user string) error
	ListBookmarksContext(ctx context.Context, channelID string) ([]slack.Bookmark, error)
	AddBookmarkContext(ctx context.Context, channelID string, params slack.AddBookmarkParameters) (slack.Bookmark, error)
	EditBookmarkContext(ctx context.Context, channelID, bookmarkID string, params slack.EditBookmarkParameters) (slack.Bookmark, error)
	RemoveBookmarkContext(ctx context.Context, channelID, bookmarkID string) error
	GetUserPresenceContext(ctx context.Context, user string) (*slack.UserPresence, error)
	SetUserPresenceContext(ctx context.Context, presence string) error
	GetDNDInfoContext(ctx context.Context, user *string) (*slack.DNDStatus, error)
	SetSnoozeContext(ctx context.Context, minutes int) (*slack.DNDStatus, error)
	EndSnoozeContext(ctx context.Context) (*slack.DNDStatus, error)
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

// DeleteScheduledMessageContext skips slack-go's version, which always sends
// as_user: Slack now rejects that deprecated argument with invalid_arguments.
func (c *MCPSlackClient) DeleteScheduledMessageContext(ctx context.Context, params *slack.DeleteScheduledMessageParameters) (bool, error) {
	err := c.APICall(ctx, "chat.deleteScheduledMessage", map[string]string{
		"channel":              params.Channel,
		"scheduled_message_id": params.ScheduledMessageID,
	}, nil)
	return err == nil, err
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

func (c *MCPSlackClient) CreateConversationContext(ctx context.Context, params slack.CreateConversationParams) (*slack.Channel, error) {
	return c.slackClient.CreateConversationContext(ctx, params)
}

func (c *MCPSlackClient) OpenConversationContext(ctx context.Context, params *slack.OpenConversationParameters) (*slack.Channel, bool, bool, error) {
	return c.slackClient.OpenConversationContext(ctx, params)
}

func (c *MCPSlackClient) InviteUsersToConversationContext(ctx context.Context, channelID string, users ...string) (*slack.Channel, error) {
	return c.slackClient.InviteUsersToConversationContext(ctx, channelID, users...)
}

func (c *MCPSlackClient) SetTopicOfConversationContext(ctx context.Context, channelID, topic string) (*slack.Channel, error) {
	return c.slackClient.SetTopicOfConversationContext(ctx, channelID, topic)
}

func (c *MCPSlackClient) SetPurposeOfConversationContext(ctx context.Context, channelID, purpose string) (*slack.Channel, error) {
	return c.slackClient.SetPurposeOfConversationContext(ctx, channelID, purpose)
}

func (c *MCPSlackClient) GetUsersInConversationContext(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
	return c.slackClient.GetUsersInConversationContext(ctx, params)
}

func (c *MCPSlackClient) GetReactionsContext(ctx context.Context, item slack.ItemRef, params slack.GetReactionsParameters) (slack.ReactedItem, error) {
	return c.slackClient.GetReactionsContext(ctx, item, params)
}

func (c *MCPSlackClient) GetEmojiContext(ctx context.Context) (map[string]string, error) {
	return c.slackClient.GetEmojiContext(ctx)
}

func (c *MCPSlackClient) AddPinContext(ctx context.Context, channel string, item slack.ItemRef) error {
	return c.slackClient.AddPinContext(ctx, channel, item)
}

func (c *MCPSlackClient) RemovePinContext(ctx context.Context, channel string, item slack.ItemRef) error {
	return c.slackClient.RemovePinContext(ctx, channel, item)
}

func (c *MCPSlackClient) ListPinsContext(ctx context.Context, channel string) ([]slack.Item, *slack.Paging, error) {
	return c.slackClient.ListPinsContext(ctx, channel)
}

func (c *MCPSlackClient) AddUserReminderContext(ctx context.Context, userID, text, time string) (*slack.Reminder, error) {
	return c.slackClient.AddUserReminderContext(ctx, userID, text, time)
}

func (c *MCPSlackClient) GetFilesContext(ctx context.Context, params slack.GetFilesParameters) ([]slack.File, *slack.Paging, error) {
	return c.slackClient.GetFilesContext(ctx, params)
}

func (c *MCPSlackClient) DeleteFileContext(ctx context.Context, fileID string) error {
	return c.slackClient.DeleteFileContext(ctx, fileID)
}

func (c *MCPSlackClient) ArchiveConversationContext(ctx context.Context, channelID string) error {
	return c.slackClient.ArchiveConversationContext(ctx, channelID)
}

func (c *MCPSlackClient) UnArchiveConversationContext(ctx context.Context, channelID string) error {
	return c.slackClient.UnArchiveConversationContext(ctx, channelID)
}

func (c *MCPSlackClient) RenameConversationContext(ctx context.Context, channelID, channelName string) (*slack.Channel, error) {
	return c.slackClient.RenameConversationContext(ctx, channelID, channelName)
}

func (c *MCPSlackClient) KickUserFromConversationContext(ctx context.Context, channelID string, user string) error {
	return c.slackClient.KickUserFromConversationContext(ctx, channelID, user)
}

func (c *MCPSlackClient) ListBookmarksContext(ctx context.Context, channelID string) ([]slack.Bookmark, error) {
	return c.slackClient.ListBookmarksContext(ctx, channelID)
}

func (c *MCPSlackClient) AddBookmarkContext(ctx context.Context, channelID string, params slack.AddBookmarkParameters) (slack.Bookmark, error) {
	return c.slackClient.AddBookmarkContext(ctx, channelID, params)
}

func (c *MCPSlackClient) EditBookmarkContext(ctx context.Context, channelID, bookmarkID string, params slack.EditBookmarkParameters) (slack.Bookmark, error) {
	return c.slackClient.EditBookmarkContext(ctx, channelID, bookmarkID, params)
}

func (c *MCPSlackClient) RemoveBookmarkContext(ctx context.Context, channelID, bookmarkID string) error {
	return c.slackClient.RemoveBookmarkContext(ctx, channelID, bookmarkID)
}

func (c *MCPSlackClient) GetUserPresenceContext(ctx context.Context, user string) (*slack.UserPresence, error) {
	return c.slackClient.GetUserPresenceContext(ctx, user)
}

func (c *MCPSlackClient) SetUserPresenceContext(ctx context.Context, presence string) error {
	return c.slackClient.SetUserPresenceContext(ctx, presence)
}

func (c *MCPSlackClient) GetDNDInfoContext(ctx context.Context, user *string) (*slack.DNDStatus, error) {
	return c.slackClient.GetDNDInfoContext(ctx, user)
}

func (c *MCPSlackClient) SetSnoozeContext(ctx context.Context, minutes int) (*slack.DNDStatus, error) {
	return c.slackClient.SetSnoozeContext(ctx, minutes)
}

func (c *MCPSlackClient) EndSnoozeContext(ctx context.Context) (*slack.DNDStatus, error) {
	return c.slackClient.EndSnoozeContext(ctx)
}

func (c *MCPSlackClient) SetUserCustomStatusContext(ctx context.Context, statusText, statusEmoji string, statusExpiration int64) error {
	return c.slackClient.SetUserCustomStatusContext(ctx, statusText, statusEmoji, statusExpiration)
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
