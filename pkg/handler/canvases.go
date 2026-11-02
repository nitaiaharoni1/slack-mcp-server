package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

// maxCanvasBytes caps the canvas HTML canvases_read returns.
const maxCanvasBytes = 5 * 1024 * 1024

// CanvasesHandler serves canvases (canvases.*) and Slack Lists (slackLists.*).
type CanvasesHandler struct {
	registry *provider.Registry
	convs    *ConversationsHandler
	logger   *zap.Logger
}

// ap returns the Slack account resolved for this tool call.
func (h *CanvasesHandler) ap(ctx context.Context) *provider.ApiProvider {
	return h.registry.FromContext(ctx)
}

func NewCanvasesHandler(registry *provider.Registry, convs *ConversationsHandler, logger *zap.Logger) *CanvasesHandler {
	return &CanvasesHandler{registry: registry, convs: convs, logger: logger}
}

// requireWrites applies the opt-in env switch of a write tool family.
func requireWrites(envVar, tool string) error {
	if os.Getenv(envVar) == "" && !strings.Contains(os.Getenv("SLACK_MCP_ENABLED_TOOLS"), tool) {
		return fmt.Errorf("%s is disabled: set %s=true to allow it", tool, envVar)
	}
	return nil
}

// jsonArg reads an argument that holds JSON, passed either as a JSON string or
// as a structured value the client already parsed.
func jsonArg(request mcp.CallToolRequest, name string) (json.RawMessage, error) {
	raw, ok := request.GetArguments()[name]
	if !ok || raw == nil {
		return nil, nil
	}
	if s, ok := raw.(string); ok {
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		if !json.Valid([]byte(s)) {
			return nil, fmt.Errorf("%s must be valid JSON", name)
		}
		return json.RawMessage(s), nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be valid JSON: %w", name, err)
	}
	return b, nil
}

func (h *CanvasesHandler) canvasInfo(ctx context.Context, canvasID string) (*slack.File, error) {
	file, _, _, err := h.ap(ctx).Slack().GetFileInfoContext(ctx, canvasID, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("files.info %s: %w", canvasID, err)
	}
	return file, nil
}

// CanvasesCreateHandler creates a standalone canvas, or a channel's canvas when channel_id is set.
func (h *CanvasesHandler) CanvasesCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesCreateHandler called", zap.Any("params", request.Params))
	if err := requireWrites("SLACK_MCP_CANVAS_TOOL", "canvases_create"); err != nil {
		return nil, err
	}

	title := request.GetString("title", "")
	content := slack.DocumentContent{Type: "markdown", Markdown: request.GetString("markdown", "")}

	var canvasID string
	if raw := request.GetString("channel_id", ""); raw != "" {
		channel, err := h.convs.resolveChannelID(ctx, raw)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"channel_id": channel}
		if title != "" {
			body["title"] = title
		}
		if content.Markdown != "" {
			body["document_content"] = content
		}
		var resp struct {
			CanvasID string `json:"canvas_id"`
		}
		if err := h.ap(ctx).Slack().APICall(ctx, "conversations.canvases.create", body, &resp); err != nil {
			return nil, err
		}
		canvasID = resp.CanvasID
	} else {
		if title == "" {
			return nil, errors.New("title is required for a standalone canvas")
		}
		if content.Markdown == "" {
			content = slack.DocumentContent{}
		}
		id, err := h.ap(ctx).Slack().CreateCanvasContext(ctx, title, content)
		if err != nil {
			return nil, err
		}
		canvasID = id
	}

	out := map[string]any{"canvas_id": canvasID}
	if file, err := h.canvasInfo(ctx, canvasID); err == nil {
		out["permalink"] = file.Permalink
		out["title"] = file.Title
	}
	return jsonResult(out)
}

// CanvasesReadHandler returns a canvas's title, link and content (HTML).
func (h *CanvasesHandler) CanvasesReadHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesReadHandler called", zap.Any("params", request.Params))

	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id is required")
	}
	file, err := h.canvasInfo(ctx, canvasID)
	if err != nil {
		return nil, err
	}
	if file.Size > maxCanvasBytes {
		return nil, fmt.Errorf("canvas is %d bytes, the limit is %d", file.Size, maxCanvasBytes)
	}
	url := file.URLPrivateDownload
	if url == "" {
		url = file.URLPrivate
	}
	if url == "" {
		return nil, fmt.Errorf("%s has no downloadable content (filetype %q)", canvasID, file.Filetype)
	}

	var buf bytes.Buffer
	if err := h.ap(ctx).Slack().GetFileContext(ctx, url, &buf); err != nil {
		return nil, fmt.Errorf("downloading canvas %s: %w", canvasID, err)
	}
	return jsonResult(map[string]any{
		"canvas_id": canvasID,
		"title":     file.Title,
		"permalink": file.Permalink,
		"updated":   file.Timestamp.Time().UTC().Format("2006-01-02T15:04:05Z"),
		"html":      buf.String(),
	})
}

var canvasOperations = map[string]bool{
	"insert_at_end": true, "insert_at_start": true, "insert_after": true,
	"insert_before": true, "replace": true, "delete": true,
}

// CanvasesEditHandler applies one change to a canvas (canvases.edit).
func (h *CanvasesHandler) CanvasesEditHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesEditHandler called", zap.Any("params", request.Params))
	if err := requireWrites("SLACK_MCP_CANVAS_TOOL", "canvases_edit"); err != nil {
		return nil, err
	}

	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id is required")
	}
	op := request.GetString("operation", "insert_at_end")
	if !canvasOperations[op] {
		return nil, fmt.Errorf("operation must be one of insert_at_end, insert_at_start, insert_after, insert_before, replace, delete")
	}
	sectionID := request.GetString("section_id", "")
	if (op == "insert_after" || op == "insert_before" || op == "delete") && sectionID == "" {
		return nil, fmt.Errorf("operation %s needs section_id (find it with canvases_sections_lookup)", op)
	}
	markdown := request.GetString("markdown", "")
	if op != "delete" && markdown == "" {
		return nil, fmt.Errorf("operation %s needs markdown", op)
	}

	change := slack.CanvasChange{Operation: op, SectionID: sectionID}
	if op != "delete" {
		change.DocumentContent = slack.DocumentContent{Type: "markdown", Markdown: markdown}
	}
	if err := h.ap(ctx).Slack().EditCanvasContext(ctx, slack.EditCanvasParams{CanvasID: canvasID, Changes: []slack.CanvasChange{change}}); err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Applied %s to canvas %s", op, canvasID)), nil
}

// CanvasesSectionsLookupHandler finds section IDs for targeted canvas edits.
func (h *CanvasesHandler) CanvasesSectionsLookupHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesSectionsLookupHandler called", zap.Any("params", request.Params))

	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id is required")
	}
	criteria := slack.LookupCanvasSectionsCriteria{ContainsText: request.GetString("contains_text", "")}
	if types := request.GetString("section_types", ""); types != "" {
		for _, t := range strings.Split(types, ",") {
			if t = strings.TrimSpace(t); t != "" {
				criteria.SectionTypes = append(criteria.SectionTypes, t)
			}
		}
	}
	if criteria.ContainsText == "" && len(criteria.SectionTypes) == 0 {
		return nil, errors.New("set contains_text or section_types")
	}

	sections, err := h.ap(ctx).Slack().LookupCanvasSectionsContext(ctx, slack.LookupCanvasSectionsParams{CanvasID: canvasID, Criteria: criteria})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(sections))
	for _, s := range sections {
		ids = append(ids, s.ID)
	}
	return jsonResult(map[string]any{"section_ids": ids})
}

// CanvasesDeleteHandler deletes a canvas.
func (h *CanvasesHandler) CanvasesDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("CanvasesDeleteHandler called", zap.Any("params", request.Params))
	if err := requireWrites("SLACK_MCP_CANVAS_TOOL", "canvases_delete"); err != nil {
		return nil, err
	}
	canvasID := request.GetString("canvas_id", "")
	if canvasID == "" {
		return nil, errors.New("canvas_id is required")
	}
	if err := h.ap(ctx).Slack().DeleteCanvasContext(ctx, canvasID); err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(fmt.Sprintf("Deleted canvas %s", canvasID)), nil
}

// listsCall runs one slackLists.* method and returns Slack's response as-is,
// since list items carry column-specific value shapes.
func (h *CanvasesHandler) listsCall(ctx context.Context, method string, body map[string]any) (*mcp.CallToolResult, error) {
	var resp map[string]any
	if err := h.ap(ctx).Slack().APICall(ctx, method, body, &resp); err != nil {
		return nil, err
	}
	delete(resp, "ok")
	return jsonResult(resp)
}

func requireListID(request mcp.CallToolRequest) (string, error) {
	id := request.GetString("list_id", "")
	if id == "" {
		return "", errors.New("list_id is required")
	}
	return id, nil
}

// ListsCreateHandler creates a List (slackLists.create).
func (h *CanvasesHandler) ListsCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("ListsCreateHandler called", zap.Any("params", request.Params))
	if err := requireWrites("SLACK_MCP_LISTS_TOOL", "lists_create"); err != nil {
		return nil, err
	}
	name := request.GetString("name", "")
	if name == "" {
		return nil, errors.New("name is required")
	}
	body := map[string]any{"name": name}
	schema, err := jsonArg(request, "schema")
	if err != nil {
		return nil, err
	}
	if schema != nil {
		body["schema"] = schema
	}
	if request.GetBool("todo_mode", false) {
		body["todo_mode"] = true
	}
	return h.listsCall(ctx, "slackLists.create", body)
}

// ListsItemsListHandler lists a List's items (slackLists.items.list).
func (h *CanvasesHandler) ListsItemsListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("ListsItemsListHandler called", zap.Any("params", request.Params))
	listID, err := requireListID(request)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"list_id": listID, "limit": request.GetInt("limit", 100)}
	if cursor := request.GetString("cursor", ""); cursor != "" {
		body["cursor"] = cursor
	}
	if request.GetBool("archived", false) {
		body["archived"] = true
	}
	return h.listsCall(ctx, "slackLists.items.list", body)
}

// ListsItemGetHandler returns one List item (slackLists.items.info).
func (h *CanvasesHandler) ListsItemGetHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("ListsItemGetHandler called", zap.Any("params", request.Params))
	listID, err := requireListID(request)
	if err != nil {
		return nil, err
	}
	itemID := request.GetString("item_id", "")
	if itemID == "" {
		return nil, errors.New("item_id is required")
	}
	return h.listsCall(ctx, "slackLists.items.info", map[string]any{"list_id": listID, "id": itemID})
}

// ListsItemsCreateHandler adds an item to a List (slackLists.items.create).
func (h *CanvasesHandler) ListsItemsCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("ListsItemsCreateHandler called", zap.Any("params", request.Params))
	if err := requireWrites("SLACK_MCP_LISTS_TOOL", "lists_items_create"); err != nil {
		return nil, err
	}
	listID, err := requireListID(request)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"list_id": listID}
	fields, err := jsonArg(request, "initial_fields")
	if err != nil {
		return nil, err
	}
	if fields != nil {
		body["initial_fields"] = fields
	}
	if parent := request.GetString("parent_item_id", ""); parent != "" {
		body["parent_item_id"] = parent
	}
	return h.listsCall(ctx, "slackLists.items.create", body)
}

// ListsItemsUpdateHandler changes cells of List items (slackLists.items.update).
func (h *CanvasesHandler) ListsItemsUpdateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("ListsItemsUpdateHandler called", zap.Any("params", request.Params))
	if err := requireWrites("SLACK_MCP_LISTS_TOOL", "lists_items_update"); err != nil {
		return nil, err
	}
	listID, err := requireListID(request)
	if err != nil {
		return nil, err
	}
	cells, err := jsonArg(request, "cells")
	if err != nil {
		return nil, err
	}
	if cells == nil {
		return nil, errors.New("cells is required")
	}
	return h.listsCall(ctx, "slackLists.items.update", map[string]any{"list_id": listID, "cells": cells})
}

// ListsItemsDeleteHandler deletes List items (slackLists.items.delete / deleteMultiple).
func (h *CanvasesHandler) ListsItemsDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("ListsItemsDeleteHandler called", zap.Any("params", request.Params))
	if err := requireWrites("SLACK_MCP_LISTS_TOOL", "lists_items_delete"); err != nil {
		return nil, err
	}
	listID, err := requireListID(request)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, id := range strings.Split(request.GetString("item_ids", ""), ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	switch len(ids) {
	case 0:
		return nil, errors.New("item_ids is required")
	case 1:
		return h.listsCall(ctx, "slackLists.items.delete", map[string]any{"list_id": listID, "id": ids[0]})
	default:
		return h.listsCall(ctx, "slackLists.items.deleteMultiple", map[string]any{"list_id": listID, "ids": ids})
	}
}
