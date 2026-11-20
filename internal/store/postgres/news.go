package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/analytics"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewsStore implements analytics.NewsStore using PostgreSQL.
type NewsStore struct {
	pool *pgxpool.Pool
}

var _ analytics.NewsStore = (*NewsStore)(nil)

func NewNewsStore(pool *pgxpool.Pool) *NewsStore {
	return &NewsStore{pool: pool}
}

const sourceColumns = `id, kind, url, name, enabled, cron, weight,
	last_fetched_at, last_success_at, COALESCE(last_error, ''), last_error_at, created_at, updated_at`

func scanSource(row pgx.Row) (*entity.NewsSource, error) {
	var s entity.NewsSource
	var kind string
	if err := row.Scan(&s.ID, &kind, &s.URL, &s.Name, &s.Enabled, &s.Cron, &s.Weight,
		&s.LastFetchedAt, &s.LastSuccessAt, &s.LastError, &s.LastErrorAt, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	s.Kind = entity.NewsSourceKind(kind)
	return &s, nil
}

// ListNewsSources returns every source, by name.
func (s *NewsStore) ListNewsSources(ctx context.Context) ([]*entity.NewsSource, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sourceColumns+` FROM sources ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("failed to list news sources: %w", err)
	}
	defer rows.Close()
	var out []*entity.NewsSource
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan news source: %w", err)
		}
		out = append(out, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate news sources: %w", err)
	}
	return out, nil
}

func (s *NewsStore) CreateNewsSource(ctx context.Context, in *entity.NewsSource) (*entity.NewsSource, error) {
	if in == nil {
		return nil, fmt.Errorf("%w: source is required", store.ErrInvalidArgument)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate id: %w", err)
	}
	src, err := scanSource(s.pool.QueryRow(ctx, `
		INSERT INTO sources (id, kind, url, name, enabled, cron, weight, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		RETURNING `+sourceColumns,
		id, string(in.Kind), in.URL, in.Name, in.Enabled, in.Cron, in.Weight))
	if err != nil {
		return nil, sourceWriteError(err, in.URL)
	}
	return src, nil
}

func (s *NewsStore) UpdateNewsSource(ctx context.Context, id string, p analytics.NewsSourcePatch) (*entity.NewsSource, error) {
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid source ID format", store.ErrInvalidArgument)
	}
	sets := []string{"updated_at = NOW()"}
	args := []any{id}
	set := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if p.URL != nil {
		set("url", *p.URL)
	}
	if p.Name != nil {
		set("name", *p.Name)
	}
	if p.Enabled != nil {
		set("enabled", *p.Enabled)
	}
	if p.Cron != nil {
		set("cron", *p.Cron)
	}
	if p.Weight != nil {
		set("weight", *p.Weight)
	}
	src, err := scanSource(s.pool.QueryRow(ctx,
		`UPDATE sources SET `+strings.Join(sets, ", ")+` WHERE id = $1 RETURNING `+sourceColumns, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: news source %s", store.ErrNotFound, id)
		}
		url := ""
		if p.URL != nil {
			url = *p.URL
		}
		return nil, sourceWriteError(err, url)
	}
	return src, nil
}

func sourceWriteError(err error, url string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: a news source with url %s", store.ErrAlreadyExists, url)
	}
	if isConstraintError(err) {
		return fmt.Errorf("%w: %v", store.ErrConstraint, err)
	}
	return fmt.Errorf("failed to write news source: %w", err)
}

// DeleteNewsSource removes the source; its documents go with it (ON DELETE
// CASCADE).
func (s *NewsStore) DeleteNewsSource(ctx context.Context, id string) error {
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid source ID format", store.ErrInvalidArgument)
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM sources WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete news source: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: news source %s", store.ErrNotFound, id)
	}
	return nil
}

// RecordSourceFetch stamps an attempt. A success leaves last_error in place,
// so the last failure stays readable beside the success that followed it.
func (s *NewsStore) RecordSourceFetch(ctx context.Context, id string, at time.Time, fetchErr string) error {
	var err error
	if fetchErr == "" {
		_, err = s.pool.Exec(ctx, `
			UPDATE sources SET last_fetched_at = $2, last_success_at = $2 WHERE id = $1`, id, at)
	} else {
		_, err = s.pool.Exec(ctx, `
			UPDATE sources SET last_fetched_at = $2, last_error = $3, last_error_at = $2 WHERE id = $1`, id, at, fetchErr)
	}
	if err != nil {
		return fmt.Errorf("failed to record news source fetch: %w", err)
	}
	return nil
}

// InsertDocuments stores the documents whose url_hash is new, in one
// statement. ON CONFLICT DO NOTHING is the dedup: a feed read again, or the
// same article in a second feed, adds no row.
func (s *NewsStore) InsertDocuments(ctx context.Context, docs []*entity.NewsDocument) (int, error) {
	if len(docs) == 0 {
		return 0, nil
	}
	n := len(docs)
	ids, sourceIDs := make([]string, n), make([]string, n)
	urls, titles, snippets, hashes := make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	published, fetched := make([]time.Time, n), make([]time.Time, n)
	for i, d := range docs {
		id, err := uuid.NewV7()
		if err != nil {
			return 0, fmt.Errorf("generate id: %w", err)
		}
		ids[i], sourceIDs[i] = id.String(), d.SourceID
		urls[i], titles[i], snippets[i], hashes[i] = d.URL, d.Title, d.Snippet, d.URLHash
		published[i], fetched[i] = d.PublishedAt, d.FetchedAt
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO documents (id, source_id, url, title, snippet, published_at, fetched_at, url_hash)
		SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::text[], $5::text[],
			$6::timestamptz[], $7::timestamptz[], $8::text[])
		ON CONFLICT (url_hash) DO NOTHING`,
		ids, sourceIDs, urls, titles, snippets, published, fetched, hashes)
	if err != nil {
		if isConstraintError(err) {
			return 0, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return 0, fmt.Errorf("failed to insert documents: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// documentPageToken is the keyset position after the last document of a page:
// documents are ordered by (published_at, id) descending.
func documentPageToken(d *entity.NewsDocument) string {
	return base64.StdEncoding.EncodeToString([]byte(d.PublishedAt.UTC().Format(time.RFC3339Nano) + "|" + d.ID))
}

// ListDocuments pages documents newest published_at first, with the name of
// the source each came from.
func (s *NewsStore) ListDocuments(ctx context.Context, opts analytics.ListDocumentsOpts) ([]*entity.NewsDocument, string, error) {
	limit := pageLimit(opts.PageSize)

	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if opts.SourceID != "" {
		if !isValidUUID(opts.SourceID) {
			return nil, "", fmt.Errorf("%w: invalid source ID format", store.ErrInvalidArgument)
		}
		where = append(where, "d.source_id = "+arg(opts.SourceID))
	}
	if !opts.Since.IsZero() {
		where = append(where, "d.published_at >= "+arg(opts.Since))
	}
	if opts.PageToken != "" {
		// Same token shape as the ledger's: time|uuid.
		at, id, err := parseLedgerPageToken(opts.PageToken)
		if err != nil {
			return nil, "", err
		}
		where = append(where, fmt.Sprintf("(d.published_at, d.id) < (%s, %s)", arg(at), arg(id)))
	}
	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}
	query := fmt.Sprintf(`
		SELECT d.id, d.source_id, s.name, d.url, d.title, d.snippet, d.published_at, d.fetched_at, d.url_hash
		FROM documents d
		JOIN sources s ON s.id = d.source_id
		%s
		ORDER BY d.published_at DESC, d.id DESC
		LIMIT %s`, whereClause, arg(limit+1))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list documents: %w", err)
	}
	defer rows.Close()
	docs := make([]*entity.NewsDocument, 0, limit)
	for rows.Next() {
		var d entity.NewsDocument
		if err := rows.Scan(&d.ID, &d.SourceID, &d.SourceName, &d.URL, &d.Title, &d.Snippet,
			&d.PublishedAt, &d.FetchedAt, &d.URLHash); err != nil {
			return nil, "", fmt.Errorf("failed to scan document: %w", err)
		}
		docs = append(docs, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate documents: %w", err)
	}
	var next string
	if len(docs) > limit {
		docs = docs[:limit]
		next = documentPageToken(docs[limit-1])
	}
	return docs, next, nil
}
