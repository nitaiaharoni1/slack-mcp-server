package server

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nitaiaharoni1/slack-mcp-server/pkg/handler"
	"github.com/nitaiaharoni1/slack-mcp-server/pkg/provider"
)

// Tools this fork adds on top of upstream. They live here, apart from
// server.go, so upstream changes merge cleanly.
const (
	ToolListAccounts            = "list_accounts"
	ToolMessagesUpdate          = "messages_update"
	ToolMessagesDelete          = "messages_delete"
	ToolMessagesSchedule        = "messages_schedule"
	ToolMessagesScheduledList   = "messages_scheduled_list"
	ToolMessagesScheduledDelete = "messages_scheduled_delete"
	ToolMessagesPermalink       = "messages_permalink"
	ToolMessagesForward         = "messages_forward"
	ToolFilesUpload             = "files_upload"
	ToolUsersInfo               = "users_info"
	ToolCanvasesCreate          = "canvases_create"
	ToolCanvasesRead            = "canvases_read"
	ToolCanvasesEdit            = "canvases_edit"
	ToolCanvasesSectionsLookup  = "canvases_sections_lookup"
	ToolCanvasesDelete          = "canvases_delete"
	ToolListsCreate             = "lists_create"
	ToolListsItemsList          = "lists_items_list"
	ToolListsItemGet            = "lists_item_get"
	ToolListsItemsCreate        = "lists_items_create"
	ToolListsItemsUpdate        = "lists_items_update"
	ToolListsItemsDelete        = "lists_items_delete"
	ToolListsUpdate             = "lists_update"
	ToolConversationsCreate     = "conversations_create"
	ToolConversationsInfo       = "conversations_info"
	ToolConversationsMembers    = "conversations_members"
	ToolConversationsInvite     = "conversations_invite"
	ToolConversationsSetTopic   = "conversations_set_topic"
	ToolReactionsGet            = "reactions_get"
	ToolEmojiSearch             = "emoji_search"
	ToolPins                    = "pins"
	ToolRemindersAdd            = "reminders_add"
	ToolUsersSetStatus          = "users_set_status"
	ToolFilesList               = "files_list"
	ToolFilesInfo               = "files_info"
	ToolFilesDelete             = "files_delete"
	ToolConversationsManage     = "conversations_manage"
	ToolBookmarks               = "bookmarks"
	ToolPresence                = "presence"
	ToolDND                     = "dnd"
)

func init() {
	ValidToolNames = append(ValidToolNames,
		ToolListAccounts, ToolMessagesUpdate, ToolMessagesDelete, ToolMessagesSchedule,
		ToolMessagesScheduledList, ToolMessagesScheduledDelete, ToolMessagesPermalink, ToolMessagesForward,
		ToolFilesUpload, ToolFilesList, ToolFilesInfo, ToolFilesDelete, ToolUsersInfo,
		ToolCanvasesCreate, ToolCanvasesRead, ToolCanvasesEdit, ToolCanvasesSectionsLookup, ToolCanvasesDelete,
		ToolListsCreate, ToolListsItemsList, ToolListsItemGet, ToolListsItemsCreate, ToolListsItemsUpdate,
		ToolListsItemsDelete, ToolListsUpdate,
		ToolConversationsCreate, ToolConversationsInfo, ToolConversationsMembers, ToolConversationsInvite,
		ToolConversationsSetTopic, ToolConversationsManage, ToolReactionsGet, ToolEmojiSearch, ToolPins,
		ToolBookmarks, ToolRemindersAdd, ToolUsersSetStatus, ToolPresence, ToolDND,
	)
}

const channelArgDescription = "ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."

// messageBodyArgs are the content arguments shared with conversations_add_message.
func messageBodyArgs() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("text",
			mcp.Description("Message text in the content_type format. Hebrew or other RTL text: start every line with U+200F (RLM) and never open a line with a mention or an English word, or Slack renders it left-to-right."),
		),
		mcp.WithString("content_type",
			mcp.DefaultString("text/markdown"),
			mcp.Description("Content type of the message: 'text/markdown' (default) or 'text/plain'. Ignored when blocks is provided."),
		),
		mcp.WithString("blocks",
			mcp.Description("Raw Slack Block Kit JSON array. Takes precedence over text/content_type for rendering; text becomes the notification fallback."),
		),
	}
}

func registerExtendedTools(s *server.MCPServer, registry *provider.Registry, enabledTools []string, convs *handler.ConversationsHandler, canvases *handler.CanvasesHandler) {
	add := func(name, writeEnv string, tool mcp.Tool, h server.ToolHandlerFunc) {
		if shouldAddTool(name, enabledTools, writeEnv) {
			addTool(s, registry, tool, h)
		}
	}
	with := func(base []mcp.ToolOption, extra ...mcp.ToolOption) []mcp.ToolOption {
		return append(append([]mcp.ToolOption{}, base...), extra...)
	}

	add(ToolListAccounts, "", mcp.NewTool(ToolListAccounts,
		mcp.WithDescription("List the configured Slack accounts: name, workspace, user and token type. Pass the name as the 'account' argument of any other tool."),
		mcp.WithTitleAnnotation("List Accounts"),
		mcp.WithReadOnlyHintAnnotation(true),
	), listAccountsHandler(registry))

	// Messages
	add(ToolMessagesUpdate, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolMessagesUpdate, with(messageBodyArgs(),
		mcp.WithDescription("Edit a message the account posted (chat.update). Same channel policy as conversations_add_message."),
		mcp.WithTitleAnnotation("Edit Message"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("timestamp", mcp.Required(), mcp.Description("ts of the message to edit, in format 1234567890.123456.")),
	)...), convs.MessagesUpdateHandler)

	add(ToolMessagesDelete, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolMessagesDelete,
		mcp.WithDescription("Delete a message the account posted (chat.delete). Cannot be undone."),
		mcp.WithTitleAnnotation("Delete Message"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("timestamp", mcp.Required(), mcp.Description("ts of the message to delete, in format 1234567890.123456.")),
	), convs.MessagesDeleteHandler)

	add(ToolMessagesSchedule, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolMessagesSchedule, with(messageBodyArgs(),
		mcp.WithDescription("Schedule a message to be posted later as the account (chat.scheduleMessage), up to 120 days ahead."),
		mcp.WithTitleAnnotation("Schedule Message"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("post_at", mcp.Required(), mcp.Description("When to post: Unix seconds, or RFC 3339 with an offset, e.g. 2026-11-05T09:00:00+02:00.")),
		mcp.WithString("thread_ts", mcp.Description("Optional ts of a thread to reply in.")),
	)...), convs.MessagesScheduleHandler)

	add(ToolMessagesScheduledList, "", mcp.NewTool(ToolMessagesScheduledList,
		mcp.WithDescription("List the account's pending scheduled messages, optionally for one channel."),
		mcp.WithTitleAnnotation("List Scheduled Messages"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Description("Optional channel filter. "+channelArgDescription)),
		mcp.WithNumber("limit", mcp.DefaultNumber(100), mcp.Description("Maximum results per page.")),
		mcp.WithString("cursor", mcp.Description("next_cursor from the previous page.")),
	), convs.ScheduledListHandler)

	add(ToolMessagesScheduledDelete, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolMessagesScheduledDelete,
		mcp.WithDescription("Cancel a pending scheduled message."),
		mcp.WithTitleAnnotation("Cancel Scheduled Message"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("scheduled_message_id", mcp.Required(), mcp.Description("ID from messages_schedule or messages_scheduled_list.")),
	), convs.ScheduledDeleteHandler)

	add(ToolMessagesPermalink, "", mcp.NewTool(ToolMessagesPermalink,
		mcp.WithDescription("Get the permalink of a message."),
		mcp.WithTitleAnnotation("Get Message Link"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("timestamp", mcp.Required(), mcp.Description("ts of the message, in format 1234567890.123456.")),
	), convs.MessagesPermalinkHandler)

	add(ToolMessagesForward, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolMessagesForward,
		mcp.WithDescription("Forward a message to another channel, DM or thread in the same workspace, with an optional comment. Slack has no forward API: this posts the message's permalink, which Slack shows as the shared message. Both conversations must be in the account's workspace."),
		mcp.WithTitleAnnotation("Forward Message"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel the message is in. "+channelArgDescription)),
		mcp.WithString("timestamp", mcp.Required(), mcp.Description("ts of the message to forward, in format 1234567890.123456.")),
		mcp.WithString("target_channel_id", mcp.Required(), mcp.Description("Where to forward it. "+channelArgDescription)),
		mcp.WithString("target_thread_ts", mcp.Description("Optional ts of a thread in the target to forward into.")),
		mcp.WithString("comment", mcp.Description("Optional text posted above the forwarded message.")),
	), convs.MessagesForwardHandler)

	add(ToolFilesUpload, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolFilesUpload,
		mcp.WithDescription("Upload a file (up to 5 MB) as the account, optionally sharing it to a channel or thread. Give exactly one of file_path, content or content_base64."),
		mcp.WithTitleAnnotation("Upload File"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("file_path", mcp.Description("Local path of the file to upload.")),
		mcp.WithString("content", mcp.Description("Text content to upload as a file.")),
		mcp.WithString("content_base64", mcp.Description("Binary content, base64-encoded.")),
		mcp.WithString("filename", mcp.Description("File name; required with content or content_base64.")),
		mcp.WithString("title", mcp.Description("Optional title.")),
		mcp.WithString("channel_id", mcp.Description("Optional channel to share to. "+channelArgDescription)),
		mcp.WithString("thread_ts", mcp.Description("Optional thread ts to share into (needs channel_id).")),
		mcp.WithString("initial_comment", mcp.Description("Optional message posted with the file.")),
	), convs.FilesUploadHandler)

	add(ToolFilesList, "", mcp.NewTool(ToolFilesList,
		mcp.WithDescription("List files the account can see, newest first, optionally only one channel's, one person's, or one type. Canvases and Lists are files too."),
		mcp.WithTitleAnnotation("List Files"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Description("Optional: only files shared in this channel. "+channelArgDescription)),
		mcp.WithString("user", mcp.Description("Optional: only files this person uploaded (ID, @handle, name or email).")),
		mcp.WithString("types", mcp.Description("Optional comma-separated types: all, spaces, snippets, images, gdocs, zips, pdfs, canvas, list.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(20), mcp.Description("Files per page.")),
		mcp.WithNumber("page", mcp.DefaultNumber(1), mcp.Description("Page number, from 1.")),
	), convs.FilesListHandler)

	add(ToolFilesInfo, "", mcp.NewTool(ToolFilesInfo,
		mcp.WithDescription("Get a file's details: name, type, size, uploader, permalink, where it is shared. Use attachment_get_data to read its content."),
		mcp.WithTitleAnnotation("Get File Info"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("file_id", mcp.Required(), mcp.Description("File ID (Fxxxxxxxxxx).")),
	), convs.FilesInfoHandler)

	add(ToolFilesDelete, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolFilesDelete,
		mcp.WithDescription("Delete a file the account uploaded or owns, including a canvas or a List. Removes it everywhere it was shared. Cannot be undone."),
		mcp.WithTitleAnnotation("Delete File"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("file_id", mcp.Required(), mcp.Description("File ID (Fxxxxxxxxxx).")),
	), convs.FilesDeleteHandler)

	add(ToolUsersInfo, "", mcp.NewTool(ToolUsersInfo,
		mcp.WithDescription("Get one user's full profile: title, email, phone, status, time zone and custom profile fields."),
		mcp.WithTitleAnnotation("Get User Profile"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("user_id", mcp.Required(), mcp.Description("User ID (Uxxxxxxxxxx) or a name/email to search for.")),
	), convs.UsersInfoHandler)

	// Canvases
	add(ToolCanvasesCreate, "SLACK_MCP_CANVAS_TOOL", mcp.NewTool(ToolCanvasesCreate,
		mcp.WithDescription("Create a canvas from markdown. With channel_id it becomes that channel's canvas (one per channel); without, a standalone canvas owned by the account."),
		mcp.WithTitleAnnotation("Create Canvas"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("title", mcp.Description("Canvas title; required for a standalone canvas.")),
		mcp.WithString("markdown", mcp.Description("Canvas content as markdown.")),
		mcp.WithString("channel_id", mcp.Description("Optional channel whose canvas to create. "+channelArgDescription)),
	), canvases.CanvasesCreateHandler)

	add(ToolCanvasesRead, "", mcp.NewTool(ToolCanvasesRead,
		mcp.WithDescription("Read a canvas: title, link, last update and its content as HTML."),
		mcp.WithTitleAnnotation("Read Canvas"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("canvas_id", mcp.Required(), mcp.Description("Canvas ID (Fxxxxxxxxxx).")),
	), canvases.CanvasesReadHandler)

	add(ToolCanvasesEdit, "SLACK_MCP_CANVAS_TOOL", mcp.NewTool(ToolCanvasesEdit,
		mcp.WithDescription("Edit a canvas with markdown: append, prepend, insert around or replace a section, or delete one. Find section IDs with canvases_sections_lookup."),
		mcp.WithTitleAnnotation("Edit Canvas"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("canvas_id", mcp.Required(), mcp.Description("Canvas ID (Fxxxxxxxxxx).")),
		mcp.WithString("operation", mcp.DefaultString("insert_at_end"), mcp.Description("insert_at_end, insert_at_start, insert_after, insert_before, replace, or delete.")),
		mcp.WithString("section_id", mcp.Description("Target section; required for insert_after, insert_before and delete. replace without it replaces the whole canvas.")),
		mcp.WithString("markdown", mcp.Description("Markdown content; required except for delete.")),
	), canvases.CanvasesEditHandler)

	add(ToolCanvasesSectionsLookup, "", mcp.NewTool(ToolCanvasesSectionsLookup,
		mcp.WithDescription("Find canvas section IDs by text and/or section type, for targeted canvases_edit calls."),
		mcp.WithTitleAnnotation("Find Canvas Sections"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("canvas_id", mcp.Required(), mcp.Description("Canvas ID (Fxxxxxxxxxx).")),
		mcp.WithString("contains_text", mcp.Description("Text the section contains.")),
		mcp.WithString("section_types", mcp.Description("Comma-separated: h1, h2, h3, any_header.")),
	), canvases.CanvasesSectionsLookupHandler)

	add(ToolCanvasesDelete, "SLACK_MCP_CANVAS_TOOL", mcp.NewTool(ToolCanvasesDelete,
		mcp.WithDescription("Delete a canvas. Cannot be undone."),
		mcp.WithTitleAnnotation("Delete Canvas"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("canvas_id", mcp.Required(), mcp.Description("Canvas ID (Fxxxxxxxxxx).")),
	), canvases.CanvasesDeleteHandler)

	// Lists
	add(ToolListsCreate, "SLACK_MCP_LISTS_TOOL", mcp.NewTool(ToolListsCreate,
		mcp.WithDescription("Create a Slack List (slackLists.create). Paid plans only."),
		mcp.WithTitleAnnotation("Create List"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("name", mcp.Required(), mcp.Description("List name.")),
		mcp.WithString("schema", mcp.Description("Optional JSON array of column definitions, as slackLists.create takes them.")),
		mcp.WithBoolean("todo_mode", mcp.Description("Add the built-in completed, assignee and due date columns.")),
	), canvases.ListsCreateHandler)

	add(ToolListsItemsList, "", mcp.NewTool(ToolListsItemsList,
		mcp.WithDescription("List the items of a Slack List with their field values (slackLists.items.list)."),
		mcp.WithTitleAnnotation("List Items"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("list_id", mcp.Required(), mcp.Description("List ID (Fxxxxxxxxxx).")),
		mcp.WithNumber("limit", mcp.DefaultNumber(100), mcp.Description("Maximum items per page.")),
		mcp.WithString("cursor", mcp.Description("Cursor from the previous page.")),
		mcp.WithBoolean("archived", mcp.Description("Return archived items instead.")),
	), canvases.ListsItemsListHandler)

	add(ToolListsItemGet, "", mcp.NewTool(ToolListsItemGet,
		mcp.WithDescription("Get one Slack List item (slackLists.items.info)."),
		mcp.WithTitleAnnotation("Get List Item"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("list_id", mcp.Required(), mcp.Description("List ID (Fxxxxxxxxxx).")),
		mcp.WithString("item_id", mcp.Required(), mcp.Description("Item (row) ID.")),
	), canvases.ListsItemGetHandler)

	add(ToolListsItemsCreate, "SLACK_MCP_LISTS_TOOL", mcp.NewTool(ToolListsItemsCreate,
		mcp.WithDescription("Add an item to a Slack List (slackLists.items.create). Read column IDs and value shapes from lists_items_list first."),
		mcp.WithTitleAnnotation("Add List Item"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("list_id", mcp.Required(), mcp.Description("List ID (Fxxxxxxxxxx).")),
		mcp.WithString("initial_fields", mcp.Description(`JSON array of field values, e.g. [{"column_id":"Col1","rich_text":[...]}].`)),
		mcp.WithString("parent_item_id", mcp.Description("Optional parent item, to create a subitem.")),
	), canvases.ListsItemsCreateHandler)

	add(ToolListsItemsUpdate, "SLACK_MCP_LISTS_TOOL", mcp.NewTool(ToolListsItemsUpdate,
		mcp.WithDescription("Change cells of Slack List items (slackLists.items.update)."),
		mcp.WithTitleAnnotation("Update List Items"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("list_id", mcp.Required(), mcp.Description("List ID (Fxxxxxxxxxx).")),
		mcp.WithString("cells", mcp.Required(), mcp.Description(`JSON array of cells, e.g. [{"row_id":"Rec1","column_id":"Col1","checkbox":true}].`)),
	), canvases.ListsItemsUpdateHandler)

	add(ToolListsItemsDelete, "SLACK_MCP_LISTS_TOOL", mcp.NewTool(ToolListsItemsDelete,
		mcp.WithDescription("Delete Slack List items. Cannot be undone."),
		mcp.WithTitleAnnotation("Delete List Items"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("list_id", mcp.Required(), mcp.Description("List ID (Fxxxxxxxxxx).")),
		mcp.WithString("item_ids", mcp.Required(), mcp.Description("Comma-separated item IDs.")),
	), canvases.ListsItemsDeleteHandler)

	add(ToolListsUpdate, "SLACK_MCP_LISTS_TOOL", mcp.NewTool(ToolListsUpdate,
		mcp.WithDescription("Rename a Slack List or turn its todo mode on or off (slackLists.update)."),
		mcp.WithTitleAnnotation("Update List"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("list_id", mcp.Required(), mcp.Description("List ID (Fxxxxxxxxxx).")),
		mcp.WithString("name", mcp.Description("New name.")),
		mcp.WithBoolean("todo_mode", mcp.Description("Turn the completed / assignee / due date columns on or off.")),
	), canvases.ListsUpdateHandler)

	// Channels and people
	add(ToolConversationsCreate, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolConversationsCreate,
		mcp.WithDescription("Create a channel (name), or open a DM or group DM with users and get its channel ID (users)."),
		mcp.WithTitleAnnotation("Create Channel or DM"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("name", mcp.Description("New channel name: lowercase, no spaces, up to 80 characters.")),
		mcp.WithBoolean("is_private", mcp.Description("Create the channel as private.")),
		mcp.WithString("users", mcp.Description("Comma-separated user IDs, @handles, names or emails, for a DM (one) or group DM (up to 8).")),
	), convs.ConversationsCreateHandler)

	add(ToolConversationsInfo, "", mcp.NewTool(ToolConversationsInfo,
		mcp.WithDescription("Get a channel's details: name, privacy, topic, purpose, member count, archived."),
		mcp.WithTitleAnnotation("Get Channel Info"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
	), convs.ConversationsInfoHandler)

	add(ToolConversationsMembers, "", mcp.NewTool(ToolConversationsMembers,
		mcp.WithDescription("List a channel's members with their names."),
		mcp.WithTitleAnnotation("List Channel Members"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithNumber("limit", mcp.DefaultNumber(200), mcp.Description("Maximum members per page.")),
		mcp.WithString("cursor", mcp.Description("next_cursor from the previous page.")),
	), convs.ConversationsMembersHandler)

	add(ToolConversationsInvite, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolConversationsInvite,
		mcp.WithDescription("Add users to a channel."),
		mcp.WithTitleAnnotation("Add People to Channel"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("users", mcp.Required(), mcp.Description("Comma-separated user IDs, @handles, names or emails.")),
	), convs.ConversationsInviteHandler)

	add(ToolConversationsSetTopic, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolConversationsSetTopic,
		mcp.WithDescription("Set a channel's topic and/or purpose (description). An empty string clears it."),
		mcp.WithTitleAnnotation("Set Channel Topic"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("topic", mcp.Description("New topic.")),
		mcp.WithString("purpose", mcp.Description("New purpose (the channel description).")),
	), convs.ConversationsSetTopicHandler)

	add(ToolConversationsManage, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolConversationsManage,
		mcp.WithDescription("Archive, unarchive or rename a channel, or remove someone from it. Follows the SLACK_MCP_ADD_MESSAGE_TOOL channel policy."),
		mcp.WithTitleAnnotation("Manage Channel"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("action", mcp.Required(), mcp.Description("archive, unarchive, rename or kick.")),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("name", mcp.Description("New name, for rename: lowercase, no spaces, up to 80 characters.")),
		mcp.WithString("user", mcp.Description("Who to remove, for kick (ID, @handle, name or email).")),
	), convs.ConversationsManageHandler)

	add(ToolBookmarks, "", mcp.NewTool(ToolBookmarks,
		mcp.WithDescription("List a channel's bookmarks (the links under its name), or add, edit or remove one. Changes follow the SLACK_MCP_ADD_MESSAGE_TOOL channel policy."),
		mcp.WithTitleAnnotation("Channel Bookmarks"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("action", mcp.DefaultString("list"), mcp.Description("list, add, edit or remove.")),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("title", mcp.Description("Bookmark title; required for add.")),
		mcp.WithString("link", mcp.Description("URL; required for add.")),
		mcp.WithString("emoji", mcp.Description("Optional emoji, e.g. :link:.")),
		mcp.WithString("bookmark_id", mcp.Description("Required for edit and remove.")),
	), convs.BookmarksHandler)

	add(ToolPresence, "", mcp.NewTool(ToolPresence,
		mcp.WithDescription("Get someone's presence (active / away), or set the account user's own: auto means active while connected, away forces away."),
		mcp.WithTitleAnnotation("Presence"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("action", mcp.DefaultString("get"), mcp.Description("get or set.")),
		mcp.WithString("user", mcp.Description("For get: whose presence (ID, @handle, name or email); empty means the account user.")),
		mcp.WithString("presence", mcp.Description("For set: auto or away.")),
	), convs.PresenceHandler)

	add(ToolDND, "", mcp.NewTool(ToolDND,
		mcp.WithDescription("Do Not Disturb: read someone's DND state, or pause the account user's notifications for some minutes (snooze) and resume them (end)."),
		mcp.WithTitleAnnotation("Do Not Disturb"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("action", mcp.DefaultString("info"), mcp.Description("info, snooze or end.")),
		mcp.WithString("user", mcp.Description("For info: whose DND (ID, @handle, name or email); empty means the account user.")),
		mcp.WithNumber("minutes", mcp.Description("For snooze: how long to pause notifications.")),
	), convs.DNDHandler)

	add(ToolReactionsGet, "", mcp.NewTool(ToolReactionsGet,
		mcp.WithDescription("List the reactions on a message and who added each one."),
		mcp.WithTitleAnnotation("Get Reactions"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("timestamp", mcp.Required(), mcp.Description("ts of the message, in format 1234567890.123456.")),
	), convs.ReactionsGetHandler)

	add(ToolEmojiSearch, "", mcp.NewTool(ToolEmojiSearch,
		mcp.WithDescription("Search the workspace's custom emoji by name (standard Unicode emoji are always available and not listed)."),
		mcp.WithTitleAnnotation("Search Emoji"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("query", mcp.Description("Part of the emoji name; empty lists all.")),
		mcp.WithNumber("limit", mcp.DefaultNumber(100), mcp.Description("Maximum names returned.")),
	), convs.EmojiSearchHandler)

	add(ToolPins, "", mcp.NewTool(ToolPins,
		mcp.WithDescription("List a channel's pinned messages, or pin / unpin a message. Pinning follows the SLACK_MCP_ADD_MESSAGE_TOOL channel policy."),
		mcp.WithTitleAnnotation("Pins"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("action", mcp.DefaultString("list"), mcp.Description("list, add or remove.")),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description(channelArgDescription)),
		mcp.WithString("timestamp", mcp.Description("ts of the message; required for add and remove.")),
	), convs.PinsHandler)

	add(ToolRemindersAdd, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolRemindersAdd,
		mcp.WithDescription("Set a Slack reminder for the account user; Slackbot delivers it. Slack's API cannot list or delete reminders any more (the id add returns is unknown to reminders.info, list and delete), so the user manages them in Slack's Later view."),
		mcp.WithTitleAnnotation("Add Reminder"),
		mcp.WithString("text", mcp.Required(), mcp.Description("Reminder text.")),
		mcp.WithString("time", mcp.Required(), mcp.Description(`When: Unix seconds, or natural language Slack understands, e.g. "in 15 minutes" or "every Thursday at 9am".`)),
	), convs.RemindersAddHandler)

	add(ToolUsersSetStatus, "SLACK_MCP_ADD_MESSAGE_TOOL", mcp.NewTool(ToolUsersSetStatus,
		mcp.WithDescription("Set or clear the account user's Slack status. Empty text and emoji clear it."),
		mcp.WithTitleAnnotation("Set My Status"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("status_text", mcp.Description("Status text, up to 100 characters.")),
		mcp.WithString("status_emoji", mcp.Description("Emoji name, e.g. palm_tree or :palm_tree:.")),
		mcp.WithNumber("expires_in_minutes", mcp.Description("Clear the status after this many minutes; 0 keeps it.")),
	), convs.StatusSetHandler)
}

func listAccountsHandler(registry *provider.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		type account struct {
			Name      string `json:"name"`
			Default   bool   `json:"default"`
			Team      string `json:"team"`
			TeamID    string `json:"team_id"`
			User      string `json:"user"`
			UserID    string `json:"user_id"`
			URL       string `json:"url"`
			TokenType string `json:"token_type"`
		}
		var out []account
		for _, ap := range registry.All() {
			a := account{Name: ap.Name(), Default: ap.Name() == registry.DefaultName()}
			switch {
			case ap.IsDemo():
				a.TokenType = "demo"
			case ap.IsBotToken():
				a.TokenType = "bot (xoxb)"
			case ap.IsOAuth():
				a.TokenType = "user (xoxp)"
			default:
				a.TokenType = "session (xoxc/xoxd)"
			}
			if ar, err := ap.Slack().AuthTest(); err == nil {
				a.Team, a.TeamID, a.User, a.UserID, a.URL = ar.Team, ar.TeamID, ar.User, ar.UserID, ar.URL
			}
			out = append(out, a)
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(b)), nil
	}
}
