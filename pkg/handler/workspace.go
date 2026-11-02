package handler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// resolveUserID accepts a user ID, @handle, name or email and returns the user ID.
func (ch *ConversationsHandler) resolveUserID(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
	if raw == "" {
		return "", errors.New("user is empty")
	}
	if isSlackUserIDPrefix(raw) {
		return raw, nil
	}
	users, err := ch.ap(ctx).SearchUsers(ctx, raw, 2)
	if err != nil {
		return "", err
	}
	switch len(users) {
	case 0:
		return "", fmt.Errorf("user %q not found in this workspace", raw)
	case 1:
		return users[0].ID, nil
	}
	return "", fmt.Errorf("user %q is ambiguous (%s, %s, ...): pass the user ID", raw, users[0].Name, users[1].Name)
}

func (ch *ConversationsHandler) resolveUserIDs(ctx context.Context, list string) ([]string, error) {
	var ids []string
	for _, raw := range strings.Split(list, ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		id, err := ch.resolveUserID(ctx, raw)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.New("users is required")
	}
	return ids, nil
}

func channelSummary(c *slack.Channel) map[string]any {
	return map[string]any{
		"id":           c.ID,
		"name":         c.Name,
		"is_private":   c.IsPrivate,
		"is_im":        c.IsIM,
		"is_mpim":      c.IsMpIM,
		"is_archived":  c.IsArchived,
		"is_member":    c.IsMember,
		"topic":        c.Topic.Value,
		"purpose":      c.Purpose.Value,
		"member_count": c.NumMembers,
		"created":      time.Unix(int64(c.Created), 0).UTC().Format(time.RFC3339),
	}
}

// ConversationsCreateHandler creates a channel, or opens a DM / group DM with users.
func (ch *ConversationsHandler) ConversationsCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ConversationsCreateHandler called", zap.Any("params", request.Params))
	if err := requireWrites("SLACK_MCP_ADD_MESSAGE_TOOL", "conversations_create"); err != nil {
		return nil, err
	}

	name := strings.TrimPrefix(strings.TrimSpace(request.GetString("name", "")), "#")
	users := request.GetString("users", "")
	switch {
	case name != "" && users != "":
		return nil, errors.New("pass name to create a channel, or users to open a DM, not both")
	case name != "":
		c, err := ch.ap(ctx).Slack().CreateConversationContext(ctx, slack.CreateConversationParams{
			ChannelName: name,
			IsPrivate:   request.GetBool("is_private", false),
		})
		if err != nil {
			return nil, err
		}
		return jsonResult(channelSummary(c))
	case users != "":
		ids, err := ch.resolveUserIDs(ctx, users)
		if err != nil {
			return nil, err
		}
		c, _, _, err := ch.ap(ctx).Slack().OpenConversationContext(ctx, &slack.OpenConversationParameters{Users: ids, ReturnIM: true})
		if err != nil {
			return nil, err
		}
		return jsonResult(map[string]any{"channel_id": c.ID, "users": ids})
	}
	return nil, errors.New("pass name (new channel) or users (DM or group DM)")
}

// ConversationsInfoHandler returns a channel's details.
func (ch *ConversationsHandler) ConversationsInfoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ConversationsInfoHandler called", zap.Any("params", request.Params))
	channel, err := ch.resolveChannelID(ctx, request.GetString("channel_id", ""))
	if err != nil {
		return nil, err
	}
	if channel == "" {
		return nil, errors.New("channel_id is required")
	}
	c, err := ch.ap(ctx).Slack().GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: channel, IncludeNumMembers: true})
	if err != nil {
		return nil, err
	}
	return jsonResult(channelSummary(c))
}

// ConversationsMembersHandler lists a channel's members with their names.
func (ch *ConversationsHandler) ConversationsMembersHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ConversationsMembersHandler called", zap.Any("params", request.Params))
	channel, err := ch.resolveChannelID(ctx, request.GetString("channel_id", ""))
	if err != nil {
		return nil, err
	}
	if channel == "" {
		return nil, errors.New("channel_id is required")
	}
	ids, next, err := ch.ap(ctx).Slack().GetUsersInConversationContext(ctx, &slack.GetUsersInConversationParameters{
		ChannelID: channel,
		Cursor:    request.GetString("cursor", ""),
		Limit:     request.GetInt("limit", 200),
	})
	if err != nil {
		return nil, err
	}
	resolver := ch.newUserResolver(ctx)
	type member struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		RealName string `json:"real_name"`
	}
	members := make([]member, 0, len(ids))
	for _, id := range ids {
		name, real, _ := resolver.resolve(id)
		members = append(members, member{ID: id, Name: name, RealName: real})
	}
	return jsonResult(map[string]any{"channel": channel, "members": members, "next_cursor": next})
}

// ConversationsInviteHandler adds users to a channel.
func (ch *ConversationsHandler) ConversationsInviteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ConversationsInviteHandler called", zap.Any("params", request.Params))
	channel, err := ch.writableChannel(ctx, request.GetString("channel_id", ""), "conversations_invite")
	if err != nil {
		return nil, err
	}
	ids, err := ch.resolveUserIDs(ctx, request.GetString("users", ""))
	if err != nil {
		return nil, err
	}
	if _, err := ch.ap(ctx).Slack().InviteUsersToConversationContext(ctx, channel, ids...); err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Added %s to channel %s", strings.Join(ids, ", "), channel)), nil
}

// ConversationsSetTopicHandler sets a channel's topic and/or purpose (description).
func (ch *ConversationsHandler) ConversationsSetTopicHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ConversationsSetTopicHandler called", zap.Any("params", request.Params))
	channel, err := ch.writableChannel(ctx, request.GetString("channel_id", ""), "conversations_set_topic")
	if err != nil {
		return nil, err
	}
	args := request.GetArguments()
	_, hasTopic := args["topic"]
	_, hasPurpose := args["purpose"]
	if !hasTopic && !hasPurpose {
		return nil, errors.New("pass topic, purpose, or both")
	}
	if hasTopic {
		if _, err := ch.ap(ctx).Slack().SetTopicOfConversationContext(ctx, channel, request.GetString("topic", "")); err != nil {
			return nil, err
		}
	}
	if hasPurpose {
		if _, err := ch.ap(ctx).Slack().SetPurposeOfConversationContext(ctx, channel, request.GetString("purpose", "")); err != nil {
			return nil, err
		}
	}
	return mcp.NewToolResultText(fmt.Sprintf("Updated channel %s", channel)), nil
}

// ReactionsGetHandler lists the reactions on a message and who added them.
func (ch *ConversationsHandler) ReactionsGetHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ReactionsGetHandler called", zap.Any("params", request.Params))
	ts := request.GetString("timestamp", "")
	if err := requireTS("timestamp", ts); err != nil {
		return nil, err
	}
	channel, err := ch.resolveChannelID(ctx, request.GetString("channel_id", ""))
	if err != nil {
		return nil, err
	}
	item, err := ch.ap(ctx).Slack().GetReactionsContext(ctx, slack.NewRefToMessage(channel, ts), slack.GetReactionsParameters{Full: true})
	if err != nil {
		return nil, err
	}
	resolver := ch.newUserResolver(ctx)
	type reaction struct {
		Emoji string   `json:"emoji"`
		Count int      `json:"count"`
		Users []string `json:"users"`
	}
	out := make([]reaction, 0, len(item.Reactions))
	for _, r := range item.Reactions {
		names := make([]string, 0, len(r.Users))
		for _, id := range r.Users {
			name, _, _ := resolver.resolve(id)
			names = append(names, name)
		}
		out = append(out, reaction{Emoji: r.Name, Count: r.Count, Users: names})
	}
	return jsonResult(map[string]any{"channel": channel, "timestamp": ts, "reactions": out})
}

// EmojiSearchHandler lists the workspace's custom emoji, optionally filtered by name.
func (ch *ConversationsHandler) EmojiSearchHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("EmojiSearchHandler called", zap.Any("params", request.Params))
	emoji, err := ch.ap(ctx).Slack().GetEmojiContext(ctx)
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.Trim(request.GetString("query", ""), ": "))
	limit := request.GetInt("limit", 100)
	var names []string
	for name := range emoji {
		if query == "" || strings.Contains(name, query) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	total := len(names)
	if len(names) > limit {
		names = names[:limit]
	}
	return jsonResult(map[string]any{"custom_emoji": names, "total_matches": total})
}

// PinsHandler adds, removes or lists pinned messages in a channel.
func (ch *ConversationsHandler) PinsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("PinsHandler called", zap.Any("params", request.Params))
	action := request.GetString("action", "list")
	if action == "list" {
		channel, err := ch.resolveChannelID(ctx, request.GetString("channel_id", ""))
		if err != nil {
			return nil, err
		}
		items, _, err := ch.ap(ctx).Slack().ListPinsContext(ctx, channel)
		if err != nil {
			return nil, err
		}
		type pin struct {
			Timestamp string `json:"timestamp"`
			User      string `json:"user"`
			Text      string `json:"text"`
		}
		out := make([]pin, 0, len(items))
		for _, it := range items {
			if it.Message != nil {
				out = append(out, pin{Timestamp: it.Message.Timestamp, User: it.Message.User, Text: it.Message.Text})
			}
		}
		return jsonResult(map[string]any{"channel": channel, "pins": out})
	}

	if action != "add" && action != "remove" {
		return nil, errors.New("action must be list, add or remove")
	}
	ts := request.GetString("timestamp", "")
	if err := requireTS("timestamp", ts); err != nil {
		return nil, err
	}
	channel, err := ch.writableChannel(ctx, request.GetString("channel_id", ""), "pins")
	if err != nil {
		return nil, err
	}
	ref := slack.NewRefToMessage(channel, ts)
	if action == "add" {
		err = ch.ap(ctx).Slack().AddPinContext(ctx, channel, ref)
	} else {
		err = ch.ap(ctx).Slack().RemovePinContext(ctx, channel, ref)
	}
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Pin %s: message %s in channel %s", action, ts, channel)), nil
}

// ConversationsManageHandler archives, unarchives or renames a channel, or
// removes someone from it.
func (ch *ConversationsHandler) ConversationsManageHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("ConversationsManageHandler called", zap.Any("params", request.Params))
	channel, err := ch.writableChannel(ctx, request.GetString("channel_id", ""), "conversations_manage")
	if err != nil {
		return nil, err
	}
	api := ch.ap(ctx).Slack()
	switch action := request.GetString("action", ""); action {
	case "archive":
		err = api.ArchiveConversationContext(ctx, channel)
	case "unarchive":
		err = api.UnArchiveConversationContext(ctx, channel)
	case "rename":
		name := request.GetString("name", "")
		if name == "" {
			return nil, errors.New("rename needs name")
		}
		_, err = api.RenameConversationContext(ctx, channel, name)
	case "kick":
		user, uerr := ch.resolveUserID(ctx, request.GetString("user", ""))
		if uerr != nil {
			return nil, uerr
		}
		err = api.KickUserFromConversationContext(ctx, channel, user)
	default:
		return nil, errors.New("action must be archive, unarchive, rename or kick")
	}
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Done: %s on channel %s", request.GetString("action", ""), channel)), nil
}

// BookmarksHandler lists, adds, edits or removes a channel's bookmarks.
func (ch *ConversationsHandler) BookmarksHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("BookmarksHandler called", zap.Any("params", request.Params))
	action := request.GetString("action", "list")
	api := ch.ap(ctx).Slack()
	type bookmark struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Link  string `json:"link"`
		Emoji string `json:"emoji,omitempty"`
		Type  string `json:"type"`
	}
	summary := func(b slack.Bookmark) bookmark {
		return bookmark{ID: b.ID, Title: b.Title, Link: b.Link, Emoji: b.Emoji, Type: b.Type}
	}

	if action == "list" {
		channel, err := ch.resolveChannelID(ctx, request.GetString("channel_id", ""))
		if err != nil {
			return nil, err
		}
		items, err := api.ListBookmarksContext(ctx, channel)
		if err != nil {
			return nil, err
		}
		out := make([]bookmark, 0, len(items))
		for _, b := range items {
			out = append(out, summary(b))
		}
		return jsonResult(map[string]any{"channel": channel, "bookmarks": out})
	}

	channel, err := ch.writableChannel(ctx, request.GetString("channel_id", ""), "bookmarks")
	if err != nil {
		return nil, err
	}
	id := request.GetString("bookmark_id", "")
	args := request.GetArguments()
	switch action {
	case "add":
		title, link := request.GetString("title", ""), request.GetString("link", "")
		if title == "" || link == "" {
			return nil, errors.New("add needs title and link")
		}
		b, err := api.AddBookmarkContext(ctx, channel, slack.AddBookmarkParameters{Title: title, Type: "link", Link: link, Emoji: request.GetString("emoji", "")})
		if err != nil {
			return nil, err
		}
		return jsonResult(summary(b))
	case "edit":
		if id == "" {
			return nil, errors.New("edit needs bookmark_id")
		}
		params := slack.EditBookmarkParameters{Link: request.GetString("link", "")}
		if _, ok := args["title"]; ok {
			t := request.GetString("title", "")
			params.Title = &t
		}
		if _, ok := args["emoji"]; ok {
			e := request.GetString("emoji", "")
			params.Emoji = &e
		}
		b, err := api.EditBookmarkContext(ctx, channel, id, params)
		if err != nil {
			return nil, err
		}
		return jsonResult(summary(b))
	case "remove":
		if id == "" {
			return nil, errors.New("remove needs bookmark_id")
		}
		if err := api.RemoveBookmarkContext(ctx, channel, id); err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(fmt.Sprintf("Removed bookmark %s from channel %s", id, channel)), nil
	}
	return nil, errors.New("action must be list, add, edit or remove")
}

// PresenceHandler reads someone's presence, or sets the account user's own.
func (ch *ConversationsHandler) PresenceHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("PresenceHandler called", zap.Any("params", request.Params))
	api := ch.ap(ctx).Slack()
	switch request.GetString("action", "get") {
	case "get":
		var user string
		if raw := request.GetString("user", ""); raw != "" {
			id, err := ch.resolveUserID(ctx, raw)
			if err != nil {
				return nil, err
			}
			user = id
		} else {
			// users.getPresence answers user_not_found to an empty user.
			me, err := api.AuthTest()
			if err != nil {
				return nil, err
			}
			user = me.UserID
		}
		p, err := api.GetUserPresenceContext(ctx, user)
		if err != nil {
			return nil, err
		}
		return jsonResult(map[string]any{"user": user, "presence": p.Presence, "online": p.Online, "manual_away": p.ManualAway})
	case "set":
		if err := requireWrites("SLACK_MCP_ADD_MESSAGE_TOOL", "presence"); err != nil {
			return nil, err
		}
		presence := request.GetString("presence", "")
		if presence != "auto" && presence != "away" {
			return nil, errors.New("presence must be auto (active when online) or away")
		}
		if err := api.SetUserPresenceContext(ctx, presence); err != nil {
			return nil, err
		}
		return mcp.NewToolResultText("Presence set to " + presence), nil
	}
	return nil, errors.New("action must be get or set")
}

// DNDHandler reads Do Not Disturb, or pauses / resumes the account user's notifications.
func (ch *ConversationsHandler) DNDHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("DNDHandler called", zap.Any("params", request.Params))
	api := ch.ap(ctx).Slack()
	var (
		status *slack.DNDStatus
		err    error
	)
	switch request.GetString("action", "info") {
	case "info":
		var user *string
		if raw := request.GetString("user", ""); raw != "" {
			id, uerr := ch.resolveUserID(ctx, raw)
			if uerr != nil {
				return nil, uerr
			}
			user = &id
		}
		status, err = api.GetDNDInfoContext(ctx, user)
	case "snooze":
		if werr := requireWrites("SLACK_MCP_ADD_MESSAGE_TOOL", "dnd"); werr != nil {
			return nil, werr
		}
		minutes := request.GetInt("minutes", 0)
		if minutes <= 0 {
			return nil, errors.New("snooze needs minutes > 0")
		}
		status, err = api.SetSnoozeContext(ctx, minutes)
	case "end":
		if werr := requireWrites("SLACK_MCP_ADD_MESSAGE_TOOL", "dnd"); werr != nil {
			return nil, werr
		}
		status, err = api.EndSnoozeContext(ctx)
	default:
		return nil, errors.New("action must be info, snooze or end")
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{"dnd_enabled": status.Enabled, "snoozed": status.SnoozeEnabled}
	if status.SnoozeEndTime != 0 {
		out["snooze_until"] = time.Unix(int64(status.SnoozeEndTime), 0).UTC().Format(time.RFC3339)
	}
	if status.NextStartTimestamp != 0 {
		out["next_dnd_start"] = time.Unix(int64(status.NextStartTimestamp), 0).UTC().Format(time.RFC3339)
		out["next_dnd_end"] = time.Unix(int64(status.NextEndTimestamp), 0).UTC().Format(time.RFC3339)
	}
	return jsonResult(out)
}

// RemindersAddHandler sets a reminder for the account user.
func (ch *ConversationsHandler) RemindersAddHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("RemindersAddHandler called", zap.Any("params", request.Params))
	text, when := request.GetString("text", ""), request.GetString("time", "")
	if text == "" || when == "" {
		return nil, errors.New("reminders_add needs text and time")
	}
	// reminders.add treats an empty user as an unknown user, so name ourselves.
	me, err := ch.ap(ctx).Slack().AuthTest()
	if err != nil {
		return nil, err
	}
	r, err := ch.ap(ctx).Slack().AddUserReminderContext(ctx, me.UserID, text, when)
	if err != nil {
		return nil, err
	}
	return jsonResult(map[string]any{"text": r.Text, "time": time.Unix(int64(r.Time), 0).UTC().Format(time.RFC3339)})
}

// StatusSetHandler sets or clears the account user's status.
func (ch *ConversationsHandler) StatusSetHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("StatusSetHandler called", zap.Any("params", request.Params))
	if err := requireWrites("SLACK_MCP_ADD_MESSAGE_TOOL", "users_set_status"); err != nil {
		return nil, err
	}
	text := request.GetString("status_text", "")
	emoji := request.GetString("status_emoji", "")
	if emoji != "" && !strings.HasPrefix(emoji, ":") {
		emoji = ":" + strings.Trim(emoji, ":") + ":"
	}
	var expiration int64
	if minutes := request.GetInt("expires_in_minutes", 0); minutes > 0 {
		expiration = time.Now().Add(time.Duration(minutes) * time.Minute).Unix()
	}
	if err := ch.ap(ctx).Slack().SetUserCustomStatusContext(ctx, text, emoji, expiration); err != nil {
		return nil, err
	}
	if text == "" && emoji == "" {
		return mcp.NewToolResultText("Cleared status"), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Status set to %s %s", emoji, text)), nil
}
