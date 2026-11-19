#!/usr/bin/env bash
# Tests verify.sh itself against a fake server: a correct one must pass, each broken mode must
# fail the matching check. Run: deploy/test/verify-selftest.sh
set -u
here=$(cd "$(dirname "$0")" && pwd)
verify="$here/../verify.sh"
export CURL="$here/fake-curl.sh"
export VERIFY_DYNAMIC=/post/1 VERIFY_DYNAMIC_MISSING=/post/nope
export VERIFY_404_TEXT='Page not found' VERIFY_UNICODE=/letter/%D7%90
export VERIFY_SLASH='/about /post/1' VERIFY_DIR_COLLISION=/photos VERIFY_DIR_COLLISION_TEXT='Photos'
failures=0

out=$("$verify" http://fake.test 2>&1)
code=$?
if [ "$code" -ne 0 ]; then
  echo "FAIL: a correct server was rejected"; echo "$out" | grep -A1 '^FAIL'; failures=$((failures + 1))
else
  echo "ok: correct server passes ($(tail -1 <<<"$out"))"
fi

# mode -> text that must appear on a FAIL line
for pair in "slash:trailing slash" "index:/index.html suffix" "dotfile:dotfile" "traversal:traversal" \
  "asset:asset Cache-Control" "html-cache:HTML Cache-Control" "etag:ETag" "query:query string" \
  "dynamic:dynamic page" "404-immutable:404 caching" "no-404-body:404 body" "unicode:non-ASCII path" \
  "slash-param:slash form /post/1" "slash-loc:slash form /post/1" "dir-404:directory collision" "dir-listing:directory collision"; do
  mode=${pair%%:*} want=${pair#*:}
  out=$(FAKE_BROKEN=$mode "$verify" http://fake.test 2>&1)
  code=$?
  if [ "$code" -eq 1 ] && grep -q "^FAIL  .*$want" <<<"$out"; then
    echo "ok: $mode is caught"
  else
    echo "FAIL: $mode not caught as '$want' (exit $code)"; failures=$((failures + 1))
  fi
done

if "$verify" >/dev/null 2>&1; then echo "FAIL: missing URL must be a usage error"; failures=$((failures + 1)); else echo "ok: usage error"; fi
[ "$failures" -eq 0 ] && echo "all verify.sh self-tests passed"
exit "$failures"
