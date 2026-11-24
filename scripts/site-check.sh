#!/usr/bin/env bash
# shellcheck disable=SC2016,SC2034  # checks are eval'd strings that use variables set beside them
# Regression check of the LIVE site's security posture: what internet.nl scores (TLS, headers,
# security.txt, DNSSEC, CAA, IPv6, mail anti-spoofing for a domain that sends no mail) plus the
# Mozilla HTTP Observatory grade. Runs after every website deploy and weekly in CI (site-check.yml);
# exits non-zero on any miss.
#   scripts/site-check.sh             # gunkly.app (or from the website repo: npm run live-check)
#   SITE=example.org scripts/site-check.sh
#   NO_EXTERNAL=1 scripts/site-check.sh  # skip the Mozilla Observatory API call
# Needs curl, openssl and jq. DNS answers come from public DNS-over-HTTPS resolvers.
set -uo pipefail

SITE=${SITE:-gunkly.app}
fails=0
pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1"; fails=$((fails + 1)); }
check() { if eval "$2" >/dev/null 2>&1; then pass "$1"; else fail "$1"; fi; }
section() { printf '\n%s\n' "$1"; }
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT

tls() { # tls <version flag> -> prints the negotiated protocol or nothing
  echo | timeout 10 openssl s_client -connect "$SITE:443" -servername "$SITE" "$1" -cipher 'DEFAULT@SECLEVEL=0' 2>/dev/null |
    awk '/^New, TLSv/ {sub(",", "", $2); print $2; exit}'
}
doh() { # doh <name> <type> -> JSON answer from Google's resolver (AD = DNSSEC validated)
  curl -fsS --max-time 15 "https://dns.google/resolve?name=$1&type=$2"
}

section "TLS"
check "TLS 1.3 accepted" '[ "$(tls -tls1_3)" = TLSv1.3 ]'
for v in tls1_2 tls1_1 tls1; do check "${v/_/.} refused" "[ -z \"\$(tls -$v)\" ]"; done
end=$(echo | openssl s_client -connect "$SITE:443" -servername "$SITE" 2>/dev/null | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
check "certificate valid for 14+ more days" '[ -n "$end" ] && [ $(( $(date -d "$end" +%s) - $(date +%s) )) -gt 1209600 ]'
check "certificate matches $SITE" "echo | openssl s_client -connect $SITE:443 -servername $SITE -verify_return_error -verify_hostname $SITE 2>&1 | grep -q 'Verify return code: 0'"
( printf 'GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n' "$SITE"; sleep 2 ) |
  timeout 10 openssl s_client -connect "$SITE:443" -servername "$SITE" -tls1_3 -sess_out "$tmp/sess" -ign_eof >/dev/null 2>&1
printf 'GET / HTTP/1.1\r\nHost: %s\r\n\r\n' "$SITE" > "$tmp/early"
check "0-RTT (early data) not accepted" "! (sleep 2 | timeout 10 openssl s_client -connect $SITE:443 -servername $SITE -tls1_3 -sess_in $tmp/sess -early_data $tmp/early 2>&1 | grep -q 'Early data was accepted')"

section "HTTP"
code() { curl -s -o /dev/null -w '%{http_code} %{redirect_url}' --max-time 15 "$@"; }
check "http://$SITE redirects to https" '[ "$(code "http://$SITE/x")" = "301 https://$SITE/x" ]'
check "https://www.$SITE redirects to $SITE" '[ "$(code "https://www.$SITE/x")" = "301 https://$SITE/x" ]'
check "home page 200" '[ "$(code "https://$SITE/")" = "200 " ]'
check "TRACE refused" '[ "$(curl -s -o /dev/null -w "%{http_code}" -X TRACE "https://$SITE/")" = 405 ]'
check ".git not exposed" '[ "$(code "https://$SITE/.git/HEAD")" = "404 " ]'
curl -sS -D "$tmp/h" -o "$tmp/body" --max-time 15 "https://$SITE/"
hdr() { grep -i "^$1:" "$tmp/h" | head -1 | cut -d: -f2- | tr -d '\r' | sed 's/^ *//'; }
check "HSTS max-age >= 1 year" '[ "$(hdr strict-transport-security | grep -oE "max-age=[0-9]+" | cut -d= -f2)" -ge 31536000 ]'
check "CSP default-src 'none' and frame-ancestors 'none'" "hdr content-security-policy | grep -q \"default-src 'none'\" && hdr content-security-policy | grep -q \"frame-ancestors 'none'\""
check "CSP without unsafe-inline/unsafe-eval" "! hdr content-security-policy | grep -q unsafe-"
check "X-Content-Type-Options nosniff" '[ "$(hdr x-content-type-options)" = nosniff ]'
check "X-Frame-Options DENY" '[ "$(hdr x-frame-options)" = DENY ]'
check "Referrer-Policy no-referrer" '[ "$(hdr referrer-policy)" = no-referrer ]'
check "Permissions-Policy set" '[ -n "$(hdr permissions-policy)" ]'
check "no cookies" '! grep -qi "^set-cookie:" "$tmp/h"'
check "pages not cached (no-cache)" '[ "$(hdr cache-control)" = no-cache ]'
check "no injected scripts (only /site.js)" '[ "$(grep -o "<script[^>]*>" "$tmp/body" | grep -vc "src=\"/site.js")" = 0 ]'

section "security.txt"
curl -sS -D "$tmp/sh" -o "$tmp/st" --max-time 15 "https://$SITE/.well-known/security.txt"
check "served as text/plain; charset=utf-8" 'grep -qi "^content-type: text/plain; charset=utf-8" "$tmp/sh"'
check "has Contact" 'grep -q "^Contact: https://" "$tmp/st"'
exp=$(awk '/^Expires:/ {print $2}' "$tmp/st")
check "Expires 60+ days away" '[ -n "$exp" ] && [ $(( $(date -d "$exp" +%s) - $(date +%s) )) -gt 5184000 ]'

section "DNS"
check "DNSSEC validated (AD) for $SITE" '[ "$(doh "$SITE" A | jq -r .AD)" = true ]'
check "IPv6 (AAAA) present" '[ "$(doh "$SITE" AAAA | jq "[.Answer[]? | select(.type == 28)] | length")" -gt 0 ]'
check "CAA restricts issuers" 'doh "$SITE" CAA | jq -r ".Answer[]?.data" | grep -q "issue \"letsencrypt.org\""'
check "SPF -all (sends no mail)" '[ "$(doh "$SITE" TXT | jq -r ".Answer[]?.data" | tr -d "\"" | grep "^v=spf1")" = "v=spf1 -all" ]'
check "DMARC p=reject" 'doh "_dmarc.$SITE" TXT | jq -r ".Answer[]?.data" | grep -q "p=reject"'
check "null MX" '[ "$(doh "$SITE" MX | jq -r ".Answer[]?.data")" = "0 ." ]'

if [ "${NO_EXTERNAL:-0}" != 1 ]; then
  section "Mozilla HTTP Observatory"
  obs=$(curl -sS -X POST --max-time 60 "https://observatory-api.mdn.mozilla.net/api/v2/scan?host=$SITE")
  check "grade A+ ($(jq -r '"\(.grade) \(.score), \(.tests_passed)/\(.tests_quantity) tests"' <<<"$obs"))" '[ "$(jq -r .grade <<<"$obs")" = "A+" ]'
fi

printf '\n'
if [ "$fails" -gt 0 ]; then echo "live-check: $fails check(s) FAILED for $SITE"; exit 1; fi
echo "live-check: all checks passed for $SITE (for the full picture also run https://internet.nl/site/$SITE/)"
