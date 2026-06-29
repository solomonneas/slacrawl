package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/slacrawl/internal/adapter"
	"github.com/openclaw/slacrawl/internal/store"
)

// runExport routes `slacrawl export <subcommand>`. Today the only target is the
// miseledger.adapter.v1 JSONL contract consumed by MiseLedger.
func (a *App) runExport(ctx context.Context, configPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("export requires a subcommand (adapter)")
	}
	switch args[0] {
	case "adapter":
		return a.runExportAdapter(ctx, configPath, args[1:])
	default:
		return fmt.Errorf("unknown export subcommand %q", args[0])
	}
}

// runExportAdapter walks the local archive and emits one miseledger.adapter.v1
// JSON record per Slack message to stdout (or --out), so the common pipe is:
//
//	slacrawl export adapter | miseledger crawl adapter -
//
// The progress summary goes to stderr to keep stdout a clean JSONL stream.
func (a *App) runExportAdapter(ctx context.Context, configPath string, args []string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("export adapter", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	workspaceID := fs.String("workspace", "", "workspace id")
	channelID := fs.String("channel", "", "restrict to a channel id")
	limit := fs.Int("limit", 0, "maximum messages to emit (0 = all)")
	outPath := fs.String("out", "-", "output file or - for stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := a.openReadableStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ws := coalesce(*workspaceID, cfg.WorkspaceID)

	// Channel rows give each message's collection a real name and kind.
	channels, err := st.Channels(ctx, ws, "", 100000)
	if err != nil {
		return err
	}
	channelByID := make(map[string]store.ChannelRow, len(channels))
	for _, c := range channels {
		channelByID[c.ID] = c
	}

	// slacrawl's Messages applies LIMIT directly, so 0 would mean zero rows.
	// Treat a non-positive --limit as "all" via a max-int sentinel.
	effLimit := *limit
	if effLimit <= 0 {
		effLimit = math.MaxInt32
	}
	messages, err := st.Messages(ctx, ws, *channelID, "", effLimit)
	if err != nil {
		return err
	}

	out := a.Stdout
	if *outPath != "-" {
		f, err := os.Create(*outPath)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		out = f
	}

	enc := json.NewEncoder(out)
	count := 0
	for _, m := range messages {
		rec := buildSlackMessageRecord(m, channelByID[m.ChannelID], cfg.DBPath, version)
		if err := enc.Encode(rec); err != nil {
			return err
		}
		count++
	}
	fmt.Fprintf(a.Stderr, "exported %d slack message(s) to miseledger.adapter.v1\n", count)
	return nil
}

// buildSlackMessageRecord maps one archived Slack message (plus its channel)
// onto the adapter contract: the channel is the collection, the message is the
// item, the author is the actor, and a threaded reply becomes a thread_reply
// relation. A Slack message is identified by channel + ts, so the item external
// id embeds both. It is a pure function so the mapping is unit testable.
func buildSlackMessageRecord(m store.MessageRow, ch store.ChannelRow, dbPath, sourceVersion string) adapter.Record {
	createdAt := slackTSToRFC3339(m.TS)

	channelName := firstNonEmpty(m.ChannelName, ch.Name, m.ChannelID)

	collectionMeta := map[string]any{"workspace_id": m.WorkspaceID}
	if kind := firstNonEmpty(ch.Kind); kind != "" {
		collectionMeta["channel_kind"] = kind
	}
	if m.WorkspaceName != "" {
		collectionMeta["workspace_name"] = m.WorkspaceName
	}

	itemMeta := map[string]any{
		"workspace_id": m.WorkspaceID,
		"channel_id":   m.ChannelID,
		"ts":           m.TS,
	}
	if m.ThreadTS != "" {
		itemMeta["thread_ts"] = m.ThreadTS
	}
	if m.Subtype != "" {
		itemMeta["subtype"] = m.Subtype
	}
	if m.ReplyCount > 0 {
		itemMeta["reply_count"] = m.ReplyCount
	}

	actorName := firstNonEmpty(m.UserName, m.UserID)
	actorType := "human"
	actorKey := m.UserID
	if actorKey == "" {
		actorKey = actorName
		actorType = "system"
	}

	// A non-empty thread_ts that differs from the message's own ts means this
	// message is a reply within that thread's root message.
	relations := []adapter.Relation{}
	if m.ThreadTS != "" && m.ThreadTS != m.TS {
		relations = append(relations, adapter.Relation{
			TargetExternalID: "slack:message:" + m.ChannelID + ":" + m.ThreadTS,
			Type:             "thread_reply",
		})
	}

	externalID := "slack:message:" + m.ChannelID + ":" + m.TS
	rawSeed := []byte(m.ChannelID + "\x1f" + m.TS + "\x1f" + m.Text)

	return adapter.Record{
		Schema: adapter.SchemaV1,
		Source: adapter.Source{Kind: "slack", Name: "slack", Version: sourceVersion},
		Collection: adapter.Collection{
			ExternalID: "slack:channel:" + m.ChannelID,
			Kind:       "slack_channel",
			Name:       channelName,
			Metadata:   metadataJSON(collectionMeta),
		},
		Item: adapter.Item{
			ExternalID: externalID,
			Kind:       "message",
			CreatedAt:  createdAt,
			Text:       m.Text,
			Tags:       []string{"slack", "message"},
			Metadata:   metadataJSON(itemMeta),
		},
		Actor: &adapter.Actor{
			ExternalID: "slack:user:" + actorKey,
			Type:       actorType,
			Name:       actorName,
		},
		Artifacts: []adapter.Artifact{},
		Links:     []adapter.Link{},
		Relations: relations,
		Raw: adapter.RawRef{
			Format: "slack/message",
			Hash:   "sha256:" + hashHex(rawSeed),
			Path:   dbPath,
		},
	}
}

// slackTSToRFC3339 converts a Slack message ts ("1623456789.000200", unix epoch
// seconds with a microsecond-style suffix) into an RFC3339Nano UTC timestamp.
// Only the integer seconds carry real time; the suffix is an ordering counter,
// so it is preserved in metadata, not the timestamp. Returns "" if unparseable.
func slackTSToRFC3339(ts string) string {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return ""
	}
	secPart := ts
	if dot := strings.IndexByte(ts, '.'); dot >= 0 {
		secPart = ts[:dot]
	}
	sec, err := strconv.ParseInt(secPart, 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339Nano)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func metadataJSON(v map[string]any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
