package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/nitaiaharoni1/slack-mcp-server/pkg/provider"
	"github.com/nitaiaharoni1/slack-mcp-server/pkg/text"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// The drafts.* payloads follow what Slack's own clients send. Nothing here ever
// sends a message: a draft goes out only when its owner presses send in Slack.

type draftDestination struct {
	ChannelID string `json:"channel_id"`
	ThreadTs  string `json:"thread_ts,omitempty"`
	Broadcast bool   `json:"broadcast,omitempty"`
}

type slackDraft struct {
	ID            string             `json:"id"`
	Destinations  []draftDestination `json:"destinations"`
	Blocks        slack.Blocks       `json:"blocks"`
	FileIDs       []string           `json:"file_ids"`
	LastUpdatedTs string             `json:"last_updated_ts"`
	DateCreated   float64            `json:"date_created"`
	DateScheduled float64            `json:"date_scheduled"`
}

// draftView is a draft as the tools return it.
type draftView struct {
	ID            string             `json:"draft_id"`
	Destinations  []draftDestination `json:"destinations"`
	Text          string             `json:"text"`
	FileIDs       []string           `json:"file_ids,omitempty"`
	LastUpdatedTs string             `json:"last_updated_ts"`
	Created       string             `json:"created,omitempty"`
	Scheduled     string             `json:"scheduled,omitempty"`
}

func unixToRFC3339(sec float64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(int64(sec), 0).UTC().Format(time.RFC3339)
}

func (d slackDraft) view() draftView {
	return draftView{
		ID:            d.ID,
		Destinations:  d.Destinations,
		Text:          text.BlocksToText(d.Blocks),
		FileIDs:       d.FileIDs,
		LastUpdatedTs: d.LastUpdatedTs,
		Created:       unixToRFC3339(d.DateCreated),
		Scheduled:     unixToRFC3339(d.DateScheduled),
	}
}

// padDraftTs pads a draft timestamp's fraction to the 7 digits Slack requires
// in client_last_updated_ts.
func padDraftTs(ts string) string {
	whole, frac, found := strings.Cut(ts, ".")
	if !found {
		return ts
	}
	return whole + "." + frac + strings.Repeat("0", max(0, 7-len(frac)))
}

// draftBlocks encodes the body as a rich text block: the only form Slack's
// composer edits. Markdown stays literal, mentions and links work.
func draftBlocks(body string) (string, error) {
	prepared := text.BidiLines(body)
	if n := utf8.RuneCountInString(prepared); n > text.MaxMarkdownChars {
		return "", fmt.Errorf("the draft is %d characters with its direction marks and Slack allows %d", n, text.MaxMarkdownChars)
	}
	encoded, err := json.Marshal([]slack.Block{plainTextBlock(prepared)})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// draftForm is the body shared by drafts.create and drafts.update.
func draftForm(body string, destinations []draftDestination, fileIDs []string) (url.Values, error) {
	if strings.TrimSpace(body) == "" {
		return nil, errors.New("text is required")
	}
	blocks, err := draftBlocks(body)
	if err != nil {
		return nil, err
	}
	dest, err := json.Marshal(destinations)
	if err != nil {
		return nil, err
	}
	if fileIDs == nil {
		fileIDs = []string{}
	}
	files, err := json.Marshal(fileIDs)
	if err != nil {
		return nil, err
	}
	return url.Values{
		"blocks":        {blocks},
		"destinations":  {string(dest)},
		"file_ids":      {string(files)},
		"client_msg_id": {uuid.NewString()},
	}, nil
}

// draftCallHint adds what to do when Slack has ended the browser session.
func draftCallHint(err error) error {
	if err == nil {
		return nil
	}
	for _, code := range []string{"invalid_auth", "token_expired", "token_revoked", "account_inactive"} {
		if strings.Contains(err.Error(), code) {
			return fmt.Errorf("%w: the Slack session behind the xoxc and xoxd tokens has ended; set fresh ones and restart the server", err)
		}
	}
	return err
}

type draftResponse struct {
	Draft *slackDraft `json:"draft"`
}

func createDraft(ctx context.Context, api provider.DraftCaller, body string, dest draftDestination, fileIDs []string) (*slackDraft, error) {
	form, err := draftForm(body, []draftDestination{dest}, fileIDs)
	if err != nil {
		return nil, err
	}
	form.Set("is_from_composer", "true")
	var out draftResponse
	if err := api.DraftCall(ctx, "drafts.create", form, &out); err != nil {
		return nil, draftCallHint(err)
	}
	if out.Draft == nil {
		return nil, errors.New("drafts.create: Slack returned no draft")
	}
	return out.Draft, nil
}

func listDrafts(ctx context.Context, api provider.DraftCaller, limit int, activeOnly bool) ([]slackDraft, error) {
	form := url.Values{}
	if limit > 0 {
		form.Set("limit", strconv.Itoa(limit))
	}
	if activeOnly {
		form.Set("is_active", "true")
	}
	var out struct {
		Drafts []slackDraft `json:"drafts"`
	}
	if err := api.DraftCall(ctx, "drafts.list", form, &out); err != nil {
		return nil, draftCallHint(err)
	}
	return out.Drafts, nil
}

func findDraft(ctx context.Context, api provider.DraftCaller, id string) (*slackDraft, error) {
	drafts, err := listDrafts(ctx, api, 0, false)
	if err != nil {
		return nil, err
	}
	for i := range drafts {
		if drafts[i].ID == id {
			return &drafts[i], nil
		}
	}
	return nil, fmt.Errorf("draft %s not found: drafts_list shows the account's drafts", id)
}

// findCurrentDraft refuses a draft that changed since the caller read it, such
// as an edit its owner made in Slack. Slack does not check this itself: it
// accepts a stale client_last_updated_ts and overwrites the draft.
func findCurrentDraft(ctx context.Context, api provider.DraftCaller, id, lastUpdatedTs string) (*slackDraft, error) {
	draft, err := findDraft(ctx, api, id)
	if err != nil {
		return nil, err
	}
	if padDraftTs(draft.LastUpdatedTs) != padDraftTs(lastUpdatedTs) {
		return nil, fmt.Errorf("draft %s changed since last_updated_ts %s, now %s: read it again with drafts_list before changing it", id, lastUpdatedTs, draft.LastUpdatedTs)
	}
	return draft, nil
}

func updateDraft(ctx context.Context, api provider.DraftCaller, id, lastUpdatedTs, body string, destinations []draftDestination, fileIDs []string) (*slackDraft, error) {
	form, err := draftForm(body, destinations, fileIDs)
	if err != nil {
		return nil, err
	}
	form.Set("draft_id", id)
	form.Set("client_last_updated_ts", padDraftTs(lastUpdatedTs))
	var out draftResponse
	if err := api.DraftCall(ctx, "drafts.update", form, &out); err != nil {
		return nil, draftCallHint(err)
	}
	if out.Draft == nil {
		return nil, errors.New("drafts.update: Slack returned no draft")
	}
	return out.Draft, nil
}

func deleteDraft(ctx context.Context, api provider.DraftCaller, id, lastUpdatedTs string) error {
	return draftCallHint(api.DraftCall(ctx, "drafts.delete", url.Values{
		"draft_id":               {id},
		"client_last_updated_ts": {padDraftTs(lastUpdatedTs)},
	}, nil))
}

// parseFileIDs reads a comma-separated file_ids argument.
func parseFileIDs(raw string) []string {
	ids := []string{}
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// DraftsCreateHandler saves a draft without sending it.
func (ch *ConversationsHandler) DraftsCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("DraftsCreateHandler called", zap.Any("params", request.Params))

	threadTs := request.GetString("thread_ts", "")
	if threadTs != "" {
		if err := requireTS("thread_ts", threadTs); err != nil {
			return nil, err
		}
	}
	api, err := ch.ap(ctx).Drafts()
	if err != nil {
		return nil, err
	}
	channel, err := ch.writableChannel(ctx, request.GetString("channel_id", ""), "drafts_create")
	if err != nil {
		return nil, err
	}
	draft, err := createDraft(ctx, api, request.GetString("text", ""),
		draftDestination{ChannelID: channel, ThreadTs: threadTs}, parseFileIDs(request.GetString("file_ids", "")))
	if err != nil {
		ch.logger.Error("Slack drafts.create failed", zap.Error(err))
		return nil, err
	}
	return jsonResult(draft.view())
}

// DraftsListHandler lists the account's drafts.
func (ch *ConversationsHandler) DraftsListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("DraftsListHandler called", zap.Any("params", request.Params))

	api, err := ch.ap(ctx).Drafts()
	if err != nil {
		return nil, err
	}
	drafts, err := listDrafts(ctx, api, request.GetInt("limit", 50), !request.GetBool("include_inactive", false))
	if err != nil {
		ch.logger.Error("Slack drafts.list failed", zap.Error(err))
		return nil, err
	}
	views := make([]draftView, 0, len(drafts))
	for _, d := range drafts {
		views = append(views, d.view())
	}
	return jsonResult(map[string]any{"drafts": views})
}

// DraftsUpdateHandler replaces a draft's text. Without new values, the draft
// keeps its destination and files.
func (ch *ConversationsHandler) DraftsUpdateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("DraftsUpdateHandler called", zap.Any("params", request.Params))

	id := request.GetString("draft_id", "")
	lastUpdatedTs := request.GetString("last_updated_ts", "")
	if id == "" || lastUpdatedTs == "" {
		return nil, errors.New("draft_id and last_updated_ts are required")
	}
	threadTs := request.GetString("thread_ts", "")
	if threadTs != "" {
		if err := requireTS("thread_ts", threadTs); err != nil {
			return nil, err
		}
	}
	api, err := ch.ap(ctx).Drafts()
	if err != nil {
		return nil, err
	}
	current, err := findCurrentDraft(ctx, api, id, lastUpdatedTs)
	if err != nil {
		return nil, err
	}

	destinations := current.Destinations
	channel := request.GetString("channel_id", "")
	if channel == "" && threadTs != "" {
		if len(destinations) != 1 {
			return nil, errors.New("thread_ts needs channel_id: this draft does not have exactly one destination")
		}
		channel = destinations[0].ChannelID
	}
	if channel != "" {
		resolved, err := ch.writableChannel(ctx, channel, "drafts_update")
		if err != nil {
			return nil, err
		}
		destinations = []draftDestination{{ChannelID: resolved, ThreadTs: threadTs}}
	}
	fileIDs := current.FileIDs
	if _, given := request.GetArguments()["file_ids"]; given {
		fileIDs = parseFileIDs(request.GetString("file_ids", ""))
	}

	draft, err := updateDraft(ctx, api, id, lastUpdatedTs, request.GetString("text", ""), destinations, fileIDs)
	if err != nil {
		ch.logger.Error("Slack drafts.update failed", zap.Error(err))
		return nil, err
	}
	return jsonResult(draft.view())
}

// DraftsDeleteHandler deletes a draft.
func (ch *ConversationsHandler) DraftsDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ch.logger.Debug("DraftsDeleteHandler called", zap.Any("params", request.Params))

	id := request.GetString("draft_id", "")
	lastUpdatedTs := request.GetString("last_updated_ts", "")
	if id == "" || lastUpdatedTs == "" {
		return nil, errors.New("draft_id and last_updated_ts are required")
	}
	api, err := ch.ap(ctx).Drafts()
	if err != nil {
		return nil, err
	}
	if _, err := findCurrentDraft(ctx, api, id, lastUpdatedTs); err != nil {
		return nil, err
	}
	if err := deleteDraft(ctx, api, id, lastUpdatedTs); err != nil {
		ch.logger.Error("Slack drafts.delete failed", zap.Error(err))
		return nil, err
	}
	return mcp.NewToolResultText("Deleted draft " + id), nil
}
