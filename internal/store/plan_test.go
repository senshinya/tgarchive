package store

import (
	"strings"
	"testing"
)

// queryPlan returns the EXPLAIN QUERY PLAN lines of q.
func queryPlan(t *testing.T, s *Store, q string, args ...any) []string {
	t.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	return out
}

// wantPlan fails unless q's plan uses an index whose name starts with each of indexes, and when
// it scans a table whole (SCAN without an index) or sorts in a temporary b-tree.
func wantPlan(t *testing.T, s *Store, name, q string, indexes []string, args ...any) {
	t.Helper()
	plan := queryPlan(t, s, q, args...)
	joined := strings.Join(plan, "\n")
	for _, ix := range indexes {
		if !strings.Contains(joined, "INDEX "+ix) {
			t.Errorf("%s: plan does not use %s:\n%s", name, ix, joined)
		}
	}
	for _, line := range plan {
		if (strings.HasPrefix(line, "SCAN ") && !strings.Contains(line, "INDEX")) || strings.Contains(line, "TEMP B-TREE") {
			t.Errorf("%s: %q in plan:\n%s", name, line, joined)
		}
	}
}

// The comment queries reach the comments of one post through the thread index; its WHERE
// thread_root_id != 0 must be stated by the query for SQLite to consider it.
func TestCommentQueriesUseThreadIndex(t *testing.T) {
	s := newStore(t)
	sc := threadScope(1)
	for name, q := range map[string]string{
		"comment state": `SELECT COALESCE(MAX(tg_message_id), 0), COUNT(*) FROM messages
			WHERE thread_root_id != 0 AND thread_root_id = ? AND deleted_at = 0`,
		"recent commenters": `SELECT extra_json FROM messages WHERE thread_root_id != 0 AND thread_root_id = ? AND deleted_at = 0
			ORDER BY tg_message_id DESC LIMIT 200`,
		"newest comments": `SELECT ` + msgCols + ` FROM messages WHERE ` + sc.cond + ` AND deleted_at = 0 ORDER BY tg_message_id DESC, id DESC LIMIT 50`,
		"older comments": `SELECT ` + msgCols + ` FROM messages WHERE ` + sc.cond + ` AND deleted_at = 0 AND (tg_message_id, id) < (5, 5)
			ORDER BY tg_message_id DESC, id DESC LIMIT 50`,
		"newer comments": `SELECT ` + msgCols + ` FROM messages WHERE ` + sc.cond + ` AND deleted_at = 0 AND (tg_message_id, id) > (5, 5)
			ORDER BY tg_message_id, id LIMIT 50`,
	} {
		wantPlan(t, s, name, q, []string{"messages_thread"}, sc.arg)
	}
}

// A channel's timeline walks messages_posts past no comment; a bot chat's walks
// messages_chat_page in arrival order.
func TestTimelineQueriesUsePostIndex(t *testing.T) {
	s := newStore(t)
	seedWatch(t, s)
	res, err := s.Ingest(ctx, IngestInput{ChannelID: chanID, Msg: channelPost(1, "", "k1"), Now: 1})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := s.chatScope(ctx, res.ChatID)
	if err != nil || channel.key != "tg_message_id" {
		t.Fatalf("scope = %+v, %v", channel, err)
	}
	private := scope{channel.cond, 999, "id"}
	for _, c := range []struct {
		name  string
		sc    scope
		index string
	}{{"channel", channel, "messages_posts"}, {"private", private, "messages_chat_page"}} {
		k := c.sc.key
		for name, q := range map[string]string{
			"newest": `SELECT ` + msgCols + ` FROM messages WHERE ` + c.sc.cond + ` AND deleted_at = 0 ORDER BY ` + k + ` DESC, id DESC LIMIT 50`,
			"older": `SELECT ` + msgCols + ` FROM messages WHERE ` + c.sc.cond + ` AND deleted_at = 0 AND (` + k + `, id) < (5, 5)
				ORDER BY ` + k + ` DESC, id DESC LIMIT 50`,
			"newer": `SELECT ` + msgCols + ` FROM messages WHERE ` + c.sc.cond + ` AND deleted_at = 0 AND (` + k + `, id) > (5, 5)
				ORDER BY ` + k + `, id LIMIT 50`,
		} {
			wantPlan(t, s, c.name+" "+name, q, []string{c.index}, c.sc.arg)
		}
	}
}

// Collecting orphans looks up the candidates by id and their links through indexes.
func TestCollectOrphansPlan(t *testing.T) {
	s := newStore(t)
	wantPlan(t, s, "orphans", `SELECT id, kind, path FROM media WHERE id IN (?, ?)
		AND NOT EXISTS (SELECT 1 FROM message_media mm WHERE mm.media_id = media.id)
		AND NOT EXISTS (SELECT 1 FROM custom_emoji ce WHERE ce.media_id = media.id) ORDER BY id`,
		[]string{"message_media_media", "custom_emoji_media"}, 1, 2)
}
