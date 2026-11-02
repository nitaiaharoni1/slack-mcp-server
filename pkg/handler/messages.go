package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// maxUploadBytes caps files_upload content, matching attachment_get_data's limit.
const maxUploadBytes = 5 * 1024 * 1024

// writableChannel resolves a channel argument for a tool that changes messages
// and applies the same SLACK_MCP_ADD_MESSAGE_TOOL policy as conversations_add_message.
func (ch *ConversationsHandler) writableChannel(ctx context.Context, raw, tool string) (string, error) {
	policy := os.Getenv("SLACK_MCP_ADD_MESSAGE_TOOL")
	if policy == "" && !strings.Contains(os.Getenv("SLACK_MCP_ENABLED_TOOLS"), tool) {
		return "", fmt.Errorf("%s is disabled: set SLACK_MCP_ADD_MESSAGE_TOOL to true, or to the channels it may write to", tool)
	}
	if raw == "" {
		return "", errors.New("channel_id is required")
	}
	channel, err := ch.resolveChannelID(ctx, raw)
	if err != nil {
		return "", err
	}
	if !isChannelAllowed(channel) {
		return "", fmt.Errorf("%s is not allowed for channel %q, applied policy: %s", tool, channel, policy)
	}
	return channel, nil
}

func requireTS(name, ts string) error {
	if ts == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !strings.Contains(ts, ".") {
		return fmt.Errorf("%s must be a message timestamp in format 1234567890.123456", name)
	}
	return nil
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(out)), nil
}

// MessagesUpdateHandler edits a message the account posted (chat.update).
func (ch *ConversationsHandler) MessagesUpdateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("MessagesUpdateHandler called", zap.Any("params", request.Params))

	ts := request.GetString("timestamp", "")
	if err := requireTS("timestamp", ts); err != nil {
		return nil, err
	}
	params, err := ch.parseParamsToolAddMessage(ctx, request)
	if err != nil {
		return nil, err
	}
	options, err := ch.messageContentOptions(params)
	if err != nil {
		return nil, err
	}

	channel, newTS, _, err := ch.ap(ctx).Slack().UpdateMessageContext(ctx, params.channel, ts, options...)
	if err != nil {
		ch.logger.Error("Slack UpdateMessageContext failed", zap.Error(err))
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Updated message in channel %s (ts=%s)", channel, newTS)), nil
}

// MessagesDeleteHandler deletes a message the account posted (chat.delete).
func (ch *ConversationsHandler) MessagesDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("MessagesDeleteHandler called", zap.Any("params", request.Params))

	ts := request.GetString("timestamp", "")
	if err := requireTS("timestamp", ts); err != nil {
		return nil, err
	}
	channel, err := ch.writableChannel(ctx, request.GetString("channel_id", ""), "messages_delete")
	if err != nil {
		return nil, err
	}

	respChannel, respTS, err := ch.ap(ctx).Slack().DeleteMessageContext(ctx, channel, ts)
	if err != nil {
		ch.logger.Error("Slack DeleteMessageContext failed", zap.Error(err))
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Deleted message %s in channel %s", respTS, respChannel)), nil
}

// parsePostAt accepts a Unix timestamp or an RFC 3339 time and returns Unix seconds.
func parsePostAt(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("post_at is required")
	}
	if secs, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return secs, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, fmt.Errorf("post_at must be a Unix timestamp or an RFC 3339 time such as 2026-11-05T09:00:00+02:00: %w", err)
	}
	return t.Unix(), nil
}

// MessagesScheduleHandler schedules a message for later (chat.scheduleMessage).
func (ch *ConversationsHandler) MessagesScheduleHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("MessagesScheduleHandler called", zap.Any("params", request.Params))

	postAt, err := parsePostAt(request.GetString("post_at", ""))
	if err != nil {
		return nil, err
	}
	if postAt <= time.Now().Unix() {
		return nil, errors.New("post_at must be in the future")
	}
	params, err := ch.parseParamsToolAddMessage(ctx, request)
	if err != nil {
		return nil, err
	}
	options, err := ch.messageContentOptions(params)
	if err != nil {
		return nil, err
	}
	if params.threadTs != "" {
		options = append(options, slack.MsgOptionTS(params.threadTs))
	}

	channel, scheduledID, err := ch.ap(ctx).Slack().ScheduleMessageContext(ctx, params.channel, strconv.FormatInt(postAt, 10), options...)
	if err != nil {
		ch.logger.Error("Slack ScheduleMessageContext failed", zap.Error(err))
		return nil, err
	}
	return jsonResult(map[string]any{
		"channel":              channel,
		"scheduled_message_id": scheduledID,
		"post_at":              time.Unix(postAt, 0).UTC().Format(time.RFC3339),
	})
}

// ScheduledListHandler lists the account's pending scheduled messages.
func (ch *ConversationsHandler) ScheduledListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ScheduledListHandler called", zap.Any("params", request.Params))

	params := &slack.GetScheduledMessagesParameters{
		Cursor: request.GetString("cursor", ""),
		Limit:  request.GetInt("limit", 100),
	}
	if raw := request.GetString("channel_id", ""); raw != "" {
		channel, err := ch.resolveChannelID(ctx, raw)
		if err != nil {
			return nil, err
		}
		params.Channel = channel
	}

	msgs, next, err := ch.ap(ctx).Slack().GetScheduledMessagesContext(ctx, params)
	if err != nil {
		ch.logger.Error("Slack GetScheduledMessagesContext failed", zap.Error(err))
		return nil, err
	}
	type scheduled struct {
		ID      string `json:"scheduled_message_id"`
		Channel string `json:"channel"`
		PostAt  string `json:"post_at"`
		Text    string `json:"text"`
	}
	out := make([]scheduled, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, scheduled{ID: m.ID, Channel: m.Channel, PostAt: time.Unix(int64(m.PostAt), 0).UTC().Format(time.RFC3339), Text: m.Text})
	}
	return jsonResult(map[string]any{"scheduled_messages": out, "next_cursor": next})
}

// ScheduledDeleteHandler cancels a pending scheduled message.
func (ch *ConversationsHandler) ScheduledDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ScheduledDeleteHandler called", zap.Any("params", request.Params))

	id := request.GetString("scheduled_message_id", "")
	if id == "" {
		return nil, errors.New("scheduled_message_id is required")
	}
	channel, err := ch.writableChannel(ctx, request.GetString("channel_id", ""), "messages_scheduled_delete")
	if err != nil {
		return nil, err
	}

	if _, err := ch.ap(ctx).Slack().DeleteScheduledMessageContext(ctx, &slack.DeleteScheduledMessageParameters{
		Channel:            channel,
		ScheduledMessageID: id,
	}); err != nil {
		ch.logger.Error("Slack DeleteScheduledMessageContext failed", zap.Error(err))
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Cancelled scheduled message %s in channel %s", id, channel)), nil
}

// MessagesPermalinkHandler returns a message's permalink.
func (ch *ConversationsHandler) MessagesPermalinkHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("MessagesPermalinkHandler called", zap.Any("params", request.Params))

	ts := request.GetString("timestamp", "")
	if err := requireTS("timestamp", ts); err != nil {
		return nil, err
	}
	channel, err := ch.resolveChannelID(ctx, request.GetString("channel_id", ""))
	if err != nil {
		return nil, err
	}
	if channel == "" {
		return nil, errors.New("channel_id is required")
	}

	link, err := ch.ap(ctx).Slack().GetPermalinkContext(ctx, &slack.PermalinkParameters{Channel: channel, Ts: ts})
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(link), nil
}

// FilesUploadHandler uploads a file, optionally sharing it to a channel or thread.
func (ch *ConversationsHandler) FilesUploadHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("FilesUploadHandler called", zap.Any("params", request.Params))

	var data []byte
	filename := request.GetString("filename", "")
	switch path, content, b64 := request.GetString("file_path", ""), request.GetString("content", ""), request.GetString("content_base64", ""); {
	case path != "":
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.Size() > maxUploadBytes {
			return nil, fmt.Errorf("file is %d bytes, the limit is %d", info.Size(), maxUploadBytes)
		}
		if data, err = os.ReadFile(path); err != nil {
			return nil, err
		}
		if filename == "" {
			filename = filepath.Base(path)
		}
	case content != "":
		data = []byte(content)
	case b64 != "":
		var err error
		if data, err = base64.StdEncoding.DecodeString(b64); err != nil {
			return nil, fmt.Errorf("content_base64 is not valid base64: %w", err)
		}
	default:
		return nil, errors.New("one of file_path, content or content_base64 is required")
	}
	if filename == "" {
		return nil, errors.New("filename is required when uploading content")
	}
	if len(data) == 0 {
		return nil, errors.New("the file is empty")
	}
	if len(data) > maxUploadBytes {
		return nil, fmt.Errorf("file is %d bytes, the limit is %d", len(data), maxUploadBytes)
	}

	params := slack.UploadFileParameters{
		Reader:          bytes.NewReader(data),
		FileSize:        len(data),
		Filename:        filename,
		Title:           request.GetString("title", ""),
		InitialComment:  request.GetString("initial_comment", ""),
		ThreadTimestamp: request.GetString("thread_ts", ""),
	}
	if raw := request.GetString("channel_id", ""); raw != "" {
		channel, err := ch.writableChannel(ctx, raw, "files_upload")
		if err != nil {
			return nil, err
		}
		params.Channel = channel
	}

	file, err := ch.ap(ctx).Slack().UploadFileContext(ctx, params)
	if err != nil {
		ch.logger.Error("Slack UploadFileContext failed", zap.Error(err))
		return nil, err
	}
	return jsonResult(map[string]any{"file_id": file.ID, "title": file.Title, "channel": params.Channel})
}

// UsersInfoHandler returns one user's full profile, including custom fields.
func (ch *ConversationsHandler) UsersInfoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("UsersInfoHandler called", zap.Any("params", request.Params))

	userID, err := ch.resolveUserID(ctx, request.GetString("user_id", ""))
	if err != nil {
		return nil, err
	}

	user, err := ch.ap(ctx).Slack().GetUserInfoContext(ctx, userID)
	if err != nil {
		return nil, err
	}
	profile, err := ch.ap(ctx).Slack().GetUserProfileContext(ctx, &slack.GetUserProfileParameters{UserID: userID, IncludeLabels: true})
	if err != nil {
		// users.profile.get needs users.profile:read; fall back to users.info's profile.
		ch.logger.Debug("GetUserProfileContext failed, using users.info profile", zap.Error(err))
		profile = &user.Profile
	}

	fields := map[string]string{}
	for id, f := range profile.FieldsMap() {
		label := f.Label
		if label == "" {
			label = id
		}
		fields[label] = f.Value
	}
	return jsonResult(map[string]any{
		"id":           user.ID,
		"name":         user.Name,
		"real_name":    user.RealName,
		"display_name": profile.DisplayName,
		"title":        profile.Title,
		"email":        profile.Email,
		"phone":        profile.Phone,
		"status_text":  profile.StatusText,
		"status_emoji": profile.StatusEmoji,
		"timezone":     user.TZ,
		"is_admin":     user.IsAdmin,
		"is_bot":       user.IsBot,
		"deleted":      user.Deleted,
		"custom":       fields,
	})
}
