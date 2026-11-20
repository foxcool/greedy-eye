package rss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return data
}

func TestParseRSS2(t *testing.T) {
	items, err := Parse(fixture(t, "rss2.xml"))
	require.NoError(t, err)
	require.Len(t, items, 4)

	first := items[0]
	assert.Equal(t, "Bitcoin Holds Above $90K as Chipmaker Earnings Lift Risk Assets", first.Title)
	assert.True(t, strings.HasPrefix(first.Link, "https://news.example.com/markets/2026/11/19/bitcoin-holds-above-90k?"))
	assert.Equal(t, "Bitcoin traded near $91,200 on Thursday after a chipmaker's results & guidance lifted equities.", first.Summary)
	assert.Equal(t, time.Date(2026, 11, 19, 13, 58, 12, 0, time.UTC), first.PublishedAt.UTC())

	// "GMT" zone name rather than a numeric offset.
	assert.Equal(t, time.Date(2026, 11, 19, 11, 30, 0, 0, time.UTC), items[1].PublishedAt.UTC())
	// No pubDate, only dc:date.
	assert.Equal(t, time.Date(2026, 11, 18, 22, 15, 0, 0, time.UTC), items[2].PublishedAt.UTC())
	// No date at all: reported as zero, left to the caller to skip.
	assert.True(t, items[3].PublishedAt.IsZero())

	for _, it := range items {
		assert.NotContains(t, it.Summary, "FULL ARTICLE", "content:encoded is the article, never the snippet")
	}
}

func TestParseAtom(t *testing.T) {
	items, err := Parse(fixture(t, "atom.xml"))
	require.NoError(t, err)
	require.Len(t, items, 2)

	first := items[0]
	assert.Equal(t, "Layer-2 Fees & What Moved Them", first.Title)
	assert.Equal(t, "https://research.example.org/posts/l2-fees", first.Link, "the text/html alternate, not the JSON one")
	assert.Equal(t, "Fees on rollups fell after the upgrade.", first.Summary)
	assert.Equal(t, time.Date(2026, 11, 19, 7, 30, 0, 0, time.UTC), first.PublishedAt.UTC(), "published wins over updated")

	second := items[1]
	assert.Equal(t, "https://research.example.org/posts/updated-only", second.Link)
	assert.Equal(t, time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC), second.PublishedAt.UTC())
	assert.Equal(t, "Only content, no summary: the snippet comes from here, cut short.", second.Summary)
}

func TestParseRefusesWhatIsNotAFeed(t *testing.T) {
	_, err := Parse(fixture(t, "not-a-feed.html"))
	require.Error(t, err)

	_, err = Parse(fixture(t, "cp1251.xml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "windows-1251")
}

func TestFetch(t *testing.T) {
	body := fixture(t, "rss2.xml")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write(body)
		case "/huge":
			_, _ = w.Write([]byte(strings.Repeat(" ", 2048)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient(Config{MaxBytes: 1024 * 1024})
	items, err := c.Fetch(context.Background(), srv.URL+"/feed")
	require.NoError(t, err)
	assert.Len(t, items, 4)

	_, err = c.Fetch(context.Background(), srv.URL+"/gone")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")

	small := NewClient(Config{MaxBytes: 1024})
	_, err = small.Fetch(context.Background(), srv.URL+"/huge")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "larger than")
}

// TestFetchErrorDoesNotQuoteTheURL: the error is stored on the source and
// shown to every reader, so a token in a feed URL must not ride along in it.
func TestFetchErrorDoesNotQuoteTheURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close() // connection refused from here on

	_, err := NewClient(Config{}).Fetch(context.Background(), addr+"/feed?token=QUERYSECRET")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "QUERYSECRET")

	_, err = NewClient(Config{}).Fetch(context.Background(), "https://example.com/\x7f?token=QUERYSECRET")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "QUERYSECRET")
}
