#!/usr/bin/env bash
# Checks that a web server in front of Hearth (Caddy, nginx, ...) follows the serving contract in
# docs/DEPLOY.md. Needs only bash and curl.
#
#   deploy/verify.sh https://example.com
#   deploy/verify.sh http://localhost:8080
#
# Optional environment:
#   VERIFY_PAGE=/about            a prebuilt page (default /about)
#   VERIFY_ASSET=/assets/x.css    a hashed asset on this origin (default: found in the page HTML)
#   VERIFY_DYNAMIC=/post/1        a server-rendered page that exists (checked twice: render, then file)
#   VERIFY_DYNAMIC_MISSING=/post/nope   a dynamic URL that must be 404
#   VERIFY_404_TEXT="Page not found"  text the branded 404 page must contain (pages/404.html)
#   VERIFY_UNICODE=/letter/%D7%90 a server-rendered URL with a percent-encoded non-ASCII parameter
#                                 (a Hebrew letter), checked like VERIFY_DYNAMIC: render, then file
#   VERIFY_SLASH="/about /post/1" paths (space or comma separated, no trailing slash): static page, parameter route...
#                                 each "<path>/" must answer 301 with Location equal to "<path>"
#   VERIFY_DIR_COLLISION=/photos  a route whose path is also a directory in public/ without an index.html: the
#                                 URL must return the Go page (200 HTML), not a listing and not a 404
#   VERIFY_DIR_COLLISION_TEXT=Photos  optional text the Go page must contain
#   VERIFY_EXTERNAL=1             you are running from outside the private network: /healthz must be hidden
#   CURL=curl                     the curl binary (tests substitute a fake)
#
# Exit code 0 = no FAIL (WARN is advice), 1 = at least one FAIL, 2 = usage error.
set -u

BASE="${1:-}"
if [ -z "$BASE" ]; then
  echo "usage: $0 BASE_URL" >&2
  exit 2
fi
BASE="${BASE%/}"
CURL="${CURL:-curl}"
PAGE="${VERIFY_PAGE:-/about}"
MISSING="/hearth-verify-missing-page"

pass=0 fail=0 warn=0
ok() { printf 'PASS  %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf 'FAIL  %s\n        %s\n' "$1" "$2"; fail=$((fail + 1)); }
note() { printf 'WARN  %s\n        %s\n' "$1" "$2"; warn=$((warn + 1)); }

# fetch METHOD PATH [curl args...]: prints the response headers, then "hearth-status:<code>".
fetch() {
  local method=$1 path=$2
  shift 2
  local args=(-s -o /dev/null -D - -w $'hearth-status:%{http_code}\n' --max-time 10 --path-as-is)
  if [ "$method" = HEAD ]; then args+=(-I); else args+=(-X "$method"); fi
  "$CURL" "${args[@]}" "$@" "$BASE$path" 2>/dev/null | tr -d '\r'
}
body() { "$CURL" -s --max-time 10 --path-as-is "$BASE$1" 2>/dev/null; }
status() { sed -n 's/^hearth-status://p' <<<"$1" | tail -1; }
header() { # header RESPONSE NAME
  awk -v want="$(tr 'A-Z' 'a-z' <<<"$2")" '{ i = index($0, ":"); if (i > 0 && tolower(substr($0, 1, i - 1)) == want) { v = substr($0, i + 1); sub(/^ +/, "", v); print v; exit } }' <<<"$1"
}
path_of() { sed -E 's#^https?://[^/]+##' <<<"$1"; }

expect_status() { # NAME RESPONSE WANTED... (any of the listed codes)
  local name=$1 resp=$2 got
  shift 2
  got=$(status "$resp")
  for w in "$@"; do [ "$got" = "$w" ] && { ok "$name ($got)"; return 0; }; done
  bad "$name" "got status '${got:-none}', want $*"
  return 1
}

# --- the page itself ---
r=$(fetch GET "$PAGE")
if expect_status "GET $PAGE" "$r" 200; then
  case "$(header "$r" content-type)" in
    text/html*) ok "content type is text/html" ;;
    *) bad "content type" "got '$(header "$r" content-type)'" ;;
  esac
  cc=$(header "$r" cache-control)
  if grep -Eqi 'max-age=0|no-cache' <<<"$cc" && grep -Eqi 'must-revalidate|no-cache' <<<"$cc"; then
    ok "HTML revalidates on every use ($cc)"
  else
    bad "HTML Cache-Control" "got '${cc:-none}', want max-age=0, must-revalidate"
  fi
  etag=$(header "$r" etag)
  if [ -n "$etag" ]; then
    ok "ETag present"
    expect_status "conditional GET with If-None-Match" "$(fetch GET "$PAGE" -H "If-None-Match: $etag")" 304
    q=$(fetch GET "$PAGE?utm_source=verify")
    if [ "$(status "$q")" = 200 ] && [ "$(header "$q" etag)" = "$etag" ]; then
      ok "query string does not change the page"
    else
      bad "query string" "$PAGE?utm_source=verify gave $(status "$q") etag '$(header "$q" etag)', want 200 and '$etag'"
    fi
  else
    bad "ETag" "missing on $PAGE"
  fi
  enc=$(header "$(fetch GET "$PAGE" -H 'Accept-Encoding: gzip, br, zstd')" content-encoding)
  if [ -n "$enc" ]; then ok "compressed ($enc)"; else note "compression" "no Content-Encoding for HTML; enable gzip/zstd"; fi
fi
expect_status "HEAD $PAGE" "$(fetch HEAD "$PAGE")" 200

# --- canonical URLs ---
check_redirect() { # NAME PATH WANT_PATH
  local resp loc
  resp=$(fetch GET "$2")
  loc=$(path_of "$(header "$resp" location)")
  if [ "$(status "$resp")" = 301 ] && [ "$loc" = "$3" ]; then
    ok "$1 redirects to $3"
  else
    bad "$1" "$2 gave $(status "$resp") location '${loc:-none}', want 301 to $3"
  fi
}
check_redirect "trailing slash" "$PAGE/" "$PAGE"
check_redirect "/index.html suffix" "$PAGE/index.html" "$PAGE"
check_redirect "root /index.html" "/index.html" "/"

# Every form listed in VERIFY_SLASH: the slash URL redirects to the same path without it.
for p in ${VERIFY_SLASH//,/ }; do
  p="/${p#/}"
  check_redirect "slash form $p/" "$p/" "$p"
done

# --- nothing hidden is reachable ---
for p in /.git/config /.env "$PAGE/.tmp-123456"; do
  resp=$(fetch GET "$p")
  case "$(status "$resp")" in
    404 | 403) ok "dotfile $p is hidden ($(status "$resp"))" ;;
    *) bad "dotfile $p" "got $(status "$resp"), want 404" ;;
  esac
done
for p in /../../etc/passwd /%2e%2e/%2e%2e/etc/passwd; do
  resp=$(fetch GET "$p")
  case "$(status "$resp")" in
    400 | 403 | 404 | 301) ok "traversal $p refused ($(status "$resp"))" ;;
    *) bad "traversal $p" "got $(status "$resp"), want 400/403/404" ;;
  esac
done

# --- unknown paths ---
r=$(fetch GET "$MISSING")
if expect_status "unknown path" "$r" 404; then
  if grep -qi immutable <<<"$(header "$r" cache-control)"; then bad "404 caching" "a 404 must not be marked immutable"; fi
  if [ -n "${VERIFY_404_TEXT:-}" ]; then
    if grep -qF -- "$VERIFY_404_TEXT" <<<"$(body "$MISSING")"; then
      ok "404 body is the branded page"
    else
      bad "404 body" "$MISSING does not contain '$VERIFY_404_TEXT'; pages/404.html is not served for missing paths"
    fi
  fi
fi

# --- hashed assets ---
asset="${VERIFY_ASSET:-}"
if [ -z "$asset" ]; then
  found=$(body "$PAGE" | grep -Eo '(src|href)="[^"]*/assets/[^"]+\.(js|css)"' | head -1 | sed -E 's/^[a-z]+="//; s/"$//')
  case "$found" in
    http://* | https://*) note "assets" "assets are on another origin ($found); verify that CDN separately (docs/DEPLOY.md, S3/CDN)" ;;
    /*) asset=$found ;;
    *) note "assets" "no /assets/ file found in $PAGE; set VERIFY_ASSET" ;;
  esac
fi
if [ -n "$asset" ]; then
  r=$(fetch GET "$asset")
  if expect_status "asset $asset" "$r" 200; then
    cc=$(header "$r" cache-control)
    if grep -qi immutable <<<"$cc" && grep -q 'max-age=31536000' <<<"$cc"; then
      ok "asset is immutable for a year"
    else
      bad "asset Cache-Control" "got '${cc:-none}', want public, max-age=31536000, immutable"
    fi
  fi
fi

# --- server-rendered pages ---
if [ -n "${VERIFY_DYNAMIC:-}" ]; then
  a=$(fetch GET "$VERIFY_DYNAMIC")
  b=$(fetch GET "$VERIFY_DYNAMIC")
  if [ "$(status "$a")" = 200 ] && [ "$(status "$b")" = 200 ] && [ -n "$(header "$a" etag)" ] && [ "$(header "$a" etag)" = "$(header "$b" etag)" ]; then
    ok "dynamic $VERIFY_DYNAMIC renders, then repeats with the same ETag"
  else
    bad "dynamic page" "first $(status "$a") etag '$(header "$a" etag)', second $(status "$b") etag '$(header "$b" etag)'"
  fi
fi
if [ -n "${VERIFY_DYNAMIC_MISSING:-}" ]; then
  expect_status "dynamic missing $VERIFY_DYNAMIC_MISSING" "$(fetch GET "$VERIFY_DYNAMIC_MISSING")" 404
fi

# A route and a public/ directory on one path (route /photos, files in public/photos/ with no index.html).
# The file server must fall through to the Go server instead of listing the directory or answering 404.
if [ -n "${VERIFY_DIR_COLLISION:-}" ]; then
  d=$VERIFY_DIR_COLLISION
  r=$(fetch GET "$d")
  if expect_status "directory collision $d" "$r" 200; then
    page=$(body "$d")
    case "$(header "$r" content-type)" in
      text/html*) ok "directory collision $d answers HTML" ;;
      *) bad "directory collision content type" "$d gave '$(header "$r" content-type)', want text/html" ;;
    esac
    if grep -Eqi 'Index of /|<title>Directory listing|<pre><a href=' <<<"$page"; then
      bad "directory collision listing" "$d returns a directory listing; the Go page must answer (docs/DEPLOY.md)"
    elif [ -n "${VERIFY_DIR_COLLISION_TEXT:-}" ] && ! grep -qF -- "$VERIFY_DIR_COLLISION_TEXT" <<<"$page"; then
      bad "directory collision body" "$d does not contain '$VERIFY_DIR_COLLISION_TEXT'; the Go page is not what answered"
    else
      ok "directory collision $d is the Go page, not a listing"
    fi
  fi
fi

# A non-ASCII parameter (voters: /letter/{letter} with a Hebrew letter). The request is percent-encoded;
# the cache file name is the UTF-8 letter, so the server must decode before it looks for the file.
if [ -n "${VERIFY_UNICODE:-}" ]; then
  a=$(fetch GET "$VERIFY_UNICODE")
  b=$(fetch GET "$VERIFY_UNICODE")
  if [ "$(status "$a")" = 200 ] && [ "$(status "$b")" = 200 ] && [ -n "$(header "$a" etag)" ] && [ "$(header "$a" etag)" = "$(header "$b" etag)" ]; then
    ok "non-ASCII path $VERIFY_UNICODE renders, then repeats with the same ETag"
  else
    bad "non-ASCII path" "first $(status "$a") etag '$(header "$a" etag)', second $(status "$b") etag '$(header "$b" etag)'"
  fi
fi

# --- platform endpoints and methods ---
hz=$(status "$(fetch GET /healthz)")
if [ "${VERIFY_EXTERNAL:-}" = 1 ]; then
  case "$hz" in
    404 | 403) ok "/healthz hidden from the outside ($hz)" ;;
    *) bad "/healthz" "got $hz from outside, want 404/403" ;;
  esac
else
  note "/healthz" "got ${hz:-none} from here; run once from a public address with VERIFY_EXTERNAL=1 to enforce"
fi
case "$(status "$(fetch POST "$PAGE")")" in
  405 | 404 | 403 | 400) ok "POST $PAGE is not served" ;;
  *) note "POST $PAGE" "answered $(status "$(fetch POST "$PAGE")"); consider rejecting non-GET/HEAD for pages" ;;
esac

printf '\n%d passed, %d failed, %d warnings\n' "$pass" "$fail" "$warn"
[ "$fail" -eq 0 ]
