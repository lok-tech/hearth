#!/usr/bin/env bash
# A pretend server for testing verify.sh without a network: stands in for curl.
# FAKE_BROKEN=<mode> makes one behaviour wrong: slash | index | dotfile | traversal | asset | html-cache | etag | query | dynamic | 404-immutable | no-404-body | unicode
set -u
method=GET url="" inm="" body=1
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
  case "${args[$i]}" in
    -X) method=${args[$((i + 1))]}; i=$((i + 1)) ;;
    -I) method=HEAD ;;
    -H) h=${args[$((i + 1))]}; i=$((i + 1)); case "$h" in If-None-Match:*) inm=${h#If-None-Match: } ;; esac ;;
    -o) body=0; i=$((i + 1)) ;;
    -w | --max-time) i=$((i + 1)) ;;
    -*) ;;
    *) url=${args[$i]} ;;
  esac
done
path=${url#*://}; path=/${path#*/}
bare=${path%%\?*}
broken=${FAKE_BROKEN:-}

out() { # STATUS [header...]
  local code=$1
  shift
  if [ "$body" = 0 ]; then
    printf 'HTTP/1.1 %s X\r\n' "$code"
    for h in "$@"; do printf '%s\r\n' "$h"; done
    printf '\r\n'
    printf 'hearth-status:%s\n' "$code"
  fi
}
page_headers=('Content-Type: text/html; charset=utf-8' 'Cache-Control: public, max-age=0, must-revalidate' 'ETag: "abc123"' 'Content-Encoding: gzip')
[ "$broken" = html-cache ] && page_headers[1]='Cache-Control: public, max-age=3600'
[ "$broken" = etag ] && page_headers[2]='X-Nothing: 1'

case "$bare" in
  /about)
    if [ "$method" = POST ]; then out 405; exit 0; fi
    [ "$broken" = query ] && [ "$bare" != "$path" ] && { out 200 "${page_headers[0]}" "${page_headers[1]}" 'ETag: "different"'; exit 0; }
    if [ -n "$inm" ] && [ "$inm" = '"abc123"' ]; then out 304; exit 0; fi
    if [ "$body" = 1 ]; then echo '<link rel="stylesheet" href="/assets/entry-abc.css"><p>about</p>'; exit 0; fi
    out 200 "${page_headers[@]}" ;;
  /about/)
    [ "$broken" = slash ] && { out 200 "${page_headers[@]}"; exit 0; }
    out 301 'Location: http://fake.test/about' ;;
  /about/index.html)
    [ "$broken" = index ] && { out 200 "${page_headers[@]}"; exit 0; }
    out 301 'Location: /about' ;;
  /index.html) out 301 'Location: /' ;;
  /.git/config | /.env | /about/.tmp-123456)
    [ "$broken" = dotfile ] && { out 200 'Content-Type: text/plain'; exit 0; }
    out 404 ;;
  /../../etc/passwd | /%2e%2e/%2e%2e/etc/passwd)
    [ "$broken" = traversal ] && { out 200 'Content-Type: text/plain'; exit 0; }
    out 400 ;;
  /assets/entry-abc.css)
    cc='Cache-Control: public, max-age=31536000, immutable'
    [ "$broken" = asset ] && cc='Cache-Control: public, max-age=60'
    out 200 'Content-Type: text/css' "$cc" ;;
  /post/1)
    et='ETag: "p1"'
    [ "$broken" = dynamic ] && et="ETag: \"$RANDOM\""
    out 200 "${page_headers[0]}" "$et" ;;
  /post/nope) out 404 ;;
  /letter/%D7%90)
    et='ETag: "letter-alef"'
    [ "$broken" = unicode ] && { out 404; exit 0; }
    out 200 "${page_headers[0]}" "$et" ;;
  /healthz) out 200 ;;
  /hearth-verify-missing-page)
    if [ "$body" = 1 ]; then
      if [ "$broken" = no-404-body ]; then echo '404 Not Found'; else echo '<h1>Page not found</h1>'; fi
      exit 0
    fi
    [ "$broken" = 404-immutable ] && { out 404 'Cache-Control: public, max-age=31536000, immutable'; exit 0; }
    out 404 ;;
  *) out 404 ;;
esac
