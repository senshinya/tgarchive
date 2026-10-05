package telegraph

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"slices"
	"time"

	"tgarchive/internal/events"
	"tgarchive/internal/store"
)

// Receipts reports the link message's archiving state to the sender (receipt.Engine).
type Receipts interface {
	Evaluate(ctx context.Context, messageID int64)
}

const (
	ReasonNotFound = "文章不存在"
	ReasonNetwork  = "网络错误"
	ReasonInternal = "内部错误"
)

// Worker fetches queued Telegraph jobs one at a time; it is independent of the userbot queue.
type Worker struct {
	st       *store.Store
	api      *Client
	receipts Receipts
	hub      *events.Hub
	wakeDL   func()
	wake     chan struct{}

	Delays []time.Duration // waits after the 1st..3rd transient failure; one more failure fails the job
	Poll   time.Duration   // idle re-check interval
	Now    func() time.Time
}

func NewWorker(st *store.Store, api *Client, rc Receipts, hub *events.Hub, wakeDL func()) *Worker {
	return &Worker{st: st, api: api, receipts: rc, hub: hub, wakeDL: wakeDL, wake: make(chan struct{}, 1),
		Delays: []time.Duration{10 * time.Second, 60 * time.Second, 300 * time.Second}, Poll: 30 * time.Second, Now: time.Now}
}

// Wake makes an idle Run look for work now (the collector calls it after queueing a job).
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) Run(ctx context.Context) {
	if n, err := w.st.RequeueFetchingTelegraphJobs(ctx, w.Now().Unix()); err != nil {
		if ctx.Err() == nil {
			log.Printf("telegraph: requeue interrupted jobs: %v", err)
		}
	} else if n > 0 {
		log.Printf("telegraph: requeued %d interrupted jobs", n)
	}
	for ctx.Err() == nil {
		did, err := w.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("telegraph: queue: %v", err)
		}
		if did {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-time.After(w.Poll):
		}
	}
}

// RunOnce processes the oldest queued job and reports whether there was one.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, err := w.st.ClaimNextTelegraphJob(ctx, w.Now().Unix())
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	w.publish(job)
	w.process(ctx, job)
	return true, nil
}

func (w *Worker) process(ctx context.Context, job *store.TelegraphJob) {
	attempts := job.Attempts
	for {
		page, err := w.api.GetPage(ctx, job.Path)
		if ctx.Err() != nil {
			return // left 'fetching'; Run requeues it on the next start
		}
		if err == nil {
			err = w.save(ctx, job, page)
			if errors.Is(err, store.ErrNotFound) {
				return // the link message was deleted meanwhile
			}
			if err != nil {
				// Log the real cause but never surface it to the user — save() failures are
				// internal (DB, encoding, …), not something the sender's phrasing should quote.
				log.Printf("telegraph: job %d: save: %v", job.ID, err)
				w.finish(ctx, job, store.TelegraphFailed, ReasonInternal)
				return
			}
			w.publish(job)
			// Evaluate before waking the downloader so 👀 always precedes 👌.
			w.receipts.Evaluate(ctx, job.MessageID)
			if w.wakeDL != nil {
				w.wakeDL()
			}
			return
		}
		var ae *APIError
		if errors.As(err, &ae) {
			reason := ReasonNotFound
			if ae.Code != "PAGE_NOT_FOUND" {
				reason = "Telegraph 错误：" + ae.Code
			}
			w.finish(ctx, job, store.TelegraphFailed, reason)
			return
		}
		attempts++
		log.Printf("telegraph: job %d (%s) attempt %d: %v", job.ID, job.Path, attempts, err)
		if attempts > len(w.Delays) {
			w.finish(ctx, job, store.TelegraphFailed, ReasonNetwork)
			return
		}
		if err := w.st.SetTelegraphJobAttempts(ctx, job.ID, attempts, ReasonNetwork, w.Now().Unix()); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return
			}
			log.Printf("telegraph: job %d: record attempt: %v", job.ID, err)
		}
		if !sleep(ctx, w.Delays[attempts-1]) {
			return
		}
	}
}

func (w *Worker) save(ctx context.Context, job *store.TelegraphJob, p *Page) error {
	nodes, refs := Normalize(p.Content)
	image := ""
	if u, ok := absURL(p.ImageURL); ok {
		image = u
		if !slices.ContainsFunc(refs, func(r MediaRef) bool { return r.URL == u }) {
			refs = append(refs, MediaRef{URL: u, Kind: KindPhoto})
		}
	}
	media := make([]store.ArticleMediaInput, 0, len(refs))
	for _, r := range refs {
		media = append(media, store.ArticleMediaInput{DedupeKey: WebKey(r.URL), URL: r.URL, Kind: r.Kind})
	}
	pageURL, ok := strictURL(p.URL)
	if !ok {
		pageURL = SiteURL + "/" + url.PathEscape(job.Path)
	}
	authorURL, _ := linkURL(p.AuthorURL)
	title := p.Title
	if title == "" {
		title = job.Path
	}
	return w.st.SaveArticle(ctx, store.SaveArticleInput{
		JobID: job.ID, MessageID: job.MessageID, Path: job.Path, URL: pageURL, Title: title, Description: p.Description,
		AuthorName: p.AuthorName, AuthorURL: authorURL, ImageURL: image, Views: p.Views, Media: media,
		Render: func(ids map[string]int64) (string, error) {
			AttachMediaIDs(nodes, ids)
			b, err := json.Marshal(nodes)
			return string(b), err
		},
		Now: w.Now().Unix(),
	})
}

func (w *Worker) finish(ctx context.Context, job *store.TelegraphJob, state, reason string) {
	if err := w.st.FinishTelegraphJob(ctx, job.ID, state, reason, w.Now().Unix()); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Printf("telegraph: finish job %d: %v", job.ID, err)
		}
		return
	}
	w.publish(job)
	w.receipts.Evaluate(ctx, job.MessageID)
}

func (w *Worker) publish(job *store.TelegraphJob) {
	w.hub.Publish(events.Event{Type: "message.updated", Data: map[string]int64{"chat_id": job.ChatID, "message_id": job.MessageID}})
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
