package store

import (
	"context"
	"errors"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrBadQuery is returned for a search query with no terms.
var ErrBadQuery = errors.New("bad search query")

const (
	maxSearchTerms = 5
	snippetContext = 30 // runes kept on each side of the first match
)

// SearchHit is one message matching a search, with the text around its first match.
type SearchHit struct {
	Message MessageView `json:"message"`
	Field   string      `json:"field"` // body / files / article: where the snippet comes from
	Snippet string      `json:"snippet"`
	// Ranges are the matches within Snippet as [start, length], counted in UTF-16 code units
	// (JavaScript string indices).
	Ranges [][2]int `json:"ranges"`
}

// SearchTerms splits a query on whitespace into at most five distinct terms; nil when blank.
func SearchTerms(q string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Fields(q) {
		if seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
		if len(out) == maxSearchTerms {
			break
		}
	}
	return out
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Search finds the messages whose text, document file names or archived article contain every
// term of q (case-insensitively, as substrings), newest first. convKey limits it to one chat
// (positive) or one bot's chats (-botId); 0 searches everything. beforeID pages.
func (s *Store) Search(ctx context.Context, q string, convKey, beforeID int64, limit int) ([]SearchHit, error) {
	terms := SearchTerms(q)
	if terms == nil {
		return nil, ErrBadQuery
	}
	query, args := searchQuery(terms, convKey, beforeID, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	type found struct {
		id                   int64
		body, files, article string
	}
	var hits []found
	for rows.Next() {
		var h found
		if err := rows.Scan(&h.id, &h.body, &h.files, &h.article); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []SearchHit{}
	if len(hits) == 0 {
		return out, nil
	}
	ids := make([]any, len(hits))
	for i, h := range hits {
		ids[i] = h.id
	}
	views, err := collectViews(s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages WHERE id IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, ids...))
	if err != nil {
		return nil, err
	}
	if err := s.hydrate(ctx, views); err != nil {
		return nil, err
	}
	byID := make(map[int64]MessageView, len(views))
	for _, v := range views {
		byID[v.ID] = v
	}
	for _, h := range hits {
		v, ok := byID[h.id]
		if !ok {
			continue
		}
		field, text := "body", h.body
		for _, c := range []struct{ name, text string }{{"body", h.body}, {"files", h.files}, {"article", h.article}} {
			if containsAny(c.text, terms) {
				field, text = c.name, c.text
				break
			}
		}
		snip, ranges := makeSnippet(text, terms)
		out = append(out, SearchHit{Message: v, Field: field, Snippet: snip, Ranges: ranges})
	}
	return out, nil
}

// minIndexedTerm is the shortest term the trigram index can answer.
const minIndexedTerm = 3

// searchQuery builds the search: every term in one of the three columns, in scope, newest first.
// Terms of three or more characters go to the full-text index as quoted phrases (which also folds
// non-ASCII case); shorter ones can only be LIKE-scanned, which folds ASCII case only.
func searchQuery(terms []string, convKey, beforeID int64, limit int) (string, []any) {
	var phrases []string
	where := []string{"m.deleted_at = 0", "m.thread_root_id = 0", "(? = 0 OR m.id < ?)"}
	args := []any{beforeID, beforeID}
	for _, t := range terms {
		if utf8.RuneCountInString(t) >= minIndexedTerm {
			phrases = append(phrases, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
		}
	}
	from := "search_fts f JOIN messages m ON m.id = f.rowid"
	if len(phrases) > 0 {
		where = append(where, "search_fts MATCH ?")
		args = append(args, strings.Join(phrases, " AND "))
	}
	switch {
	case convKey > 0:
		where = append(where, "m.chat_id = ?")
		args = append(args, convKey)
	case convKey < 0:
		where = append(where, "m.chat_id IN (SELECT id FROM chats WHERE bot_id = ?)")
		args = append(args, -convKey)
	}
	for _, t := range terms {
		if utf8.RuneCountInString(t) >= minIndexedTerm {
			continue
		}
		where = append(where, `(f.body LIKE ? ESCAPE '\' OR f.files LIKE ? ESCAPE '\' OR f.article LIKE ? ESCAPE '\')`)
		p := "%" + likeEscaper.Replace(t) + "%"
		args = append(args, p, p, p)
	}
	args = append(args, limit)
	return `SELECT f.rowid, f.body, f.files, f.article FROM ` + from + `
		WHERE ` + strings.Join(where, " AND ") + ` ORDER BY m.id DESC LIMIT ?`, args
}

func containsAny(text string, terms []string) bool {
	low := strings.ToLower(text)
	for _, t := range terms {
		if strings.Contains(low, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

// makeSnippet cuts text (whitespace runs collapsed to one space) to the first match of any term
// with snippetContext runes either side, marking cuts with an ellipsis, and lists every match
// inside it as UTF-16 [start, length] ranges.
func makeSnippet(text string, terms []string) (string, [][2]int) {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	lower := []rune(strings.ToLower(string(runes)))
	if len(lower) != len(runes) { // a case mapping changed the length: match on the text as is
		lower = runes
	}
	low := make([][]rune, len(terms))
	for i, t := range terms {
		low[i] = []rune(strings.ToLower(t))
	}
	first, firstLen := -1, 0
	for _, t := range low {
		if p := runeIndex(lower, t, 0); p >= 0 && (first < 0 || p < first) {
			first, firstLen = p, len(t)
		}
	}
	start, end := 0, len(runes)
	if first >= 0 {
		start, end = max(0, first-snippetContext), min(len(runes), first+firstLen+snippetContext)
	} else if end > 2*snippetContext {
		end = 2 * snippetContext
	}
	var b []rune
	if start > 0 {
		b = append(b, '…')
	}
	b = append(b, runes[start:end]...)
	if end < len(runes) {
		b = append(b, '…')
	}
	offset := 0
	if start > 0 {
		offset = 1
	}
	window := lower[start:end]
	type span struct{ from, n int } // in runes of b
	var spans []span
	for _, t := range low {
		if len(t) == 0 {
			continue
		}
		for p := runeIndex(window, t, 0); p >= 0; p = runeIndex(window, t, p+len(t)) {
			spans = append(spans, span{p + offset, len(t)})
		}
	}
	// Sorted by position, overlaps dropped, so the WebUI can slice them in order.
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].from < spans[j-1].from; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	ranges := [][2]int{}
	last := -1
	for _, sp := range spans {
		if sp.from < last {
			continue
		}
		ranges = append(ranges, [2]int{utf16Len(b[:sp.from]), utf16Len(b[sp.from : sp.from+sp.n])})
		last = sp.from + sp.n
	}
	return string(b), ranges
}

func runeIndex(s, sub []rune, from int) int {
	for i := from; i+len(sub) <= len(s); i++ {
		match := true
		for j := range sub {
			if s[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func utf16Len(rs []rune) int {
	n := 0
	for _, r := range rs {
		n += utf16.RuneLen(r)
	}
	return n
}
