//go:build integration

package postgres

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/adapter/rss"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/analytics"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recordedFeed = "../../adapter/rss/testdata/rss2.xml"

func createTestSource(t *testing.T, s *NewsStore, name, url string) *entity.NewsSource {
	t.Helper()
	src, err := s.CreateNewsSource(context.Background(), &entity.NewsSource{
		Kind: entity.NewsSourceRSS, URL: url, Name: name, Enabled: true, Cron: "*/30 * * * *", Weight: 1,
	})
	require.NoError(t, err)
	return src
}

func ingest(t *testing.T, s *NewsStore, feeds analytics.FeedReader) analytics.NewsReport {
	t.Helper()
	h := analytics.NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithNews(s, feeds)
	report, err := h.IngestNews(context.Background())
	require.NoError(t, err)
	return report
}

// fileFeeds answers a URL from a recorded file, or with an error.
type fileFeeds map[string]string

func (f fileFeeds) Fetch(_ context.Context, url string) ([]entity.FeedItem, error) {
	path, ok := f[url]
	if !ok {
		return nil, fmt.Errorf("feed answered 503 Service Unavailable: %s", url)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return rss.Parse(data)
}

func TestNewsIngestTwiceStoresOnce(t *testing.T) {
	pool := getTestPool(t)
	s := NewNewsStore(pool)
	src := createTestSource(t, s, "Example Wire", "https://news.example.com/rss/")
	feeds := fileFeeds{src.URL: recordedFeed}

	first := ingest(t, s, feeds)
	assert.Equal(t, 3, first.Inserted, "four items, one undated")

	// Make the source due again without waiting for its cron.
	_, err := pool.Exec(context.Background(), `UPDATE sources SET last_fetched_at = NULL`)
	require.NoError(t, err)

	second := ingest(t, s, feeds)
	assert.Equal(t, 1, second.Fetched)
	assert.Equal(t, 0, second.Inserted)

	var rows int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM documents`).Scan(&rows))
	assert.Equal(t, 3, rows)
}

func TestNewsListDocumentsNewestPublishedFirst(t *testing.T) {
	pool := getTestPool(t)
	s := NewNewsStore(pool)
	src := createTestSource(t, s, "Example Wire", "https://news.example.com/rss/")
	ingest(t, s, fileFeeds{src.URL: recordedFeed})
	ctx := context.Background()

	docs, next, err := s.ListDocuments(ctx, analytics.ListDocumentsOpts{})
	require.NoError(t, err)
	assert.Empty(t, next)
	require.Len(t, docs, 3)
	want := []time.Time{
		time.Date(2026, 11, 19, 13, 58, 12, 0, time.UTC),
		time.Date(2026, 11, 19, 11, 30, 0, 0, time.UTC),
		time.Date(2026, 11, 18, 22, 15, 0, 0, time.UTC),
	}
	for i, d := range docs {
		assert.Equal(t, want[i], d.PublishedAt.UTC(), "document %d", i)
		assert.Equal(t, "Example Wire", d.SourceName)
	}

	// Pages follow the same order without repeating or skipping.
	page1, token, err := s.ListDocuments(ctx, analytics.ListDocumentsOpts{PageSize: 2})
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.NotEmpty(t, token)
	page2, token, err := s.ListDocuments(ctx, analytics.ListDocumentsOpts{PageSize: 2, PageToken: token})
	require.NoError(t, err)
	assert.Empty(t, token)
	require.Len(t, page2, 1)
	assert.Equal(t, docs[2].ID, page2[0].ID)

	since, _, err := s.ListDocuments(ctx, analytics.ListDocumentsOpts{Since: want[1]})
	require.NoError(t, err)
	assert.Len(t, since, 2, "since is inclusive")

	other := createTestSource(t, s, "Other", "https://other.example.com/rss")
	none, _, err := s.ListDocuments(ctx, analytics.ListDocumentsOpts{SourceID: other.ID})
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestNewsFailingSourceIsRecordedAndOthersRun(t *testing.T) {
	pool := getTestPool(t)
	s := NewNewsStore(pool)
	broken := createTestSource(t, s, "A Broken", "https://broken.example.com/rss")
	good := createTestSource(t, s, "B Good", "https://news.example.com/rss/")

	report := ingest(t, s, fileFeeds{good.URL: recordedFeed})
	assert.Equal(t, 1, report.Failed)
	assert.Equal(t, 1, report.Fetched)
	assert.Equal(t, 3, report.Inserted)

	sources, err := s.ListNewsSources(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 2)
	b, g := sources[0], sources[1]
	assert.Equal(t, broken.ID, b.ID)
	assert.Contains(t, b.LastError, "503")
	require.NotNil(t, b.LastErrorAt)
	require.NotNil(t, b.LastFetchedAt)
	assert.Nil(t, b.LastSuccessAt)
	assert.Empty(t, g.LastError)
	require.NotNil(t, g.LastSuccessAt)
}

func TestNewsDocumentRowHoldsOnlyAHeadline(t *testing.T) {
	pool := getTestPool(t)
	s := NewNewsStore(pool)
	src := createTestSource(t, s, "Example Wire", "https://news.example.com/rss/")
	ctx := context.Background()

	_, err := s.InsertDocuments(ctx, []*entity.NewsDocument{{
		SourceID: src.ID, URL: "https://news.example.com/a", Title: "t",
		Snippet:     strings.Repeat("x", entity.MaxDocumentSnippetRunes+1),
		PublishedAt: time.Now(), FetchedAt: time.Now(), URLHash: "h1",
	}})
	assert.ErrorIs(t, err, store.ErrConstraint)

	var cols []string
	rows, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'documents' ORDER BY column_name`)
	require.NoError(t, err)
	for rows.Next() {
		var c string
		require.NoError(t, rows.Scan(&c))
		cols = append(cols, c)
	}
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, []string{"id", "source_id", "url", "title", "snippet", "published_at", "fetched_at", "url_hash"}, cols)
}

func TestNewsSourceMutations(t *testing.T) {
	pool := getTestPool(t)
	s := NewNewsStore(pool)
	ctx := context.Background()
	src := createTestSource(t, s, "Example Wire", "https://news.example.com/rss/")

	_, err := s.CreateNewsSource(ctx, &entity.NewsSource{
		Kind: entity.NewsSourceRSS, URL: src.URL, Name: "Dup", Enabled: true, Cron: src.Cron,
	})
	assert.ErrorIs(t, err, store.ErrAlreadyExists)

	off, name := false, "Renamed"
	updated, err := s.UpdateNewsSource(ctx, src.ID, analytics.NewsSourcePatch{Enabled: &off, Name: &name})
	require.NoError(t, err)
	assert.False(t, updated.Enabled)
	assert.Equal(t, "Renamed", updated.Name)
	assert.Equal(t, src.URL, updated.URL, "fields not in the patch are kept")

	ingest(t, s, fileFeeds{src.URL: recordedFeed}) // disabled: reads nothing
	docs, _, err := s.ListDocuments(ctx, analytics.ListDocumentsOpts{})
	require.NoError(t, err)
	assert.Empty(t, docs)

	on := true
	_, err = s.UpdateNewsSource(ctx, src.ID, analytics.NewsSourcePatch{Enabled: &on})
	require.NoError(t, err)
	ingest(t, s, fileFeeds{src.URL: recordedFeed})

	require.NoError(t, s.DeleteNewsSource(ctx, src.ID))
	docs, _, err = s.ListDocuments(ctx, analytics.ListDocumentsOpts{})
	require.NoError(t, err)
	assert.Empty(t, docs, "a deleted source takes its documents")
	assert.ErrorIs(t, s.DeleteNewsSource(ctx, src.ID), store.ErrNotFound)
}
