package httpapi

import (
	"net/http"
	"syscall"
)

func (s *Server) listAllMedia(w http.ResponseWriter, r *http.Request) {
	before, ok := queryInt(r, "before", 0, 0, 1<<62)
	limit, ok2 := queryInt(r, "limit", 60, 1, 100)
	if !ok || !ok2 {
		writeErr(w, http.StatusBadRequest, "bad before or limit")
		return
	}
	q := r.URL.Query()
	typ, source := q.Get("type"), q.Get("source")
	if typ == "" {
		typ = "all"
	}
	if source == "" {
		source = "all"
	}
	msgs, err := s.Store.ListAllMedia(r.Context(), typ, source, before, int(limit))
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	tz, ok := queryInt(r, "tz", 0, -840, 840)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad tz")
		return
	}
	st, err := s.Store.Stats(r.Context(), int(tz), s.Now().Unix())
	if err != nil {
		storeErr(w, err)
		return
	}
	var fs syscall.Statfs_t
	if s.Cfg.DataDir != "" && syscall.Statfs(s.Cfg.DataDir, &fs) == nil {
		st.Totals.DiskFree = int64(fs.Bavail) * int64(fs.Bsize)
		st.Totals.DiskTotal = int64(fs.Blocks) * int64(fs.Bsize)
	}
	writeJSON(w, http.StatusOK, st)
}
