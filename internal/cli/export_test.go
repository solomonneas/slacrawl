package cli

import (
	"encoding/json"
	"testing"

	"github.com/openclaw/slacrawl/internal/adapter"
	"github.com/openclaw/slacrawl/internal/store"
)

func TestBuildSlackMessageRecord(t *testing.T) {
	msg := store.MessageRow{
		WorkspaceID:   "T1",
		WorkspaceName: "Acme",
		ChannelID:     "C1",
		ChannelName:   "eng",
		TS:            "1623456789.000200",
		UserID:        "U1",
		UserName:      "alice",
		Text:          "ship it",
		ThreadTS:      "1623456700.000100",
		ReplyCount:    2,
		Subtype:       "",
	}
	ch := store.ChannelRow{WorkspaceID: "T1", ID: "C1", Name: "eng", Kind: "public_channel"}

	rec := buildSlackMessageRecord(msg, ch, "/tmp/slacrawl.db", "9.9.9")

	if rec.Source.Kind != "slack" || rec.Source.Version != "9.9.9" {
		t.Fatalf("source = %+v", rec.Source)
	}
	if rec.Collection.ExternalID != "slack:channel:C1" || rec.Collection.Kind != "slack_channel" || rec.Collection.Name != "eng" {
		t.Fatalf("collection = %+v", rec.Collection)
	}
	if rec.Item.ExternalID != "slack:message:C1:1623456789.000200" || rec.Item.Text != "ship it" {
		t.Fatalf("item = %+v", rec.Item)
	}
	// 1623456789 -> 2021-06-12T00:13:09Z
	if rec.Item.CreatedAt != "2021-06-12T00:13:09Z" {
		t.Fatalf("created_at = %q, want 2021-06-12T00:13:09Z", rec.Item.CreatedAt)
	}
	if rec.Actor == nil || rec.Actor.ExternalID != "slack:user:U1" || rec.Actor.Type != "human" {
		t.Fatalf("actor = %+v", rec.Actor)
	}
	if len(rec.Relations) != 1 || rec.Relations[0].TargetExternalID != "slack:message:C1:1623456700.000100" || rec.Relations[0].Type != "thread_reply" {
		t.Fatalf("relations = %+v", rec.Relations)
	}

	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := adapter.Parse(line); err != nil {
		t.Fatalf("adapter.Parse rejected emitted record: %v", err)
	}
}

func TestBuildSlackMessageRecordThreadRootNoSelfRelation(t *testing.T) {
	// A thread root message (thread_ts == ts) must not relate to itself, and a
	// missing user id must fall back to a system actor.
	rec := buildSlackMessageRecord(store.MessageRow{
		ChannelID: "C2",
		TS:        "1623456789.000200",
		ThreadTS:  "1623456789.000200",
		Text:      "thread start",
	}, store.ChannelRow{}, "", "")

	if len(rec.Relations) != 0 {
		t.Fatalf("thread root should have no relation, got %+v", rec.Relations)
	}
	if rec.Actor.Type != "system" {
		t.Fatalf("actor type = %q, want system", rec.Actor.Type)
	}
	if rec.Collection.Name != "C2" {
		t.Fatalf("collection name fallback = %q, want C2", rec.Collection.Name)
	}
	line, _ := json.Marshal(rec)
	if _, err := adapter.Parse(line); err != nil {
		t.Fatalf("adapter.Parse rejected record: %v", err)
	}
}

func TestSlackTSToRFC3339(t *testing.T) {
	cases := map[string]string{
		"1623456789.000200": "2021-06-12T00:13:09Z",
		"1623456789":        "2021-06-12T00:13:09Z",
		"":                  "",
		"not-a-ts":          "",
	}
	for in, want := range cases {
		if got := slackTSToRFC3339(in); got != want {
			t.Errorf("slackTSToRFC3339(%q) = %q, want %q", in, got, want)
		}
	}
}
