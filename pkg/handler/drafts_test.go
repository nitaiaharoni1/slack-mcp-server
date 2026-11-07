package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type draftCall struct {
	method string
	form   url.Values
}

// fakeDrafts answers drafts.* calls from canned JSON. Tests never reach Slack.
type fakeDrafts struct {
	calls     []draftCall
	responses map[string]string
	err       error
}

func (f *fakeDrafts) DraftCall(_ context.Context, method string, form url.Values, out any) error {
	f.calls = append(f.calls, draftCall{method, form})
	if f.err != nil {
		return f.err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(f.responses[method]), out)
}

const rawDraftJSON = `{"id":"Dr1","last_updated_ts":"1700000000.123","date_created":1700000000,
	"destinations":[{"channel_id":"C1","thread_ts":"1699.000100","broadcast":true}],"file_ids":["F1"],
	"blocks":[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hello"}]}]}]}`

func TestUnitPadDraftTs(t *testing.T) {
	assert.Equal(t, "1700000000.1230000", padDraftTs("1700000000.123"))
	assert.Equal(t, "1700000000.1234567", padDraftTs("1700000000.1234567"))
	assert.Equal(t, "1700000000", padDraftTs("1700000000"))
}

func TestUnitCreateDraft(t *testing.T) {
	api := &fakeDrafts{responses: map[string]string{"drafts.create": `{"ok":true,"draft":` + rawDraftJSON + `}`}}
	draft, err := createDraft(context.Background(), api, "hi <@U9>",
		draftDestination{ChannelID: "D1", ThreadTs: "1699.000100"}, []string{"F1", "F2"})
	require.NoError(t, err)
	assert.Equal(t, "Dr1", draft.ID)

	require.Len(t, api.calls, 1)
	call := api.calls[0]
	assert.Equal(t, "drafts.create", call.method, "creating a draft must only ever call drafts.create")
	assert.Equal(t, "true", call.form.Get("is_from_composer"))
	assert.NotEmpty(t, call.form.Get("client_msg_id"))
	assert.JSONEq(t, `[{"channel_id":"D1","thread_ts":"1699.000100"}]`, call.form.Get("destinations"))
	assert.JSONEq(t, `["F1","F2"]`, call.form.Get("file_ids"))

	blocks := call.form.Get("blocks")
	assert.Contains(t, blocks, `"type":"rich_text"`)
	assert.Contains(t, blocks, `"user_id":"U9"`, "a mention must become a real mention element")
}

func TestUnitCreateDraftInAChannelSendsNoThread(t *testing.T) {
	api := &fakeDrafts{responses: map[string]string{"drafts.create": `{"draft":` + rawDraftJSON + `}`}}
	_, err := createDraft(context.Background(), api, "hi", draftDestination{ChannelID: "C1"}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"channel_id":"C1"}]`, api.calls[0].form.Get("destinations"))
	assert.JSONEq(t, `[]`, api.calls[0].form.Get("file_ids"), "nil files must be an empty array, not null")
}

func TestUnitCreateDraftRefusals(t *testing.T) {
	dest := draftDestination{ChannelID: "C1"}

	t.Run("empty text makes no call", func(t *testing.T) {
		api := &fakeDrafts{}
		_, err := createDraft(context.Background(), api, "  \n", dest, nil)
		assert.EqualError(t, err, "text is required")
		assert.Empty(t, api.calls)
	})

	t.Run("over the limit makes no call", func(t *testing.T) {
		api := &fakeDrafts{}
		_, err := createDraft(context.Background(), api, strings.Repeat("a", 12001), dest, nil)
		require.Error(t, err)
		assert.Empty(t, api.calls)
	})

	t.Run("a response with no draft is an error", func(t *testing.T) {
		api := &fakeDrafts{responses: map[string]string{"drafts.create": `{"ok":true}`}}
		_, err := createDraft(context.Background(), api, "hi", dest, nil)
		assert.Error(t, err)
	})
}

func TestUnitDraftTextGetsRTLDirection(t *testing.T) {
	blocks, err := draftBlocks("היי")
	require.NoError(t, err)
	assert.Contains(t, blocks, "‏")
}

func TestUnitListDrafts(t *testing.T) {
	api := &fakeDrafts{responses: map[string]string{"drafts.list": `{"ok":true,"drafts":[` + rawDraftJSON + `]}`}}
	drafts, err := listDrafts(context.Background(), api, 10, true)
	require.NoError(t, err)
	assert.Equal(t, "10", api.calls[0].form.Get("limit"))
	assert.Equal(t, "true", api.calls[0].form.Get("is_active"))

	require.Len(t, drafts, 1)
	view := drafts[0].view()
	assert.Equal(t, "Dr1", view.ID)
	assert.Equal(t, "1700000000.123", view.LastUpdatedTs)
	assert.Equal(t, "2023-11-14T22:13:20Z", view.Created)
	assert.Empty(t, view.Scheduled)
	assert.Contains(t, view.Text, "hello")
	assert.Equal(t, []string{"F1"}, view.FileIDs)
	assert.Equal(t, "C1", view.Destinations[0].ChannelID)
}

func TestUnitListDraftsEmptyAndInactive(t *testing.T) {
	api := &fakeDrafts{responses: map[string]string{"drafts.list": `{"ok":true,"drafts":[]}`}}
	drafts, err := listDrafts(context.Background(), api, 0, false)
	require.NoError(t, err)
	assert.Empty(t, drafts)
	assert.False(t, api.calls[0].form.Has("is_active"))
	assert.False(t, api.calls[0].form.Has("limit"))
}

func TestUnitFindDraftNotFound(t *testing.T) {
	api := &fakeDrafts{responses: map[string]string{"drafts.list": `{"drafts":[` + rawDraftJSON + `]}`}}
	_, err := findDraft(context.Background(), api, "Dr404")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Dr404")

	found, err := findDraft(context.Background(), api, "Dr1")
	require.NoError(t, err)
	assert.Equal(t, "Dr1", found.ID)
}

func TestUnitFindCurrentDraft(t *testing.T) {
	api := &fakeDrafts{responses: map[string]string{"drafts.list": `{"drafts":[` + rawDraftJSON + `]}`}}

	t.Run("same timestamp, also when padded", func(t *testing.T) {
		for _, ts := range []string{"1700000000.123", "1700000000.1230000"} {
			found, err := findCurrentDraft(context.Background(), api, "Dr1", ts)
			require.NoError(t, err)
			assert.Equal(t, "Dr1", found.ID)
		}
	})

	t.Run("a draft edited since is refused", func(t *testing.T) {
		_, err := findCurrentDraft(context.Background(), api, "Dr1", "1699999999.000")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "changed since")
	})

	t.Run("a missing draft is refused", func(t *testing.T) {
		_, err := findCurrentDraft(context.Background(), api, "Dr404", "1700000000.123")
		assert.Error(t, err)
	})
}

func TestUnitUpdateDraftKeepsBroadcastAndPadsTs(t *testing.T) {
	api := &fakeDrafts{responses: map[string]string{"drafts.update": `{"draft":` + rawDraftJSON + `}`}}
	dest := []draftDestination{{ChannelID: "C1", ThreadTs: "1699.000100", Broadcast: true}}
	_, err := updateDraft(context.Background(), api, "Dr1", "1700000000.123", "new text", dest, []string{"F1"})
	require.NoError(t, err)

	call := api.calls[0]
	assert.Equal(t, "drafts.update", call.method)
	assert.Equal(t, "Dr1", call.form.Get("draft_id"))
	assert.Equal(t, "1700000000.1230000", call.form.Get("client_last_updated_ts"))
	assert.JSONEq(t, `[{"channel_id":"C1","thread_ts":"1699.000100","broadcast":true}]`, call.form.Get("destinations"))
	assert.JSONEq(t, `["F1"]`, call.form.Get("file_ids"))
}

func TestUnitDeleteDraft(t *testing.T) {
	api := &fakeDrafts{}
	require.NoError(t, deleteDraft(context.Background(), api, "Dr1", "1700000000.123"))
	assert.Equal(t, "drafts.delete", api.calls[0].method)
	assert.Equal(t, "Dr1", api.calls[0].form.Get("draft_id"))
	assert.Equal(t, "1700000000.1230000", api.calls[0].form.Get("client_last_updated_ts"))
}

func TestUnitDraftErrorsSayWhenTheSessionEnded(t *testing.T) {
	expired := &fakeDrafts{err: errors.New("drafts.list: invalid_auth")}
	_, err := listDrafts(context.Background(), expired, 0, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "set fresh ones")

	other := &fakeDrafts{err: errors.New("drafts.delete: draft_not_found")}
	err = deleteDraft(context.Background(), other, "Dr1", "1.2")
	assert.EqualError(t, err, "drafts.delete: draft_not_found")
}

func TestUnitParseFileIDs(t *testing.T) {
	assert.Equal(t, []string{"F1", "F2"}, parseFileIDs(" F1, F2 ,"))
	assert.Equal(t, []string{}, parseFileIDs(""))
}
