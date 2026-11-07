package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rusq/slackdump/v3/auth"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func draftTestClient(t *testing.T, token string, handler http.HandlerFunc) *MCPSlackClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	value, err := auth.NewValueAuth(token, "xoxd-cookie")
	require.NoError(t, err)
	return &MCPSlackClient{httpClient: srv.Client(), teamEndpoint: srv.URL + "/", authProvider: value}
}

func TestUnitDraftCallPostsAForm(t *testing.T) {
	var path, contentType string
	var form url.Values
	client := draftTestClient(t, "xoxc-session", func(w http.ResponseWriter, r *http.Request) {
		path, contentType = r.URL.Path, r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(raw))
		_, _ = w.Write([]byte(`{"ok":true,"value":"x"}`))
	})

	var out struct {
		Value string `json:"value"`
	}
	err := client.DraftCall(context.Background(), "drafts.list", url.Values{"limit": {"5"}}, &out)
	require.NoError(t, err)
	assert.Equal(t, "/api/drafts.list", path)
	assert.Equal(t, "application/x-www-form-urlencoded", contentType)
	assert.Equal(t, "xoxc-session", form.Get("token"))
	assert.Equal(t, "5", form.Get("limit"))
	assert.Equal(t, "x", out.Value)
}

func TestUnitDraftCallSurfacesSlackErrorsWithoutTheToken(t *testing.T) {
	client := draftTestClient(t, "xoxc-secret", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	})
	err := client.DraftCall(context.Background(), "drafts.list", url.Values{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "drafts.list: invalid_auth")
	assert.NotContains(t, err.Error(), "xoxc-secret")
}

func TestUnitCredentialsSessionAuth(t *testing.T) {
	logger := zap.NewNop()
	cases := []struct {
		name  string
		creds Credentials
		want  bool
	}{
		{"oauth with both", Credentials{XOXP: "xoxp-a", XOXC: "xoxc-a", XOXD: "xoxd-a"}, true},
		{"oauth alone", Credentials{XOXP: "xoxp-a"}, false},
		{"oauth with xoxc only", Credentials{XOXP: "xoxp-a", XOXC: "xoxc-a"}, false},
		{"oauth with xoxd only", Credentials{XOXP: "xoxp-a", XOXD: "xoxd-a"}, false},
		{"session as the main auth", Credentials{XOXC: "xoxc-a", XOXD: "xoxd-a"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.creds.sessionAuth(logger) != nil)
		})
	}
}

func TestUnitAuthProviderKeepsOAuthFirst(t *testing.T) {
	value, err := Credentials{XOXP: "xoxp-a", XOXC: "xoxc-a", XOXD: "xoxd-a"}.authProvider(zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, "xoxp-a", value.SlackToken(), "session tokens must not take over the account's other tools")
}

func TestUnitDrafts(t *testing.T) {
	t.Run("oauth without session tokens names the variables", func(t *testing.T) {
		ap := &ApiProvider{name: "work", client: &MCPSlackClient{isOAuth: true}, logger: zap.NewNop()}
		_, err := ap.Drafts()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SLACK_MCP_WORK_XOXC_TOKEN")
		assert.Contains(t, err.Error(), "SLACK_MCP_WORK_XOXD_TOKEN")
	})

	t.Run("bot token is refused", func(t *testing.T) {
		ap := &ApiProvider{name: "work", client: &MCPSlackClient{isOAuth: true, isBotToken: true}, logger: zap.NewNop()}
		_, err := ap.Drafts()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bot token")
	})

	t.Run("session account uses its own client", func(t *testing.T) {
		client := &MCPSlackClient{}
		ap := &ApiProvider{name: "work", client: client, logger: zap.NewNop()}
		got, err := ap.Drafts()
		require.NoError(t, err)
		assert.Same(t, client, got)
	})

	t.Run("demo account has no client", func(t *testing.T) {
		var none *MCPSlackClient
		ap := &ApiProvider{name: "work", client: none, logger: zap.NewNop()}
		_, err := ap.Drafts()
		require.Error(t, err)
	})
}

func TestUnitSameUser(t *testing.T) {
	me := &slack.AuthTestResponse{TeamID: "T1", UserID: "U1"}
	assert.NoError(t, sameUser("work", me, &slack.AuthTestResponse{TeamID: "T1", UserID: "U1"}))

	for name, other := range map[string]*slack.AuthTestResponse{
		"other workspace": {TeamID: "T2", UserID: "U1"},
		"other user":      {TeamID: "T1", UserID: "U2"},
	} {
		t.Run(name, func(t *testing.T) {
			err := sameUser("work", me, other)
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), `account "work"`))
		})
	}
	assert.Error(t, sameUser("work", nil, me))
}
