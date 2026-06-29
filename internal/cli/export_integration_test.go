package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/slacrawl/internal/adapter"
	"github.com/openclaw/slacrawl/internal/store"
	"github.com/stretchr/testify/require"
)

// TestExportAdapterEndToEnd seeds a real slacrawl store, runs the actual
// `export adapter` command through App.Run, and validates that stdout is a clean
// stream of miseledger.adapter.v1 records (summary goes to stderr).
func TestExportAdapterEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	dbPath := filepath.Join(tmp, "slacrawl.db")

	var stdout, stderr bytes.Buffer
	app := &App{Stdout: &stdout, Stderr: &stderr}
	ctx := context.Background()
	require.NoError(t, app.Run(ctx, []string{"--config", configPath, "init", "--db", dbPath}))

	st, err := store.Open(dbPath)
	require.NoError(t, err)
	now := time.Now().UTC()
	root := fmt.Sprintf("%d.%06d", now.Add(-2*time.Hour).Unix(), 100)
	reply := fmt.Sprintf("%d.%06d", now.Add(-1*time.Hour).Unix(), 200)
	require.NoError(t, st.UpsertWorkspace(ctx, store.Workspace{ID: "T1", Name: "team", RawJSON: "{}", UpdatedAt: now}))
	require.NoError(t, st.UpsertChannel(ctx, store.Channel{ID: "C1", WorkspaceID: "T1", Name: "engineering", Kind: "public_channel", RawJSON: "{}", UpdatedAt: now}))
	require.NoError(t, st.UpsertUser(ctx, store.User{ID: "U1", WorkspaceID: "T1", Name: "alice", DisplayName: "Alice", RawJSON: "{}", UpdatedAt: now}))
	require.NoError(t, st.UpsertMessage(ctx, store.Message{
		ChannelID: "C1", TS: root, WorkspaceID: "T1", UserID: "U1",
		Text: "thread start", NormalizedText: "thread start", ThreadTS: root, RawJSON: "{}", UpdatedAt: now,
	}, nil))
	require.NoError(t, st.UpsertMessage(ctx, store.Message{
		ChannelID: "C1", TS: reply, WorkspaceID: "T1", UserID: "U1",
		Text: "a reply", NormalizedText: "a reply", ThreadTS: root, RawJSON: "{}", UpdatedAt: now,
	}, nil))
	require.NoError(t, st.Close())

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, app.Run(ctx, []string{"--config", configPath, "export", "adapter"}))

	// stdout must be pure JSONL; the human summary is on stderr.
	require.Contains(t, stderr.String(), "miseledger.adapter.v1")
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.Len(t, lines, 2, "expected two adapter records")

	sawReply := false
	for _, line := range lines {
		rec, err := adapter.Parse([]byte(line))
		require.NoError(t, err, "every emitted line must satisfy the adapter contract")
		require.Equal(t, "slack", rec.Source.Kind)
		require.Equal(t, "slack_channel", rec.Collection.Kind)
		require.Equal(t, "engineering", rec.Collection.Name)
		if len(rec.Relations) == 1 {
			require.Equal(t, "thread_reply", rec.Relations[0].Type)
			require.Equal(t, "slack:message:C1:"+root, rec.Relations[0].TargetExternalID)
			sawReply = true
		}
	}
	require.True(t, sawReply, "the reply message should carry a thread_reply relation")
}
