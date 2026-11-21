package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/service/analytics"
	"github.com/foxcool/greedy-eye/internal/spend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeNews struct {
	report  analytics.NewsReport
	err     error
	calls   int
	classes []spend.Class
}

func (f *fakeNews) IngestNews(ctx context.Context) (analytics.NewsReport, error) {
	f.calls++
	f.classes = append(f.classes, spend.ClassFromContext(ctx))
	return f.report, f.err
}

func TestIngestNews_RunsInBackgroundClass(t *testing.T) {
	news := &fakeNews{report: analytics.NewsReport{Enabled: 2, Due: 2, Fetched: 1, Failed: 1,
		Failures: []analytics.NewsFailure{{SourceID: "s1", Name: "Broken", Error: "feed answered 503"}}}}
	s := newTestScheduler(t, Config{}, &mockRuleStore{}, &fakeExecutor{}, &fakeFetcher{}).WithNewsIngester(news)

	assert.NotPanics(t, s.ingestNews)
	require.Equal(t, 1, news.calls)
	assert.Equal(t, spend.ClassBackground, news.classes[0])

	news.err = errors.New("db down")
	assert.NotPanics(t, s.ingestNews)
}

// TestStart_NewsNeedsBothCronAndIngester: a deployment that does not serve
// analytics has no news to ingest, so the cron alone schedules nothing.
func TestStart_NewsNeedsBothCronAndIngester(t *testing.T) {
	store := &mockRuleStore{}
	expectRulesPage(store, "", nil, "")
	s := newTestScheduler(t, Config{NewsCron: "*/5 * * * *"}, store, &fakeExecutor{}, &fakeFetcher{})
	require.NoError(t, s.Start())
	assert.Len(t, s.cron.Entries(), 1, "rule reload only: no ingester wired")

	store2 := &mockRuleStore{}
	expectRulesPage(store2, "", nil, "")
	s2 := newTestScheduler(t, Config{NewsCron: "*/5 * * * *"}, store2, &fakeExecutor{}, &fakeFetcher{}).
		WithNewsIngester(&fakeNews{})
	require.NoError(t, s2.Start())
	assert.Len(t, s2.cron.Entries(), 2)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.Stop(ctx)
	s2.Stop(ctx)
}

func TestNew_InvalidNewsCron(t *testing.T) {
	_, err := New(Config{NewsCron: "not a cron"}, &mockRuleStore{}, &fakeExecutor{}, &fakeFetcher{}, nil, testLogger())
	assert.Error(t, err)
}
