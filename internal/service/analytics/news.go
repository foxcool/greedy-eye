package analytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/robfig/cron/v3"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// defaultNewsCron is when a source is due unless it says otherwise.
	defaultNewsCron = "*/30 * * * *"
	// defaultDocumentsPage is a widget's worth of headlines.
	defaultDocumentsPage = 50
	// feedTimeout bounds one source, so one slow feed cannot use up the
	// whole run and starve the sources after it.
	feedTimeout = time.Minute
	// maxSourceErrorRunes keeps a recorded failure readable in a list.
	maxSourceErrorRunes = 500
	maxSourceNameRunes  = 200
)

// ListDocumentsOpts filters and pages documents, newest published first.
type ListDocumentsOpts struct {
	SourceID  string
	Since     time.Time // inclusive; zero means no lower bound
	PageSize  int
	PageToken string
}

// NewsSourcePatch names the fields an update changes; nil leaves a field.
type NewsSourcePatch struct {
	URL     *string
	Name    *string
	Enabled *bool
	Cron    *string
	Weight  *int32
}

// NewsStore is the data access contract for news. Analytics is otherwise a
// read-only view over other domains; news is the one thing it stores.
type NewsStore interface {
	ListDocuments(ctx context.Context, opts ListDocumentsOpts) ([]*entity.NewsDocument, string, error)
	ListNewsSources(ctx context.Context) ([]*entity.NewsSource, error)
	// CreateNewsSource returns store.ErrAlreadyExists when the URL is taken.
	CreateNewsSource(ctx context.Context, s *entity.NewsSource) (*entity.NewsSource, error)
	UpdateNewsSource(ctx context.Context, id string, p NewsSourcePatch) (*entity.NewsSource, error)
	// DeleteNewsSource removes the source and its documents.
	DeleteNewsSource(ctx context.Context, id string) error
	// InsertDocuments stores the documents whose url_hash is new and returns
	// how many that was: a feed read twice stores nothing the second time.
	InsertDocuments(ctx context.Context, docs []*entity.NewsDocument) (int, error)
	// RecordSourceFetch stamps an attempt: a success when fetchErr is empty,
	// otherwise the error and its time.
	RecordSourceFetch(ctx context.Context, id string, at time.Time, fetchErr string) error
}

// FeedReader reads one feed. Satisfied by *rss.Client.
type FeedReader interface {
	Fetch(ctx context.Context, url string) ([]entity.FeedItem, error)
}

// WithNews returns a new Handler that serves and ingests news. Without it the
// news RPCs answer Unimplemented.
func (h *Handler) WithNews(s NewsStore, feeds FeedReader) *Handler {
	copied := *h
	copied.news = s
	copied.feeds = feeds
	return &copied
}

func (h *Handler) newsConfigured() error {
	if h.news == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("news is not configured"))
	}
	return nil
}

// ListDocuments returns headlines newest published first. Any signed-in user
// may read: the sources are shared, like the asset catalogue.
func (h *Handler) ListDocuments(ctx context.Context, req *connect.Request[apiv1.ListDocumentsRequest]) (*connect.Response[apiv1.ListDocumentsResponse], error) {
	if err := h.newsConfigured(); err != nil {
		return nil, err
	}
	if err := requireUser(ctx); err != nil {
		return nil, err
	}
	opts := ListDocumentsOpts{
		SourceID:  req.Msg.GetSourceId(),
		PageSize:  int(req.Msg.GetPageSize()),
		PageToken: req.Msg.GetPageToken(),
	}
	if opts.PageSize <= 0 {
		opts.PageSize = defaultDocumentsPage
	}
	if req.Msg.Since != nil {
		opts.Since = req.Msg.Since.AsTime()
	}
	docs, next, err := h.news.ListDocuments(ctx, opts)
	if err != nil {
		return nil, newsError(err)
	}
	out := make([]*apiv1.NewsDocument, 0, len(docs))
	for _, d := range docs {
		out = append(out, documentToProto(d))
	}
	return connect.NewResponse(&apiv1.ListDocumentsResponse{Documents: out, NextPageToken: next}), nil
}

// ListNewsSources returns every source with its last outcome. Not admin-only:
// whoever reads the headlines is the one who needs to know a feed is down.
func (h *Handler) ListNewsSources(ctx context.Context, _ *connect.Request[apiv1.ListNewsSourcesRequest]) (*connect.Response[apiv1.ListNewsSourcesResponse], error) {
	if err := h.newsConfigured(); err != nil {
		return nil, err
	}
	if err := requireUser(ctx); err != nil {
		return nil, err
	}
	sources, err := h.news.ListNewsSources(ctx)
	if err != nil {
		return nil, newsError(err)
	}
	out := make([]*apiv1.NewsSource, 0, len(sources))
	for _, s := range sources {
		out = append(out, sourceToProto(s))
	}
	return connect.NewResponse(&apiv1.ListNewsSourcesResponse{Sources: out}), nil
}

func (h *Handler) CreateNewsSource(ctx context.Context, req *connect.Request[apiv1.CreateNewsSourceRequest]) (*connect.Response[apiv1.NewsSource], error) {
	if err := h.newsConfigured(); err != nil {
		return nil, err
	}
	if err := requireNewsAdmin(ctx); err != nil {
		return nil, err
	}
	msg := req.Msg
	if msg.Kind != apiv1.NewsSourceKind_NEWS_SOURCE_KIND_UNSPECIFIED && msg.Kind != apiv1.NewsSourceKind_NEWS_SOURCE_KIND_RSS {
		return nil, invalid(fmt.Errorf("unknown source kind %s", msg.Kind))
	}
	src := &entity.NewsSource{
		Kind:    entity.NewsSourceRSS,
		Enabled: true,
		Cron:    defaultNewsCron,
		Weight:  1,
	}
	var err error
	if src.URL, err = validSourceURL(msg.Url); err != nil {
		return nil, err
	}
	if src.Name, err = validSourceName(msg.Name); err != nil {
		return nil, err
	}
	if msg.Enabled != nil {
		src.Enabled = *msg.Enabled
	}
	if msg.Cron != nil {
		if src.Cron, err = validCron(*msg.Cron); err != nil {
			return nil, err
		}
	}
	if msg.Weight != nil {
		if src.Weight, err = validWeight(*msg.Weight); err != nil {
			return nil, err
		}
	}
	created, err := h.news.CreateNewsSource(ctx, src)
	if err != nil {
		return nil, newsError(err)
	}
	return connect.NewResponse(sourceToProto(created)), nil
}

func (h *Handler) UpdateNewsSource(ctx context.Context, req *connect.Request[apiv1.UpdateNewsSourceRequest]) (*connect.Response[apiv1.NewsSource], error) {
	if err := h.newsConfigured(); err != nil {
		return nil, err
	}
	if err := requireNewsAdmin(ctx); err != nil {
		return nil, err
	}
	msg := req.Msg
	if msg.Id == "" {
		return nil, invalid(errors.New("id is required"))
	}
	var p NewsSourcePatch
	if msg.Url != nil {
		u, err := validSourceURL(*msg.Url)
		if err != nil {
			return nil, err
		}
		p.URL = &u
	}
	if msg.Name != nil {
		n, err := validSourceName(*msg.Name)
		if err != nil {
			return nil, err
		}
		p.Name = &n
	}
	if msg.Cron != nil {
		c, err := validCron(*msg.Cron)
		if err != nil {
			return nil, err
		}
		p.Cron = &c
	}
	if msg.Weight != nil {
		w, err := validWeight(*msg.Weight)
		if err != nil {
			return nil, err
		}
		p.Weight = &w
	}
	p.Enabled = msg.Enabled
	updated, err := h.news.UpdateNewsSource(ctx, msg.Id, p)
	if err != nil {
		return nil, newsError(err)
	}
	return connect.NewResponse(sourceToProto(updated)), nil
}

func (h *Handler) DeleteNewsSource(ctx context.Context, req *connect.Request[apiv1.DeleteNewsSourceRequest]) (*connect.Response[apiv1.DeleteNewsSourceResponse], error) {
	if err := h.newsConfigured(); err != nil {
		return nil, err
	}
	if err := requireNewsAdmin(ctx); err != nil {
		return nil, err
	}
	if req.Msg.Id == "" {
		return nil, invalid(errors.New("id is required"))
	}
	if err := h.news.DeleteNewsSource(ctx, req.Msg.Id); err != nil {
		return nil, newsError(err)
	}
	return connect.NewResponse(&apiv1.DeleteNewsSourceResponse{}), nil
}

// NewsReport is what one ingest run did. Every due source is in exactly one
// of Fetched and Failed.
type NewsReport struct {
	Enabled  int
	Due      int // enabled sources whose cron fired since their last attempt
	Fetched  int
	Failed   int
	Inserted int
	Skipped  int // items with no title, no usable link or no date
	Failures []NewsFailure
}

// NewsFailure names a source that failed this run.
type NewsFailure struct {
	SourceID string
	Name     string
	Error    string
}

// IngestNews reads every enabled source that is due and stores the new
// documents. A source that fails has its error recorded on it and the run
// moves on: one dead feed must not empty the others. The returned error is
// only for not being able to start at all.
func (h *Handler) IngestNews(ctx context.Context) (NewsReport, error) {
	var report NewsReport
	if h.news == nil || h.feeds == nil {
		return report, errors.New("news is not configured")
	}
	sources, err := h.news.ListNewsSources(ctx)
	if err != nil {
		return report, fmt.Errorf("list news sources: %w", err)
	}
	now := h.now()
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		report.Enabled++
		if isDue(src, now) {
			report.Due++
			h.runSource(ctx, src, now, &report)
		}
	}
	return report, nil
}

// isDue reports whether the source's cron fired since its last attempt. A
// source never attempted is due, and so is one whose cron does not parse:
// ingestSource then fails it, so the bad cron is recorded on the source.
func isDue(src *entity.NewsSource, now time.Time) bool {
	sched, err := cron.ParseStandard(src.Cron)
	if err != nil || src.LastFetchedAt == nil {
		return true
	}
	return !sched.Next(*src.LastFetchedAt).After(now)
}

// runSource ingests one due source and records the outcome on the source and
// in the report.
func (h *Handler) runSource(ctx context.Context, src *entity.NewsSource, now time.Time, report *NewsReport) {
	inserted, skipped, err := h.ingestSource(ctx, src, now)
	report.Inserted += inserted
	report.Skipped += skipped
	var failure string // empty records a success
	if err != nil {
		failure = entity.TruncateRunes(err.Error(), maxSourceErrorRunes)
		report.Failed++
		report.Failures = append(report.Failures, NewsFailure{SourceID: src.ID, Name: src.Name, Error: failure})
	} else {
		report.Fetched++
	}
	if err := h.news.RecordSourceFetch(ctx, src.ID, now, failure); err != nil {
		h.log.Error("news: source outcome not recorded",
			slog.String("source_id", src.ID), slog.Any("error", err))
	}
}

// ingestSource fetches one feed and stores what is new in it.
func (h *Handler) ingestSource(ctx context.Context, src *entity.NewsSource, now time.Time) (inserted, skipped int, err error) {
	if _, err := cron.ParseStandard(src.Cron); err != nil {
		return 0, 0, fmt.Errorf("invalid cron %q: %w", src.Cron, err)
	}
	fetchCtx, cancel := context.WithTimeout(ctx, feedTimeout)
	defer cancel()
	items, err := h.feeds.Fetch(fetchCtx, src.URL)
	if err != nil {
		return 0, 0, err
	}
	docs := make([]*entity.NewsDocument, 0, len(items))
	for _, it := range items {
		doc, ok := documentFromItem(src.ID, it, now)
		if !ok {
			skipped++
			continue
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return 0, skipped, nil
	}
	inserted, err = h.news.InsertDocuments(ctx, docs)
	if err != nil {
		return 0, skipped, fmt.Errorf("store documents: %w", err)
	}
	return inserted, skipped, nil
}

// documentFromItem keeps the headline, the canonical link and a snippet. An
// item without a publication date is skipped rather than given the fetch
// time: a document is shown with when it was published, and a guess would
// put an old item at the top of the list.
func documentFromItem(sourceID string, it entity.FeedItem, now time.Time) (*entity.NewsDocument, bool) {
	title := strings.TrimSpace(it.Title)
	if title == "" || it.PublishedAt.IsZero() {
		return nil, false
	}
	canonical, err := entity.CanonicalURL(it.Link)
	if err != nil || len(canonical) > entity.MaxDocumentURLBytes {
		return nil, false
	}
	hash, err := entity.URLHash(canonical)
	if err != nil {
		return nil, false
	}
	return &entity.NewsDocument{
		SourceID:    sourceID,
		URL:         canonical,
		Title:       entity.TruncateRunes(title, entity.MaxDocumentTitleRunes),
		Snippet:     entity.TruncateRunes(strings.TrimSpace(it.Summary), entity.MaxDocumentSnippetRunes),
		PublishedAt: it.PublishedAt.UTC(),
		FetchedAt:   now,
		URLHash:     hash,
	}, true
}

func requireUser(ctx context.Context) error {
	if _, ok := middleware.UserFromContext(ctx); !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	return nil
}

// requireNewsAdmin gates source mutations: a source is read for every user,
// so adding or removing one changes everyone's headlines.
func requireNewsAdmin(ctx context.Context) error {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if !user.IsAdmin() {
		return connect.NewError(connect.CodePermissionDenied, errors.New("changing news sources is admin-only"))
	}
	return nil
}

func validSourceURL(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", invalid(errors.New("url is required"))
	}
	if _, err := entity.CanonicalURL(u); err != nil {
		return "", invalid(fmt.Errorf("url: %w", err))
	}
	// Userinfo becomes an Authorization header on fetch, and the URL is shown
	// to every reader of ListNewsSources.
	if parsed, err := url.Parse(u); err != nil || parsed.User != nil {
		return "", invalid(errors.New("url must not carry credentials"))
	}
	if len(u) > entity.MaxDocumentURLBytes {
		return "", invalid(errors.New("url is too long"))
	}
	return u, nil
}

func validSourceName(raw string) (string, error) {
	n := strings.TrimSpace(raw)
	if n == "" {
		return "", invalid(errors.New("name is required"))
	}
	if len([]rune(n)) > maxSourceNameRunes {
		return "", invalid(fmt.Errorf("name is longer than %d characters", maxSourceNameRunes))
	}
	return n, nil
}

func validCron(raw string) (string, error) {
	c := strings.TrimSpace(raw)
	if _, err := cron.ParseStandard(c); err != nil {
		return "", invalid(fmt.Errorf("cron %q: %w", c, err))
	}
	return c, nil
}

func validWeight(w int32) (int32, error) {
	if w < 0 {
		return 0, invalid(errors.New("weight must not be negative"))
	}
	return w, nil
}

func invalid(err error) error {
	return connect.NewError(connect.CodeInvalidArgument, err)
}

func newsError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrInvalidArgument):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, store.ErrAlreadyExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, store.ErrConstraint):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func documentToProto(d *entity.NewsDocument) *apiv1.NewsDocument {
	return &apiv1.NewsDocument{
		Id:          d.ID,
		SourceId:    d.SourceID,
		SourceName:  d.SourceName,
		Url:         d.URL,
		Title:       d.Title,
		Snippet:     d.Snippet,
		PublishedAt: timestamppb.New(d.PublishedAt),
	}
}

func sourceToProto(s *entity.NewsSource) *apiv1.NewsSource {
	out := &apiv1.NewsSource{
		Id:        s.ID,
		Kind:      apiv1.NewsSourceKind_NEWS_SOURCE_KIND_RSS,
		Url:       s.URL,
		Name:      s.Name,
		Enabled:   s.Enabled,
		Cron:      s.Cron,
		Weight:    s.Weight,
		LastError: s.LastError,
		CreatedAt: timestamppb.New(s.CreatedAt),
		UpdatedAt: timestamppb.New(s.UpdatedAt),
	}
	if s.LastFetchedAt != nil {
		out.LastFetchedAt = timestamppb.New(*s.LastFetchedAt)
	}
	if s.LastSuccessAt != nil {
		out.LastSuccessAt = timestamppb.New(*s.LastSuccessAt)
	}
	if s.LastErrorAt != nil {
		out.LastErrorAt = timestamppb.New(*s.LastErrorAt)
	}
	return out
}
