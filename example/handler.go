package main

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/orelhadad/hearth/pkg/hearth"
)

// A slug is untrusted input: it comes straight from the URL. Reject anything that is not a plain
// slug before it reaches the database or becomes a file name in the cache.
var slugRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func validSlug(s string) bool { return len(s) <= 80 && slugRE.MatchString(s) }

// postHandler serves post/{slug}. The page template is pages/post/[slug].html; this function
// only supplies data and decides between 200, 301, 404 and 410.
func postHandler(store Store) hearth.Handler {
	return func(r *http.Request, params map[string]string) (any, hearth.Meta, error) {
		slug := params["slug"]
		if !validSlug(slug) {
			return nil, hearth.Meta{}, hearth.ErrNotFound
		}
		post, err := store.Post(r.Context(), slug)
		var moved *MovedError
		switch {
		case err == nil:
			return post, hearth.Meta{}, nil
		case errors.Is(err, ErrNotFound):
			return nil, hearth.Meta{}, hearth.ErrNotFound
		case errors.Is(err, ErrGone):
			return nil, hearth.Meta{}, hearth.ErrGone
		case errors.As(err, &moved):
			// The target comes from the database, so it is validated like any other input:
			// only a plain slug, only a path on this site (no open redirect).
			if !validSlug(moved.To) {
				return nil, hearth.Meta{}, errors.New("redirect target is not a valid slug")
			}
			return nil, hearth.Meta{}, hearth.Redirect(http.StatusMovedPermanently, languagePrefix(r.URL.Path)+"/post/"+moved.To)
		default:
			return nil, hearth.Meta{}, err // a failed lookup is a 500 and is never cached
		}
	}
}

// languagePrefix returns "/he" for "/he/post/x" and "" for "/post/x", so a redirect stays in
// the visitor's language.
func languagePrefix(path string) string {
	if i := strings.Index(path, "/post/"); i > 0 {
		return path[:i]
	}
	return ""
}
