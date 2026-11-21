package analytics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/adapter/rss"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memNews is an in-memory NewsStore with the one property the ingest relies
// on: a document whose url_hash is already stored is not stored again.
type memNews struct {
	mu      sync.Mutex
	sources map[string]*entity.NewsSource
	docs    map[string]*entity.NewsDocument // by url_hash
}

func newMemNews(sources ...*entity.NewsSource) *memNews {
	m := &memNews{sources: map[string]*entity.NewsSource{}, docs: map[string]*entity.NewsDocument{}}
	for _, s := range sources {
		m.sources[s.ID] = s
	}
	return m
}

func (m *memNews) ListDocuments(_ context.Context, opts ListDocumentsOpts) ([]*entity.NewsDocument, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*entity.NewsDocument
	for _, d := range m.docs {
		if opts.SourceID != "" && d.SourceID != opts.SourceID {
			continue
		}
		if !opts.Since.IsZero() && d.PublishedAt.Before(opts.Since) {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublishedAt.After(out[j].PublishedAt) })
	return out, "", nil
}

func (m *memNews) ListNewsSources(context.Context) ([]*entity.NewsSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*entity.NewsSource
	for _, s := range m.sources {
		c := *s
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memNews) CreateNewsSource(_ context.Context, s *entity.NewsSource) (*entity.NewsSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.sources {
		if existing.URL == s.URL {
			return nil, store.ErrAlreadyExists
		}
	}
	c := *s
	c.ID = uuid.NewString()
	m.sources[c.ID] = &c
	return &c, nil
}

func (m *memNews) UpdateNewsSource(_ context.Context, id string, p NewsSourcePatch) (*entity.NewsSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sources[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if p.Name != nil {
		s.Name = *p.Name
	}
	if p.Enabled != nil {
		s.Enabled = *p.Enabled
	}
	if p.Cron != nil {
		s.Cron = *p.Cron
	}
	c := *s
	return &c, nil
}

func (m *memNews) DeleteNewsSource(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sources[id]; !ok {
		return store.ErrNotFound
	}
	delete(m.sources, id)
	return nil
}

func (m *memNews) InsertDocuments(_ context.Context, docs []*entity.NewsDocument) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range docs {
		if _, ok := m.docs[d.URLHash]; ok {
			continue
		}
		c := *d
		c.SourceName = m.sources[d.SourceID].Name
		m.docs[d.URLHash] = &c
		n++
	}
	return n, nil
}

func (m *memNews) RecordSourceFetch(_ context.Context, id string, at time.Time, fetchErr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sources[id]
	s.LastFetchedAt = &at
	if fetchErr == "" {
		s.LastSuccessAt = &at
	} else {
		s.LastError, s.LastErrorAt = fetchErr, &at
	}
	return nil
}

// recordedFeed is the RSS fixture the reader's own tests parse.
const recordedFeed = "../../adapter/rss/testdata/rss2.xml"

// fixtureFeeds answers each URL from a recorded file; a URL without one
// answers with a feed error.
type fixtureFeeds map[string]string

func (f fixtureFeeds) Fetch(_ context.Context, url string) ([]entity.FeedItem, error) {
	path, ok := f[url]
	if !ok {
		return nil, errors.New("feed answered 503 Service Unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return rss.Parse(data)
}

func newsHandler(m *memNews, feeds FeedReader, now time.Time) *Handler {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithNews(m, feeds)
	h.now = func() time.Time { return now }
	return h
}

func rssSource(name, url string) *entity.NewsSource {
	return &entity.NewsSource{ID: uuid.NewString(), Kind: entity.NewsSourceRSS, URL: url, Name: name, Enabled: true, Cron: defaultNewsCron, Weight: 1}
}

func asUser(roles ...string) context.Context {
	return middleware.ContextWithUser(context.Background(), &entity.User{ID: uuid.NewString(), Roles: roles})
}

func TestIngestNewsTwiceStoresOnce(t *testing.T) {
	src := rssSource("Example Wire", "https://news.example.com/rss/")
	m := newMemNews(src)
	feeds := fixtureFeeds{src.URL: recordedFeed}
	now := time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC)

	first, err := newsHandler(m, feeds, now).IngestNews(context.Background())
	require.NoError(t, err)
	// Four items in the fixture, one without a date: it has no time to be shown with.
	assert.Equal(t, 3, first.Inserted)
	assert.Equal(t, 1, first.Skipped)
	assert.Equal(t, 1, first.Fetched)
	assert.Equal(t, 0, first.Failed)

	// Due again an hour later; the same feed adds nothing.
	second, err := newsHandler(m, feeds, now.Add(time.Hour)).IngestNews(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, second.Fetched)
	assert.Equal(t, 0, second.Inserted)
	assert.Len(t, m.docs, 3)

	for _, d := range m.docs {
		assert.LessOrEqual(t, len([]rune(d.Snippet)), entity.MaxDocumentSnippetRunes)
		assert.NotContains(t, d.Snippet, "FULL ARTICLE")
		assert.NotContains(t, d.URL, "utm_", "stored URL is the canonical one")
	}
}

func TestIngestNewsFailingSourceIsRecordedAndOthersRun(t *testing.T) {
	broken := rssSource("Broken", "https://broken.example.com/rss")
	good := rssSource("Good", "https://news.example.com/rss/")
	m := newMemNews(broken, good)
	feeds := fixtureFeeds{good.URL: recordedFeed}
	now := time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC)

	report, err := newsHandler(m, feeds, now).IngestNews(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, report.Due)
	assert.Equal(t, 1, report.Failed)
	assert.Equal(t, 1, report.Fetched)
	assert.Equal(t, 3, report.Inserted)
	require.Len(t, report.Failures, 1)
	assert.Equal(t, broken.ID, report.Failures[0].SourceID)

	b := m.sources[broken.ID]
	assert.Contains(t, b.LastError, "503")
	require.NotNil(t, b.LastErrorAt)
	assert.Equal(t, now, *b.LastErrorAt)
	assert.Nil(t, b.LastSuccessAt)
	require.NotNil(t, m.sources[good.ID].LastSuccessAt)
}

func TestIngestNewsHonoursEachSourceSchedule(t *testing.T) {
	now := time.Date(2026, 11, 19, 15, 10, 0, 0, time.UTC)
	fetched := now.Add(-10 * time.Minute) // 15:00, next */30 fire is 15:30
	notDue := rssSource("Fresh", "https://fresh.example.com/rss")
	notDue.LastFetchedAt = &fetched
	disabled := rssSource("Off", "https://off.example.com/rss")
	disabled.Enabled = false
	m := newMemNews(notDue, disabled)

	report, err := newsHandler(m, fixtureFeeds{}, now).IngestNews(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, report.Due)
	assert.Equal(t, 1, report.Enabled)

	report, err = newsHandler(m, fixtureFeeds{notDue.URL: recordedFeed}, now.Add(20*time.Minute)).IngestNews(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, report.Due)
	assert.Equal(t, 1, report.Fetched)
}

func TestIngestNewsBadCronIsRecordedOnTheSource(t *testing.T) {
	src := rssSource("Bad Cron", "https://news.example.com/rss/")
	src.Cron = "every hour"
	m := newMemNews(src)
	now := time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC)

	report, err := newsHandler(m, fixtureFeeds{src.URL: recordedFeed}, now).IngestNews(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, report.Due)
	assert.Equal(t, 1, report.Failed)
	assert.Equal(t, 0, report.Inserted, "a source with a bad cron is not read")
	assert.Contains(t, m.sources[src.ID].LastError, "invalid cron")
	require.NotNil(t, m.sources[src.ID].LastErrorAt)
	assert.Nil(t, m.sources[src.ID].LastSuccessAt)
}

func TestListDocumentsNewestPublishedFirst(t *testing.T) {
	src := rssSource("Example Wire", "https://news.example.com/rss/")
	m := newMemNews(src)
	h := newsHandler(m, fixtureFeeds{src.URL: recordedFeed}, time.Date(2026, 11, 19, 15, 0, 0, 0, time.UTC))
	_, err := h.IngestNews(context.Background())
	require.NoError(t, err)

	resp, err := h.ListDocuments(asUser(), connect.NewRequest(&apiv1.ListDocumentsRequest{}))
	require.NoError(t, err)
	docs := resp.Msg.Documents
	require.Len(t, docs, 3)
	assert.Equal(t, "Bitcoin Holds Above $90K as Chipmaker Earnings Lift Risk Assets", docs[0].Title)
	assert.Equal(t, "Example Wire", docs[0].SourceName)
	assert.Equal(t, time.Date(2026, 11, 19, 13, 58, 12, 0, time.UTC), docs[0].PublishedAt.AsTime())
	for i := 1; i < len(docs); i++ {
		assert.False(t, docs[i].PublishedAt.AsTime().After(docs[i-1].PublishedAt.AsTime()))
	}
}

func TestListDocumentsNeedsAUser(t *testing.T) {
	h := newsHandler(newMemNews(), fixtureFeeds{}, time.Now())
	_, err := h.ListDocuments(context.Background(), connect.NewRequest(&apiv1.ListDocumentsRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestNewsSourceMutationsAreAdminOnly(t *testing.T) {
	src := rssSource("Example Wire", "https://news.example.com/rss/")
	m := newMemNews(src)
	h := newsHandler(m, fixtureFeeds{}, time.Now())
	user := asUser()

	_, err := h.CreateNewsSource(user, connect.NewRequest(&apiv1.CreateNewsSourceRequest{Url: "https://x.example.com/rss", Name: "X"}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	off := false
	_, err = h.UpdateNewsSource(user, connect.NewRequest(&apiv1.UpdateNewsSourceRequest{Id: src.ID, Enabled: &off}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = h.DeleteNewsSource(user, connect.NewRequest(&apiv1.DeleteNewsSourceRequest{Id: src.ID}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	assert.True(t, m.sources[src.ID].Enabled, "a refused update changed nothing")

	// Reading the sources, failures included, is not admin-only.
	list, err := h.ListNewsSources(user, connect.NewRequest(&apiv1.ListNewsSourcesRequest{}))
	require.NoError(t, err)
	assert.Len(t, list.Msg.Sources, 1)
}

func TestCreateNewsSourceValidatesAndDefaults(t *testing.T) {
	m := newMemNews()
	h := newsHandler(m, fixtureFeeds{}, time.Now())
	admin := asUser("admin")

	for name, req := range map[string]*apiv1.CreateNewsSourceRequest{
		"no url":       {Name: "X"},
		"not http":     {Url: "file:///etc/passwd", Name: "X"},
		"no name":      {Url: "https://x.example.com/rss"},
		"bad cron":     {Url: "https://x.example.com/rss", Name: "X", Cron: ref("every hour")},
		"neg weight":   {Url: "https://x.example.com/rss", Name: "X", Weight: ref(int32(-1))},
		"unknown kind": {Url: "https://x.example.com/rss", Name: "X", Kind: apiv1.NewsSourceKind(99)},
		"userinfo":     {Url: "https://user:secret@x.example.com/rss", Name: "X"},
	} {
		_, err := h.CreateNewsSource(admin, connect.NewRequest(req))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), name)
	}

	created, err := h.CreateNewsSource(admin, connect.NewRequest(&apiv1.CreateNewsSourceRequest{Url: " https://x.example.com/rss ", Name: " X "}))
	require.NoError(t, err)
	assert.Equal(t, "https://x.example.com/rss", created.Msg.Url)
	assert.Equal(t, "X", created.Msg.Name)
	assert.True(t, created.Msg.Enabled)
	assert.Equal(t, defaultNewsCron, created.Msg.Cron)
	assert.Equal(t, int32(1), created.Msg.Weight)
	assert.Equal(t, apiv1.NewsSourceKind_NEWS_SOURCE_KIND_RSS, created.Msg.Kind)

	_, err = h.CreateNewsSource(admin, connect.NewRequest(&apiv1.CreateNewsSourceRequest{Url: "https://x.example.com/rss", Name: "Y"}))
	assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))

	_, err = h.UpdateNewsSource(admin, connect.NewRequest(&apiv1.UpdateNewsSourceRequest{Id: created.Msg.Id, Url: ref("https://user@x.example.com/rss")}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestNewsUnconfigured(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := h.ListDocuments(asUser(), connect.NewRequest(&apiv1.ListDocumentsRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

func ref[T any](v T) *T { return &v }
