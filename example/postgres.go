package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Postgres is the real Store. The library itself has no database driver; the driver is imported
// in main.go, and everything here is plain database/sql with parameterised queries only.
type Postgres struct{ DB *sql.DB }

func (p Postgres) Post(ctx context.Context, slug string) (Post, error) {
	var (
		post    Post
		deleted sql.NullTime
	)
	err := p.DB.QueryRowContext(ctx,
		`select slug, title, body, updated_at, deleted_at from posts where slug = $1`, slug,
	).Scan(&post.Slug, &post.Title, &post.Body, &post.UpdatedAt, &deleted)
	switch {
	case err == nil:
		if deleted.Valid {
			return Post{}, ErrGone
		}
		return post, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Post{}, err
	}
	var to string
	err = p.DB.QueryRowContext(ctx, `select new_slug from slug_redirects where old_slug = $1`, slug).Scan(&to)
	switch {
	case err == nil:
		return Post{}, &MovedError{To: to}
	case errors.Is(err, sql.ErrNoRows):
		return Post{}, ErrNotFound
	default:
		return Post{}, err
	}
}

func (p Postgres) Slugs(ctx context.Context) ([]string, error) {
	rows, err := p.DB.QueryContext(ctx, `select slug from posts where deleted_at is null order by slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Version changes whenever a post is added, edited, deleted or renamed. A cheap aggregate is
// enough for a few thousand rows; at larger scale use a trigger-maintained counter instead.
func (p Postgres) Version(ctx context.Context) (string, error) {
	var (
		latest sql.NullTime
		n      int
		r      int
	)
	err := p.DB.QueryRowContext(ctx,
		`select max(updated_at), count(*), (select count(*) from slug_redirects) from posts`,
	).Scan(&latest, &n, &r)
	if err != nil {
		return "", err
	}
	t := time.Time{}
	if latest.Valid {
		t = latest.Time
	}
	return versionOf(t, n+r), nil
}
