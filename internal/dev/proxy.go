package dev

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"

	"github.com/lok-tech/hearth/internal/config"
)

// withProxy sends requests under a dev.proxy prefix to its backend and everything else to next.
// It runs before page routes, the Vite fallback and live reload, longest prefix first, and
// matches whole path segments: "/api" takes "/api" and "/api/x" but not "/apiary".
// The method, path, query, body and headers pass through. The request to the backend carries
// the backend's own Host (virtual hosts and TLS names keep working) and X-Forwarded-For, -Host
// and -Proto describe the original request. Redirects and cookies from the backend are not
// rewritten. With no entries it returns next unchanged.
func withProxy(proxy map[string]string, next http.Handler) (http.Handler, error) {
	if len(proxy) == 0 {
		return next, nil
	}
	if err := config.ValidateProxy(proxy); err != nil {
		return nil, err
	}
	type entry struct {
		prefix string
		h      http.Handler
	}
	var entries []entry
	for prefix, raw := range proxy {
		if reloadPath == prefix || strings.HasPrefix(reloadPath, prefix+"/") || strings.HasPrefix(prefix, reloadPath+"/") {
			return nil, fmt.Errorf("hearth: dev.proxy prefix %q is used by live reload", prefix)
		}
		target, err := parseProxyTarget(raw, "dev.proxy["+prefix+"]")
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{prefix, backendProxy(target)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i].prefix) != len(entries[j].prefix) {
			return len(entries[i].prefix) > len(entries[j].prefix)
		}
		return entries[i].prefix < entries[j].prefix
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		for _, e := range entries {
			if p == e.prefix || strings.HasPrefix(p, e.prefix+"/") {
				e.h.ServeHTTP(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	}), nil
}

func backendProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target) // joins the target's path, keeps path and query, sets Host to the target
			pr.SetXForwarded()
		},
		Transport: proxyTransport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "hearth: dev.proxy backend "+target.Host+" unreachable: "+err.Error(), http.StatusBadGateway)
		},
	}
}
