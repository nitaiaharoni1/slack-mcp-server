package server

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AccountArg is the argument every tool takes to pick the Slack account.
const AccountArg = "account"

// writeTools holds the tools that change something in Slack (no read-only
// hint). With several accounts these must name their account explicitly.
var writeTools = map[string]bool{}

// actionTools default to a read-only "list" action; only their other actions write.
var actionTools = map[string]bool{ToolPins: true}

// addTool registers a tool with the shared account argument.
func addTool(s *server.MCPServer, registry *provider.Registry, tool mcp.Tool, h server.ToolHandlerFunc) {
	if tool.InputSchema.Properties == nil {
		tool.InputSchema.Properties = map[string]any{}
	}
	readOnly := tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint
	if !readOnly {
		writeTools[tool.Name] = true
	}

	desc := fmt.Sprintf("Slack account to act as. One of: %s. Use list_accounts to see each one's workspace and user.",
		strings.Join(registry.Names(), ", "))
	switch {
	case !registry.IsMulti():
		desc += " Default: " + registry.DefaultName() + "."
	case actionTools[tool.Name]:
		desc += " Default for action=list: " + registry.DefaultName() + ". Required for any other action: it changes Slack."
	case readOnly:
		desc += " Default: " + registry.DefaultName() + ". IDs (channels, users, messages) belong to one workspace: reuse the account the ID came from."
	default:
		desc += " Required for this tool: it changes Slack, so it never falls back to a default account."
	}
	tool.InputSchema.Properties[AccountArg] = map[string]any{"type": "string", "description": desc}
	if !readOnly && registry.IsMulti() && !actionTools[tool.Name] {
		tool.InputSchema.Required = append(tool.InputSchema.Required, AccountArg)
	}
	s.AddTool(tool, h)
}

func isWriteCall(req mcp.CallToolRequest) bool {
	if !writeTools[req.Params.Name] {
		return false
	}
	if actionTools[req.Params.Name] {
		action := req.GetString("action", "")
		return action != "" && action != "list"
	}
	return true
}

// channelIDPattern matches conversation IDs (public, private, DM, group DM).
var channelIDPattern = regexp.MustCompile(`^[CDG][A-Z0-9]{8,}$`)

// slackURLPattern finds workspace URLs such as https://acme.slack.com/archives/...
var slackURLPattern = regexp.MustCompile(`https?://[a-z0-9-]+\.(?:enterprise\.)?slack(?:-gov)?\.com/[^\s>|)]*`)

func workspaceOf(ap *provider.ApiProvider) string {
	ar, err := ap.Slack().AuthTest()
	if err != nil || ar == nil {
		return ""
	}
	ws, err := text.Workspace(ar.URL)
	if err != nil {
		return ""
	}
	return ws
}

// crossWorkspaceError catches IDs and links that belong to another configured
// account: a channel ID only another account's cache knows, or a Slack URL on
// another account's workspace. It returns "" when nothing points elsewhere.
func crossWorkspaceError(registry *provider.Registry, ap *provider.ApiProvider, req mcp.CallToolRequest) string {
	others := make([]*provider.ApiProvider, 0)
	for _, p := range registry.All() {
		if p != ap {
			others = append(others, p)
		}
	}
	if len(others) == 0 {
		return ""
	}

	if ch := req.GetString("channel_id", ""); channelIDPattern.MatchString(ch) && !ap.HasChannel(ch) {
		for _, p := range others {
			if p.HasChannel(ch) {
				return fmt.Sprintf("channel %s belongs to account %q, not %q. Call again with account=%q, or pick a channel from account %q.",
					ch, p.Name(), ap.Name(), p.Name(), ap.Name())
			}
		}
	}

	mine := workspaceOf(ap)
	for name, v := range req.GetArguments() {
		s, ok := v.(string)
		if !ok || name == AccountArg {
			continue
		}
		for _, link := range slackURLPattern.FindAllString(s, -1) {
			u, err := url.Parse(link)
			if err != nil {
				continue
			}
			ws := strings.Split(u.Hostname(), ".")[0]
			if ws == mine {
				continue
			}
			for _, p := range others {
				if workspaceOf(p) == ws {
					return fmt.Sprintf("the link in %s is on workspace %q, which is account %q, not %q. Call again with account=%q.",
						name, ws, p.Name(), ap.Name(), p.Name())
				}
			}
		}
	}
	return ""
}

// accountLabel tells the model which account produced a result.
func accountLabel(ap *provider.ApiProvider) string {
	ar, err := ap.Slack().AuthTest()
	if err != nil || ar == nil {
		return fmt.Sprintf("[slack account: %s]", ap.Name())
	}
	return fmt.Sprintf("[slack account: %s | workspace: %s (%s) | acting as: %s]", ap.Name(), ar.Team, strings.TrimSuffix(ar.URL, "/"), ar.User)
}

// accountMiddleware resolves the account argument before the handler runs, so
// every handler talks to that account's own client and caches. With several
// accounts it also refuses writes that do not name their account, refuses IDs
// and links from another account's workspace, and labels every result with the
// account it came from.
func accountMiddleware(registry *provider.Registry) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name := strings.TrimSpace(req.GetString(AccountArg, ""))
			if registry.IsMulti() && name == "" && isWriteCall(req) {
				return mcp.NewToolResultError(fmt.Sprintf(
					"%s changes Slack and several accounts are configured (%s): pass account explicitly. Run list_accounts if unsure which workspace is which.",
					req.Params.Name, strings.Join(registry.Names(), ", "))), nil
			}
			ap, err := registry.Get(name)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if ap.IsBotToken() && botUnsupportedTools[req.Params.Name] {
				return mcp.NewToolResultError(fmt.Sprintf("%s is not available for account %q: it uses a bot token", req.Params.Name, ap.Name())), nil
			}
			if ap.IsOAuth() && sessionOnlyTools[req.Params.Name] {
				return mcp.NewToolResultError(fmt.Sprintf("%s is not available for account %q: it needs browser session tokens (xoxc/xoxd)", req.Params.Name, ap.Name())), nil
			}
			if registry.IsMulti() {
				if msg := crossWorkspaceError(registry, ap, req); msg != "" {
					return mcp.NewToolResultError(msg), nil
				}
			}

			res, err := next(provider.WithAccount(ctx, ap), req)
			if !registry.IsMulti() || req.Params.Name == ToolListAccounts {
				return res, err
			}
			if err != nil {
				return res, fmt.Errorf("[slack account: %s] %w", ap.Name(), err)
			}
			if res == nil {
				return res, nil
			}
			label := mcp.NewTextContent(accountLabel(ap))
			res.Content = append([]mcp.Content{label}, res.Content...)
			return res, nil
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
