package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AccountArg is the argument every tool takes to pick the Slack account.
const AccountArg = "account"

// addTool registers a tool with the shared optional account argument.
func addTool(s *server.MCPServer, registry *provider.Registry, tool mcp.Tool, h server.ToolHandlerFunc) {
	if tool.InputSchema.Properties == nil {
		tool.InputSchema.Properties = map[string]any{}
	}
	tool.InputSchema.Properties[AccountArg] = map[string]any{
		"type": "string",
		"description": fmt.Sprintf("Slack account to act as. One of: %s. Default: %s. Use list_accounts to see which workspace and user each one is.",
			strings.Join(registry.Names(), ", "), registry.DefaultName()),
	}
	s.AddTool(tool, h)
}

// accountMiddleware resolves the account argument before the handler runs, so
// every handler talks to that account's own client and caches.
func accountMiddleware(registry *provider.Registry) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ap, err := registry.Get(req.GetString(AccountArg, ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if ap.IsBotToken() && botUnsupportedTools[req.Params.Name] {
				return mcp.NewToolResultError(fmt.Sprintf("%s is not available for account %q: it uses a bot token", req.Params.Name, ap.Name())), nil
			}
			if ap.IsOAuth() && sessionOnlyTools[req.Params.Name] {
				return mcp.NewToolResultError(fmt.Sprintf("%s is not available for account %q: it needs browser session tokens (xoxc/xoxd)", req.Params.Name, ap.Name())), nil
			}
			return next(provider.WithAccount(ctx, ap), req)
		}
	}
}

var botUnsupportedTools = map[string]bool{
	ToolConversationsSearchMessages: true,
	ToolConversationsUnreads:        true,
	ToolSavedList:                   true,
	ToolSavedUpdate:                 true,
	ToolSavedClearCompleted:         true,
}

var sessionOnlyTools = map[string]bool{
	ToolSavedList:           true,
	ToolSavedUpdate:         true,
	ToolSavedClearCompleted: true,
}
