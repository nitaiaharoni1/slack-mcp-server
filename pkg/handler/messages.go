package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nitaiaharoni1/slack-mcp-server/pkg/text"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// maxUploadBytes is Slack's limit for one uploaded file.
const maxUploadBytes = 1 << 30

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
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // keep <, > and & readable: canvases are HTML, messages hold <@U...>
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(strings.TrimRight(out.String(), "\n")), nil
}

// requireLocalFiles guards the arguments that read or write paths on the
// server's machine: over a network transport they would hand that machine's
// files to whoever calls the server.
func (ch *ConversationsHandler) requireLocalFiles(ctx context.Context, arg string) error {
	if transport := ch.ap(ctx).ServerTransport(); transport != "stdio" {
		return fmt.Errorf("%s is only available over the stdio transport, not %s", arg, transport)
	}
	return nil
}

// expandPath resolves ~ and returns an absolute path.
func expandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[1:])
	}
	return filepath.Abs(path)
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

// MessagesForwardHandler shares a message into another conversation. Slack has
// no forward API, so it posts the message's permalink (after an optional
// comment) with unfurling on, which Slack renders as the shared message.
func (ch *ConversationsHandler) MessagesForwardHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("MessagesForwardHandler called", zap.Any("params", request.Params))

	ts := request.GetString("timestamp", "")
	if err := requireTS("timestamp", ts); err != nil {
		return nil, err
	}
	source, err := ch.resolveChannelID(ctx, request.GetString("channel_id", ""))
	if err != nil {
		return nil, err
	}
	if source == "" {
		return nil, errors.New("channel_id is required")
	}
	target, err := ch.writableChannel(ctx, request.GetString("target_channel_id", ""), "messages_forward")
	if err != nil {
		return nil, err
	}
	threadTs := request.GetString("target_thread_ts", "")
	if threadTs != "" {
		if err := requireTS("target_thread_ts", threadTs); err != nil {
			return nil, err
		}
	}

	link, err := ch.ap(ctx).Slack().GetPermalinkContext(ctx, &slack.PermalinkParameters{Channel: source, Ts: ts})
	if err != nil {
		return nil, err
	}
	// Plain text, not a markdown block: Slack unfurls the permalink only then.
	body := link
	if comment := strings.TrimSpace(request.GetString("comment", "")); comment != "" {
		body = text.InlineMrkdwn(comment) + "\n" + link
	}
	options := []slack.MsgOption{slack.MsgOptionText(body, false), slack.MsgOptionEnableLinkUnfurl()}
	if threadTs != "" {
		options = append(options, slack.MsgOptionTS(threadTs))
	}
	respChannel, respTS, err := ch.ap(ctx).Slack().PostMessageContext(ctx, target, options...)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Forwarded message %s from %s to channel %s (ts=%s)", ts, source, respChannel, respTS)), nil
}

type fileSummary struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Title     string   `json:"title"`
	Type      string   `json:"type"`
	Mimetype  string   `json:"mimetype"`
	Size      int      `json:"size"`
	User      string   `json:"user"`
	Created   string   `json:"created"`
	Permalink string   `json:"permalink"`
	SharedIn  []string `json:"shared_in"`
}

func summarizeFile(f slack.File) fileSummary {
	shared := append(append(append([]string{}, f.Channels...), f.Groups...), f.IMs...)
	return fileSummary{
		ID: f.ID, Name: f.Name, Title: f.Title, Type: f.PrettyType, Mimetype: f.Mimetype, Size: f.Size, User: f.User,
		Created: f.Created.Time().UTC().Format(time.RFC3339), Permalink: f.Permalink, SharedIn: shared,
	}
}

// FilesListHandler lists files, newest first, optionally by channel, user or type.
func (ch *ConversationsHandler) FilesListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("FilesListHandler called", zap.Any("params", request.Params))
	params := slack.GetFilesParameters{
		Types: request.GetString("types", ""),
		Count: request.GetInt("limit", 20),
		Page:  request.GetInt("page", 1),
	}
	if raw := request.GetString("channel_id", ""); raw != "" {
		channel, err := ch.resolveChannelID(ctx, raw)
		if err != nil {
			return nil, err
		}
		params.Channel = channel
	}
	if raw := request.GetString("user", ""); raw != "" {
		user, err := ch.resolveUserID(ctx, raw)
		if err != nil {
			return nil, err
		}
		params.User = user
	}
	files, paging, err := ch.ap(ctx).Slack().GetFilesContext(ctx, params)
	if err != nil {
		return nil, err
	}
	// Slack returns the newest page, but oldest first within it.
	sort.SliceStable(files, func(i, j int) bool { return files[i].Created > files[j].Created })
	out := make([]fileSummary, 0, len(files))
	for _, f := range files {
		out = append(out, summarizeFile(f))
	}
	result := map[string]any{"files": out}
	if paging != nil {
		result["page"], result["pages"], result["total"] = paging.Page, paging.Pages, paging.Total
	}
	return jsonResult(result)
}

// FilesInfoHandler returns one file's details.
func (ch *ConversationsHandler) FilesInfoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("FilesInfoHandler called", zap.Any("params", request.Params))
	id := request.GetString("file_id", "")
	if id == "" {
		return nil, errors.New("file_id is required")
	}
	f, _, _, err := ch.ap(ctx).Slack().GetFileInfoContext(ctx, id, 0, 0)
	if err != nil {
		return nil, err
	}
	return jsonResult(summarizeFile(*f))
}

// FilesDeleteHandler deletes a file. Canvases and Lists are files too.
func (ch *ConversationsHandler) FilesDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("FilesDeleteHandler called", zap.Any("params", request.Params))
	if err := requireWrites("SLACK_MCP_ADD_MESSAGE_TOOL", "files_delete"); err != nil {
		return nil, err
	}
	id := request.GetString("file_id", "")
	if id == "" {
		return nil, errors.New("file_id is required")
	}
	if err := ch.ap(ctx).Slack().DeleteFileContext(ctx, id); err != nil {
		return nil, err
	}
	return mcp.NewToolResultText("Deleted file " + id), nil
}

// FilesDownloadHandler saves a file to the local disk, at any size.
func (ch *ConversationsHandler) FilesDownloadHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("FilesDownloadHandler called", zap.Any("params", request.Params))

	if err := ch.requireLocalFiles(ctx, "save_path"); err != nil {
		return nil, err
	}
	id := request.GetString("file_id", "")
	if id == "" {
		return nil, errors.New("file_id is required")
	}
	rawPath := request.GetString("save_path", "")
	if rawPath == "" {
		return nil, errors.New("save_path is required")
	}
	file, _, _, err := ch.ap(ctx).Slack().GetFileInfoContext(ctx, id, 0, 0)
	if err != nil {
		return nil, err
	}
	target, err := downloadTarget(rawPath, file)
	if err != nil {
		return nil, err
	}

	// Download beside the target so a failed transfer leaves no partial file.
	tmp, err := os.CreateTemp(filepath.Dir(target), ".slack-download-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	size, err := ch.fetchFile(ctx, file, tmp)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return nil, err
	}
	return jsonResult(map[string]any{
		"file_id": file.ID, "filename": file.Name, "mimetype": file.Mimetype, "size": size, "saved_to": target,
	})
}

// downloadTarget turns save_path into the file to write: the path itself, or
// the file's own name inside it when it is a directory. It never overwrites.
func downloadTarget(rawPath string, file *slack.File) (string, error) {
	target, err := expandPath(rawPath)
	if err != nil {
		return "", err
	}
	info, statErr := os.Stat(target)
	if strings.HasSuffix(rawPath, "/") || statErr == nil && info.IsDir() {
		name := filepath.Base(file.Name)
		if name == "." || name == ".." || name == string(filepath.Separator) {
			name = file.ID
		}
		target = filepath.Join(target, name)
	}
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("%s already exists: pass another save_path", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	return target, nil
}

// FilesUploadHandler uploads a file, optionally sharing it to a channel or thread.
func (ch *ConversationsHandler) FilesUploadHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("FilesUploadHandler called", zap.Any("params", request.Params))

	path, content, b64 := request.GetString("file_path", ""), request.GetString("content", ""), request.GetString("content_base64", "")
	sources := 0
	for _, source := range []string{path, content, b64} {
		if source != "" {
			sources++
		}
	}
	if sources != 1 {
		return nil, errors.New("give exactly one of file_path, content or content_base64")
	}

	threadTs := request.GetString("thread_ts", "")
	if threadTs != "" {
		if request.GetString("channel_id", "") == "" {
			return nil, errors.New("thread_ts needs channel_id: a thread belongs to a channel")
		}
		if err := requireTS("thread_ts", threadTs); err != nil {
			return nil, err
		}
	}

	var reader io.Reader
	var size int64
	filename := request.GetString("filename", "")
	switch {
	case path != "":
		if err := ch.requireLocalFiles(ctx, "file_path"); err != nil {
			return nil, err
		}
		resolved, err := expandPath(path)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(resolved)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory", resolved)
		}
		reader, size = f, info.Size()
		if filename == "" {
			filename = filepath.Base(resolved)
		}
	case content != "":
		reader, size = strings.NewReader(content), int64(len(content))
	default:
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("content_base64 is not valid base64: %w", err)
		}
		reader, size = bytes.NewReader(data), int64(len(data))
	}
	if filename == "" {
		return nil, errors.New("filename is required when uploading content")
	}
	if size == 0 {
		return nil, errors.New("the file is empty")
	}
	if size > maxUploadBytes {
		return nil, fmt.Errorf("file is %d bytes, the limit is %d", size, maxUploadBytes)
	}

	params := slack.UploadFileParameters{
		Reader:          reader,
		FileSize:        int(size),
		Filename:        filename,
		Title:           request.GetString("title", ""),
		ThreadTimestamp: threadTs,
	}
	if raw := request.GetString("channel_id", ""); raw != "" {
		channel, err := ch.writableChannel(ctx, raw, "files_upload")
		if err != nil {
			return nil, err
		}
		params.Channel = channel
	}
	if comment := strings.TrimSpace(request.GetString("initial_comment", "")); comment != "" {
		if params.Channel == "" {
			return nil, errors.New("initial_comment needs channel_id: the comment is a message in that channel")
		}
		block, err := markdownBlock(comment)
		if err != nil {
			return nil, err
		}
		params.Blocks = slack.Blocks{BlockSet: []slack.Block{block}}
	}

	file, err := ch.ap(ctx).Slack().UploadFileContext(ctx, params)
	if err != nil {
		ch.logger.Error("Slack UploadFileContext failed", zap.Error(err))
		return nil, err
	}
	result := map[string]any{"file_id": file.ID, "title": file.Title, "channel": params.Channel, "size": size}
	if info, _, _, err := ch.ap(ctx).Slack().GetFileInfoContext(ctx, file.ID, 0, 0); err == nil {
		result["permalink"] = info.Permalink
	}
	return jsonResult(result)
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
