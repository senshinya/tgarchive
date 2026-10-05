package httpapi

import (
	"net/http"

	"tgarchive/internal/store"
)

// failedListLimit caps how many failed media the downloads panel lists.
const failedListLimit = 50

type activeDownload struct {
	store.DownloadItem
	Done      int64 `json:"done"`
	Total     int64 `json:"total"`
	StartedAt int64 `json:"started_at"`
}

type queueView struct {
	Count int64 `json:"count"`
	Bytes int64 `json:"bytes"`
}

type downloadsView struct {
	Active []activeDownload     `json:"active"`
	Queued queueView            `json:"queued"`
	Failed []store.DownloadItem `json:"failed"`
	Speed  int64                `json:"speed"`
}

// downloads is the downloads panel's snapshot: what is downloading now (with byte progress),
// how much is waiting, and what failed.
func (s *Server) downloads(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	progress := s.Downloader.Progress.Snapshot()
	ids := make([]int64, len(progress))
	for i, p := range progress {
		ids[i] = p.MediaID
	}
	items, err := s.Store.DownloadItems(ctx, ids)
	if err != nil {
		storeErr(w, err)
		return
	}
	out := downloadsView{Active: []activeDownload{}, Speed: s.Downloader.Progress.Speed()}
	for _, p := range progress {
		if it, ok := items[p.MediaID]; ok {
			out.Active = append(out.Active, activeDownload{DownloadItem: it, Done: p.Done, Total: p.Total, StartedAt: p.StartedAt})
		}
	}
	if out.Queued.Count, out.Queued.Bytes, err = s.Store.QueueStats(ctx, ids); err != nil {
		storeErr(w, err)
		return
	}
	if out.Failed, err = s.Store.FailedDownloads(ctx, failedListLimit); err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
