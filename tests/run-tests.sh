#!/usr/bin/env bash
# The script under test is sourced through a dynamic path, reads several of the
# variables assigned here, and calls the stubs only indirectly via main().
# shellcheck disable=SC1091,SC2034,SC2154,SC2317,SC2329
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FIXTURES_DIR="$SCRIPT_DIR/fixtures"

PASS=0
FAIL=0

pass() {
    PASS=$((PASS + 1))
    printf '  ok  %s\n' "$1"
}

fail() {
    FAIL=$((FAIL + 1))
    printf ' FAIL %s\n' "$1" >&2
    [[ $# -gt 1 ]] && printf '       %s\n' "$2" >&2
    # Never propagate the failure: under `set -e` a non-zero return here
    # would abort the whole suite instead of reporting the failed assertion.
    return 0
}

assert_equals() {
    local name="$1" expected="$2" actual="$3"
    if [[ "$actual" == "$expected" ]]; then
        pass "$name"
    else
        fail "$name" "expected '$expected', got '$actual'"
    fi
}

assert_success() {
    local name="$1"
    shift
    if "$@"; then
        pass "$name"
    else
        fail "$name" "expected success"
    fi
}

assert_failure() {
    local name="$1"
    shift
    if "$@"; then
        fail "$name" "expected failure"
    else
        pass "$name"
    fi
}

assert_exit() {
    local name="$1" expected="$2"
    shift 2
    set +e
    "$@" >/dev/null 2>&1
    local status=$?
    set -e
    if [[ $status -eq $expected ]]; then
        pass "$name"
    else
        fail "$name" "expected exit $expected, got $status"
    fi
}

assert_output_contains() {
    local name="$1" needle="$2"
    shift 2
    local output
    set +e
    output="$("$@" 2>&1)"
    local status=$?
    set -e
    if [[ $status -eq 0 || $status -eq 1 ]] && [[ "$output" == *"$needle"* ]]; then
        pass "$name"
    else
        fail "$name" "output did not contain '$needle'"
    fi
}

# Byte length of a string, independent of the shell's locale.
bytes() {
    printf '%s' "$1" | wc -c | tr -d ' '
}

# Emulates the ffprobe call used for the media tags: h264/AAC/1080p, German.
ffprobe() {
    printf '%s' '{"streams":[{"codec_type":"video","codec_name":"h264","height":1080},{"codec_type":"audio","codec_name":"aac","channels":2,"tags":{"language":"ger"}}]}'
}

# Video files produced below $1, relative to it and sorted.
produced_files() {
    ( cd "$1" && find . -type f \( -name '*.mp4' -o -name '*.mkv' \) | sed 's#^\./##' | LC_ALL=C sort )
}

# shellcheck source=../ard-plus-dl.sh
source "$ROOT_DIR/ard-plus-dl.sh"

test_decode_jwt_header() {
    printf '\n[decode_jwt_header]\n'
    assert_equals 'decodes standard JWT header' 'JWT' "$(decode_jwt_header 'eyJ0eXAiOiJKV1QifQ.payload.signature')"
    assert_equals 'decodes base64url header' 'JWT' "$(decode_jwt_header 'eyJ0eXAiOiJKV1QifQ')"
    assert_failure 'rejects empty token' decode_jwt_header ''
}

test_resolve_token_file() {
    printf '\n[resolve_token_file]\n'
    local tmp saved_home saved_xdg
    tmp=$(mktemp -d)
    work_dir="$tmp"
    saved_home="${HOME:-}"
    saved_xdg="${XDG_STATE_HOME:-}"
    unset XDG_STATE_HOME
    HOME=''
    resolve_token_file
    assert_equals 'falls back to work_dir without HOME' \
        "$tmp/.ard-plus-dl/token" "$token_file"
    HOME="$saved_home"
    if [[ -n "$saved_xdg" ]]; then
        export XDG_STATE_HOME="$saved_xdg"
    fi
}

test_normalize_url() {
    printf '\n[normalize_url]\n'
    assert_equals 'strips trailing slash' \
        'https://www.ardplus.de/details/foo' \
        "$(normalize_url 'https://www.ardplus.de/details/foo/')"
    assert_equals 'strips comments' \
        'https://www.ardplus.de/details/foo' \
        "$(normalize_url 'https://www.ardplus.de/details/foo # note')"
    assert_equals 'trims whitespace' \
        'https://www.ardplus.de/details/foo' \
        "$(normalize_url '  https://www.ardplus.de/details/foo  ')"
    assert_equals 'strips query string' \
        'https://www.ardplus.de/details/a0S01000000EWYi-lola-rennt' \
        "$(normalize_url 'https://www.ardplus.de/details/a0S01000000EWYi-lola-rennt?utm_source=share')"
    assert_equals 'strips query and trailing slash' \
        'https://www.ardplus.de/details/foo' \
        "$(normalize_url 'https://www.ardplus.de/details/foo/?ref=1')"
}

test_sanitize_path_component() {
    printf '\n[sanitize_path_component]\n'
    assert_equals 'replaces single slash' 'A B' "$(sanitize_path_component 'A/B')"
    assert_equals 'replaces all slashes' 'A B C' "$(sanitize_path_component 'A/B/C')"
    assert_equals 'leaves title without slashes unchanged' 'Normal Title' "$(sanitize_path_component 'Normal Title')"
    assert_equals 'strips glob and bracket characters' 'AC' "$(sanitize_path_component 'A*?[]C')"
    assert_equals 'strips control characters and newlines' 'AB' "$(sanitize_path_component $'A\nB')"
    assert_equals 'trims surrounding dots and spaces' 'Title' "$(sanitize_path_component ' ..Title.. ')"
    assert_equals 'trims leading dots' 'Title' "$(sanitize_path_component '..Title')"
    assert_equals 'empty title becomes placeholder' '_' "$(sanitize_path_component '')"
    assert_equals 'dotted title becomes placeholder' '_' "$(sanitize_path_component ' ... ')"
    assert_equals 'collapses runs of spaces' 'A B' "$(sanitize_path_component 'A   B')"
    assert_equals 'collapses spaces left by slash replacement' 'A B' \
        "$(sanitize_path_component 'A / B')"
}

test_graphql_response_ok() {
    printf '\n[graphql_response_ok]\n'
    assert_success 'accepts valid response' \
        graphql_response_ok "$FIXTURES_DIR/graphql-ok.json"
    assert_failure 'rejects graphql error' \
        graphql_response_ok "$FIXTURES_DIR/graphql-error.json"
    assert_failure 'rejects missing file' \
        graphql_response_ok "$FIXTURES_DIR/does-not-exist.json"
}

test_validate_content_url() {
    printf '\n[content url validation]\n'
    assert_success 'accepts a details url' \
        validate_content_url 'https://www.ardplus.de/details/a0S01000000EWYi-lola-rennt'
    assert_success 'accepts a category url' \
        validate_content_url 'https://www.ardplus.de/kategorie/tatort-koeln'
    assert_failure 'rejects other schemes' \
        validate_content_url 'ftp://www.ardplus.de/details/x'
    assert_failure 'rejects foreign hosts' \
        validate_content_url 'https://example.com/details/x'
    assert_failure 'rejects the bare host' \
        validate_content_url 'https://www.ardplus.de/'
    assert_failure 'rejects relative paths' \
        validate_content_url '/kategorie/foo'
    assert_equals 'reports the unsupported host' 'unsupported host: example.com' \
        "$(validate_content_url 'https://example.com/details/x')"
    assert_equals 'reports the unsupported path' 'unsupported path: /' \
        "$(validate_content_url 'https://www.ardplus.de/')"
    assert_equals 'reports a malformed url' 'not an absolute http(s) URL: /kategorie/foo' \
        "$(validate_content_url '/kategorie/foo')"
}

test_download_paths() {
    printf '\n[download paths]\n'
    local tmp
    tmp=$(mktemp -d)
    downloads_dir="$tmp/downloads"
    mkdir -p "$downloads_dir"

    assert_equals 'resolve_download_path' \
        "$downloads_dir/Show/Season 01/Episode" \
        "$(resolve_download_path 'Show/Season 01/Episode')"
    assert_equals 'output_mp4_path' \
        "$downloads_dir/Movie (2024)/Movie.mp4" \
        "$(output_mp4_path 'Movie (2024)/Movie')"
}

test_skip_logic() {
    printf '\n[skip logic]\n'
    local tmp
    tmp=$(mktemp -d)
    downloads_dir="$tmp/downloads"
    skip_existing_files=1
    batch_mode=0
    mkdir -p "$downloads_dir/Complete Show"

    local output_path
    output_path="$(output_mp4_path 'Complete Show/movie')"
    printf 'video' >"$output_path"

    assert_success 'detects complete file' is_complete_file 'Complete Show/movie'
    assert_success 'skips existing complete file' skip_if_exists 'Complete Show/movie'

    mkdir -p "$downloads_dir/Partial Show"
    output_path="$(output_mp4_path 'Partial Show/movie')"
    printf 'partial' >"${output_path}.ytdl"

    assert_failure 'detects incomplete artifacts' is_complete_file 'Partial Show/movie'
    assert_failure 'does not skip incomplete download' skip_if_exists 'Partial Show/movie'

    mkdir -p "$downloads_dir/Leftover Show"
    output_path="$(output_mp4_path 'Leftover Show/movie')"
    printf 'video' >"$output_path"
    printf 'fragment' >"${output_path}.part-Frag1"

    assert_success 'mp4 with leftover part file is complete' is_complete_file 'Leftover Show/movie'
    assert_success 'skips mp4 with leftover part file' skip_if_exists 'Leftover Show/movie'

    mkdir -p "$downloads_dir/Cleanup Show"
    output_path="$(output_mp4_path 'Cleanup Show/movie')"
    printf 'video' >"$output_path"
    printf 'fragment' >"${output_path}.part-Frag1"
    printf 'state' >"${output_path}.ytdl"
    printf 'fragment' >"$(dirname "$output_path")/$(basename "$output_path" .mp4).f137.mp4"

    cleanup_incomplete_files 'Cleanup Show/movie'
    assert_success 'cleanup keeps part file' test -f "${output_path}.part-Frag1"
    assert_failure 'cleanup removes ytdl file' test -f "${output_path}.ytdl"
    assert_failure 'cleanup removes video fragments' test -f \
        "$(dirname "$output_path")/$(basename "$output_path" .mp4).f137.mp4"

    skip_existing_files=0
    assert_failure 'force redownload ignores complete file' skip_if_exists 'Complete Show/movie'
}

test_episode_tail_from() {
    printf '\n[episode tail offset]\n'
    local episodes
    episodes='[{"episodeNo":1},{"episodeNo":2},{"episodeNo":3},{"episodeNo":4},{"episodeNo":5}]'

    # episode_tail_from returns what `tail -n +…` gets, so assert on the
    # episodes that actually survive.
    kept_for() {
        echo "$episodes" | jq -c '.[]' | tail -n +"$1" | awk 'END { print NR }'
    }

    # 0 and 1 both mean "skip nothing" and must not drop an episode.
    assert_equals 'skip 0 keeps all five episodes' '5' "$(kept_for "$(episode_tail_from 0 1)")"
    assert_equals 'skip 1 keeps all five episodes' '5' "$(kept_for "$(episode_tail_from 1 1)")"
    assert_equals 'skip 2 drops the first two episodes' '3' "$(kept_for "$(episode_tail_from 2 1)")"
    assert_equals 'skip 3 drops the first three episodes' '2' "$(kept_for "$(episode_tail_from 3 1)")"
    assert_equals 'skip 5 drops every episode' '0' "$(kept_for "$(episode_tail_from 5 1)")"

    # The caller passes 0 once the first season consumed the skip, so every
    # later season has to start at episode 1 again.
    assert_equals 'skip is not applied to later seasons' '5' "$(kept_for "$(episode_tail_from 2 0)")"
    assert_equals 'skip 1 stays a no-op on later seasons' '5' "$(kept_for "$(episode_tail_from 1 0)")"
    assert_equals 'skip 0 stays a no-op on later seasons' '5' "$(kept_for "$(episode_tail_from 0 0)")"
}

test_build_path_functions() {
    printf '\n[output path builders]\n'

    # build_movie_path <title> [year]
    assert_equals 'movie path nests the title under title and year' \
        'Lola rennt (1998)/Lola rennt' "$(build_movie_path 'Lola rennt' 1998)"
    assert_equals 'movie path without a year omits the year' \
        'Lola rennt/Lola rennt' "$(build_movie_path 'Lola rennt' '')"
    assert_equals 'movie title slashes stay inside one directory' \
        'Der Fall X: Y (2020)/Der Fall X: Y' "$(build_movie_path 'Der Fall/X: Y' 2020)"
    assert_equals 'quotes in a movie title survive' \
        'Say "Hi"/Say "Hi"' "$(build_movie_path 'Say "Hi"' '')"

    # build_episode_path <series title> <season> [episode no] <episode title>
    assert_equals 'episode path numbers season and episode' \
        'Gegen den Wind/Season 01/Gegen den Wind S01E03 - Schönes Wochenende' \
        "$(build_episode_path 'Gegen den Wind' 1 3 'Schönes Wochenende')"
    assert_equals 'episode path pads season and episode to two digits' \
        'Show/Season 12/Show S12E07 - Finale' "$(build_episode_path 'Show' 12 7 'Finale')"
    assert_equals 'episode without a number falls back to a placeholder' \
        'Show/Season 03/Show S03E?? - Pilot' "$(build_episode_path 'Show' 3 '' 'Pilot')"
    assert_equals 'episode title is sanitised inside the path' \
        'Show/Season 01/Show S01E01 - a bc' "$(build_episode_path 'Show' 1 1 'a/b?c')"

    # build_tatort_path <city> [team] [episode no] <title> [year]
    assert_equals 'tatort path with team, episode and year' \
        'Tatort Köln (Bucerius) - Folge 850 - Der Fall (2019)' \
        "$(build_tatort_path 'Köln' 'Bucerius' 850 'Der Fall' 2019)"
    assert_equals 'tatort path without a team drops the team' \
        'Tatort Bremen - Der Fall' "$(build_tatort_path 'Bremen' '' '' 'Der Fall' '')"
    assert_equals 'tatort path without an episode drops the Folge part' \
        'Tatort Bremen (Team X) - Der Fall' \
        "$(build_tatort_path 'Bremen' 'Team X' '' 'Der Fall' '')"
    assert_equals 'tatort path keeps the title year' \
        'Tatort Köln - Folge 1 - Der Fall (2020)' \
        "$(build_tatort_path 'Köln' '' 1 'Der Fall' 2020)"
}

test_path_length_limit() {
    printf '\n[path length limit]\n'
    local tmp long_ascii long_umlaut capped within

    long_ascii=$(printf 'a%.0s' {1..400})
    assert_equals 'caps an over-long component at the byte limit' '200' \
        "$(bytes "$(cap_component_bytes 200 "$long_ascii")")"
    assert_equals 'keeps a short component unchanged' 'short' \
        "$(cap_component_bytes 200 'short')"

    long_umlaut=$(printf 'ä%.0s' {1..300})
    capped=$(cap_component_bytes 200 "$long_umlaut")
    [[ "$(bytes "$capped")" -le 200 ]] && within=ok || within=too-long
    assert_equals 'caps multi-byte text within the byte limit' 'ok' "$within"
    # A byte-wise cut would leave a partial character behind, which does not
    # match the pattern and therefore survives the substitution below.
    assert_equals 'truncation keeps whole characters only' '' "${capped//ä/}"

    # The regression that matters: the composed paths must be creatable on disk.
    tmp=$(mktemp -d)
    downloads_dir="$tmp/downloads"
    mkdir -p "$downloads_dir"
    local long_title
    long_title=$(printf 'Titel %.0s' {1..60})

    assert_creatable() {
        local name="$1" path="$2" out
        out=$(output_mp4_path "$path")
        mkdir -p "$(dirname "$out")"
        if : >"$out" 2>/dev/null; then
            pass "$name"
        else
            fail "$name" "$out"
        fi
    }
    assert_creatable 'movie path with a 360-byte title is creatable' \
        "$(build_movie_path "$long_title" 2024)"
    assert_creatable 'episode path with a 360-byte title is creatable' \
        "$(build_episode_path "$long_title" 1 1 "$long_title")"
    assert_creatable 'tatort path with a 360-byte title is creatable' \
        "$(build_tatort_path 'München' 'Leitmayr' 1200 "$long_title" 2024)"
}

test_session_payloads() {
    printf '\n[session payloads]\n'
    local tmp captured
    tmp=$(mktemp -d)

    # Stub curl so the payload the script actually sends can be inspected
    # instead of re-deriving it from a copy of the jq template.
    cat >"$tmp/curl" <<'STUB'
#!/usr/bin/env bash
payload='' prev=''
for arg in "$@"; do
    [[ "$prev" == '--data-raw' ]] && payload="$arg"
    prev="$arg"
done
printf '%s' "$payload" >"$CURL_CAPTURE_FILE"
response="$CURL_STUB_RESPONSE"
[[ -n "$response" ]] || response='{}'
printf '%s' "$response"
STUB
    chmod +x "$tmp/curl"
    export CURL_CAPTURE_FILE="$tmp/payload.json"
    curlBin="$tmp/curl"

    movieId='abc123'
    contentType='CmsEpisode'
    token='test-token'
    : >"$CURL_CAPTURE_FILE"
    export CURL_STUB_RESPONSE='{"authorizationParams":"token=abc"}'
    assert_equals 'auth returns the authorization params' 'token=abc' "$(auth)"
    captured=$(<"$CURL_CAPTURE_FILE")
    assert_equals 'auth sends the tracked content id' 'abc123' "$(jq -r '.contentId' <<<"$captured")"
    assert_equals 'auth tracks the episode content type' 'CmsEpisode' "$(jq -r '.contentType' <<<"$captured")"
    assert_equals 'auth requests a non-download session' 'false' "$(jq -r '.download' <<<"$captured")"

    : >"$CURL_CAPTURE_FILE"
    export CURL_STUB_RESPONSE='{}'
    cleanup
    captured=$(<"$CURL_CAPTURE_FILE")
    assert_equals 'cleanup deletes the tracked episode session' 'CmsEpisode' \
        "$(jq -r '.contentType' <<<"$captured")"
    assert_equals 'cleanup sends no extra payload keys' 'contentId,contentType' \
        "$(jq -r 'keys | join(",")' <<<"$captured")"

    # Without a tracked content there is nothing to authorize or release.
    movieId=''
    contentType=''
    : >"$CURL_CAPTURE_FILE"
    export CURL_STUB_RESPONSE='{"authorizationParams":"token=abc"}'
    assert_equals 'auth without content context returns nothing' '' "$(auth)"
    assert_equals 'auth without content context does not call the api' '' \
        "$(<"$CURL_CAPTURE_FILE")"
    assert_success 'cleanup skips without content context' cleanup

    token=''
    unset CURL_CAPTURE_FILE
    unset CURL_STUB_RESPONSE
}

test_session_renewal() {
    printf '\n[session renewal]\n'
    local tmp log status result
    tmp=$(mktemp -d)
    log="$tmp/log"
    : >"$log"

    # The stub emulates the three endpoints the script talks to: /auth/login
    # answers with a JWT header, /token/session with the playback parameters,
    # playback/delete with an empty body. When grant-after-login exists, a
    # login makes the next session call succeed.
    cat >"$tmp/curl" <<'STUB'
#!/usr/bin/env bash
url=''
for arg in "$@"; do
    case "$arg" in
        https://*) url="$arg" ;;
    esac
done
case "$url" in
    *'/auth/login'*)
        n=$(cat "$STUB_DIR/login-count" 2>/dev/null || printf '0')
        printf '%s' "$((n + 1))" >"$STUB_DIR/login-count"
        [[ -f "$STUB_DIR/login-fails" ]] && exit 0
        printf 'authorization: Bearer %s\n' "$STUB_JWT"
        ;;
    *'/token/session/playback/delete'*)
        printf '{}'
        ;;
    *'/token/session'*)
        if [[ -f "$STUB_DIR/grant-after-login" && -f "$STUB_DIR/login-count" ]]; then
            printf '{"authorizationParams":"token=abc"}'
        else
            printf '{"authorizationParams":null}'
        fi
        ;;
esac
STUB
    chmod +x "$tmp/curl"
    export STUB_DIR="$tmp"
    export STUB_JWT='eyJ0eXAiOiJKV1QifQ.payload.signature'
    curlBin="$tmp/curl"
    token_file="$tmp/token"
    username='user'
    password='pass'
    movieId='abc123'
    contentType='CmsMovie'
    # Logins are counted, so silencing output keeps the count readable.
    log_msg() { printf '%s\n' "$1" >>"$log"; }

    # A rejected session is renewed once and the request succeeds afterwards.
    : >"$tmp/grant-after-login"
    result=$(ensure_playback_authorization)
    assert_equals 'renewed session returns the authorization params' 'token=abc' "$result"
    assert_equals 'renewal logs in exactly once' '1' "$(<"$tmp/login-count")"
    assert_success 'renewal stores the new token' test -f "$token_file"
    assert_equals 'renewal stores the JWT it received' \
        'eyJ0eXAiOiJKV1QifQ.payload.signature' "$(<"$token_file")"
    assert_equals 'renewal is logged' '1' \
        "$(grep -c 'neuer Login' "$log" | tr -d ' ')"
    assert_equals 'a successful authorization resets the renewal guard' '0' "$session_renewed"

    # When the API keeps rejecting the session, the renewal is not repeated
    # for every following episode.
    rm -f "$tmp/grant-after-login"
    rm -f "$tmp/login-count"
    set +e
    ensure_playback_authorization >/dev/null
    status=$?
    set -e
    assert_equals 'a rejected session fails' '1' "$status"
    assert_equals 'the first rejection renews the session' '1' "$(<"$tmp/login-count")"
    set +e
    ensure_playback_authorization >/dev/null
    status=$?
    set -e
    assert_equals 'a second rejection fails too' '1' "$status"
    assert_equals 'the renewal is not repeated for the next episode' '1' "$(<"$tmp/login-count")"

    # Bad credentials must return a failure instead of terminating the run.
    : >"$tmp/login-fails"
    login_quiet() { login 2>/dev/null; }
    assert_failure 'login reports bad credentials' login_quiet
    rm -f "$tmp/login-fails"

    unset STUB_DIR
    unset STUB_JWT
    token=''
    movieId=''
    contentType=''
}

test_run_yt_dlp_log_capture() {
    printf '\n[yt-dlp log capture]\n'
    local tmp log status
    tmp=$(mktemp -d)
    log="$tmp/log"
    mkdir -p "$tmp/bin"
    cat >"$tmp/bin/yt-dlp" <<'STUB'
#!/bin/sh
# A carriage-return progress line followed by more error lines than the log
# window keeps.
printf 'progress 1\rmore progress\r'
i=1
while [ "$i" -le 30 ]; do
    printf 'ERROR line %s\n' "$i" >&2
    i=$((i + 1))
done
exit 1
STUB
    chmod +x "$tmp/bin/yt-dlp"
    PATH="$tmp/bin:$PATH"
    downloads_dir="$tmp/downloads"
    skip_existing_files=1
    batch_mode=1
    LOG_FILE="$log"
    : >"$log"

    set +e
    run_yt_dlp 'https://video/stream' 'Show/Episode' 'Show/Episode' >/dev/null
    status=$?
    set -e

    assert_equals 'a failing download is reported' '1' "$status"
    assert_equals 'the failure reason names the item' \
        'yt-dlp download failed for Show/Episode' "$DOWNLOAD_FAIL_REASON"
    assert_equals 'the yt-dlp error reaches the batch log' '1' \
        "$(grep -c 'ERROR line 30' "$log" | tr -d ' ')"
    assert_equals 'error lines beyond the log window are dropped' '0' \
        "$(grep -c 'ERROR line 1$' "$log" | tr -d ' ')"

    # In the interactive mode yt-dlp keeps writing to the terminal.
    batch_mode=0
    : >"$log"
    set +e
    run_yt_dlp 'https://video/stream' 'Show/Episode' 'Show/Episode' >/dev/null 2>/dev/null
    status=$?
    set -e
    assert_equals 'a failing download fails in interactive mode too' '1' "$status"
    assert_equals 'nothing is written to the log without batch mode' '' "$(cat "$log")"

    PATH="${PATH#"$tmp/bin:"}"
}

test_download_url_unsupported_url() {
    printf '\n[download_url url validation]\n'
    local tmp api_calls=0 status
    tmp=$(mktemp -d)
    downloads_dir="$tmp/downloads"
    mkdir -p "$downloads_dir"
    content_result="$tmp/content.json"
    DOWNLOAD_FAIL_REASON=''

    fetch_graphql() { api_calls=$((api_calls + 1)); return 1; }
    skip_if_exists() { return 1; }
    log_msg() { :; }

    set +e
    download_url 'https://example.com/foo' 1
    status=$?
    set -e

    assert_equals 'fails on an unsupported URL' '1' "$status"
    assert_equals 'does not call the API for an unsupported URL' '0' "$api_calls"
    assert_equals 'reports the unsupported host as the reason' \
        'invalid URL: unsupported host: example.com' "$DOWNLOAD_FAIL_REASON"
}

test_fetch_graphql_retry_policy() {
    printf '\n[graphql retry policy]\n'
    local tmp out log calls sequence saved_log_msg
    tmp=$(mktemp -d)
    out="$tmp/out.json"
    log="$tmp/log"
    calls="$tmp/calls"
    sequence="$tmp/sequence"
    saved_log_msg=$(declare -f log_msg)

    # The stub consumes one "<status>|<body>" line per call, writes the body to
    # the -o file and echoes the status.
    cat >"$tmp/curl" <<'STUB'
#!/usr/bin/env bash
out='' prev=''
for arg in "$@"; do
    [[ "$prev" == '-o' ]] && out="$arg"
    prev="$arg"
done
line=$(head -n 1 "$GRAPHQL_STUB_SEQUENCE")
tail -n +2 "$GRAPHQL_STUB_SEQUENCE" >"$GRAPHQL_STUB_SEQUENCE.next"
mv "$GRAPHQL_STUB_SEQUENCE.next" "$GRAPHQL_STUB_SEQUENCE"
printf '%s\n' "$line" >>"$GRAPHQL_STUB_CALLS"
printf '%s' "${line#*|}" >"$out"
printf '%s' "${line%%|*}"
STUB
    chmod +x "$tmp/curl"
    export GRAPHQL_STUB_SEQUENCE="$sequence"
    export GRAPHQL_STUB_CALLS="$calls"
    curlBin="$tmp/curl"
    token='stub'
    sleep() { :; }
    : >"$log"
    log_msg() { printf '%s\n' "$1" >>"$log"; }
    call_fetch() { fetch_graphql "$@" >/dev/null; }

    : >"$calls"
    printf '200|{"errors":[{"message":"Movie not found"}]}\n' >"$sequence"
    assert_failure 'permanent API error fails' \
        call_fetch 'MovieDetails' 'query' '{"movieId":"x","externalId":"","slug":"","potentialMovieId":""}' "$out"
    assert_equals 'permanent API error is requested once' '1' \
        "$(wc -l <"$calls" | tr -d ' ')"
    assert_equals 'permanent API error is reported' '1' \
        "$(grep -c 'permanently' "$log" | tr -d ' ')"
    assert_equals 'permanent API error is not logged as a retry' '0' \
        "$(grep -c 'attempt ' "$log" | tr -d ' ')"

    : >"$calls"
    : >"$log"
    printf '000|\n200|{"data":{"movie":{"id":"m"}}}\n' >"$sequence"
    assert_success 'transport error is retried' \
        call_fetch 'MovieDetails' 'query' '{"movieId":"x","externalId":"","slug":"","potentialMovieId":""}' "$out"
    assert_equals 'transport error costs two calls' '2' \
        "$(wc -l <"$calls" | tr -d ' ')"
    assert_equals 'transport error is logged as a retry' '1' \
        "$(grep -c 'attempt 1/5' "$log" | tr -d ' ')"

    : >"$calls"
    : >"$log"
    printf '503|\n200|{"data":{"movie":{"id":"m"}}}\n' >"$sequence"
    assert_success 'server error is retried' \
        call_fetch 'MovieDetails' 'query' '{"movieId":"x","externalId":"","slug":"","potentialMovieId":""}' "$out"
    assert_equals 'server error costs two calls' '2' \
        "$(wc -l <"$calls" | tr -d ' ')"

    unset -f sleep
    eval "$saved_log_msg"
    unset GRAPHQL_STUB_SEQUENCE
    unset GRAPHQL_STUB_CALLS
}

test_download_url_episode_failures() {
    printf '\n[episode failure isolation]\n'
    local tmp downloaded status cases=0
    tmp=$(mktemp -d)
    downloaded="$tmp/downloaded.txt"
    content_result="$tmp/content.json"
    automatic_download=1
    batch_mode=0
    skip_existing_files=1
    file_tag='example'
    token='test-token'

    local series_details='{"data":{"movie":null,"series":{"title":"My Show","seasons":{"nodes":[{"id":"s1","title":"Staffel 1","seasonInSeries":1}]}}}}'
    local series_episodes='{"data":{"episodes":{"nodes":[{"id":"ep1","episodeInSeason":1,"title":"Erste","videoSource":{"dashUrl":"https://video/1"}},{"id":"ep2","episodeInSeason":2,"title":"Zweite","videoSource":{"dashUrl":"https://video/2"}},{"id":"ep3","episodeInSeason":3,"title":"Dritte","videoSource":{"dashUrl":"https://video/3"}}]}}}'
    local tag='German.AAC.1080p.Web-DL.x264-example'
    local season_dir="My.Show.S01.$tag"

    fresh_downloads() {
        cases=$((cases + 1))
        downloads_dir="$tmp/downloads-$cases"
        mkdir -p "$downloads_dir"
        : >"$downloaded"
    }

    # Everything below the API boundary is stubbed; the loop, its failure
    # handling, the media probe and the naming are the real code paths.
    fetch_graphql() {
        if [[ "$1" == 'MovieDetails' ]]; then
            printf '%s' "$series_details" >"$4"
            echo 200
            return 0
        fi
        printf '%s' "$series_episodes" >"$4"
        echo 200
        return 0
    }
    auth() { printf 'token=abc'; }
    cleanup() { :; }
    skip_if_exists() { return 1; }
    log_msg() { :; }
    run_yt_dlp() {
        local target
        printf '%s\n' "$2" >>"$downloaded"
        if [[ -n "${FAIL_EPISODE:-}" && "$2" == *"$FAIL_EPISODE"* ]]; then
            DOWNLOAD_FAIL_REASON="yt-dlp download failed for $2"
            return 1
        fi
        target=$(output_mp4_path "$2")
        mkdir -p "$(dirname "$target")"
        printf 'video' >"$target"
        return 0
    }

    export FAIL_EPISODE='S01E02'
    fresh_downloads
    set +e
    download_url 'https://www.ardplus.de/details/a0S010000007GcX-my-show' 1
    status=$?
    set -e
    assert_equals 'a failing episode still fails the series' '1' "$status"
    assert_equals 'later episodes are downloaded after a failure' \
"$season_dir/My.Show.S01E01.$tag.mp4
$season_dir/My.Show.S01E03.$tag.mp4" \
        "$(produced_files "$downloads_dir")"
    assert_equals 'the summary names the first failure' \
        '1 episode download(s) failed (first: yt-dlp download failed for My Show/Season 01/My Show S01E02 - Zweite)' \
        "$DOWNLOAD_FAIL_REASON"

    # The first episode failing must not stop the two after it either.
    export FAIL_EPISODE='S01E01'
    fresh_downloads
    set +e
    download_url 'https://www.ardplus.de/details/a0S010000007GcX-my-show' 1
    status=$?
    set -e
    assert_equals 'a first-episode failure fails the series' '1' "$status"
    assert_equals 'episodes after a first-episode failure are downloaded' \
"$season_dir/My.Show.S01E02.$tag.mp4
$season_dir/My.Show.S01E03.$tag.mp4" \
        "$(produced_files "$downloads_dir")"
    assert_equals 'the first failure is reported' \
        '1 episode download(s) failed (first: yt-dlp download failed for My Show/Season 01/My Show S01E01 - Erste)' \
        "$DOWNLOAD_FAIL_REASON"

    # A season whose episodes cannot be fetched must not stop later seasons.
    export FAIL_EPISODE='NONE'
    fresh_downloads
    series_details='{"data":{"movie":null,"series":{"title":"My Show","seasons":{"nodes":[{"id":"s1","title":"Staffel 1","seasonInSeries":1},{"id":"s2","title":"Staffel 2","seasonInSeries":2}]}}}}'
    fetch_graphql() {
        if [[ "$1" == 'MovieDetails' ]]; then
            printf '%s' "$series_details" >"$4"
            echo 200
            return 0
        fi
        if [[ "$3" == *'"seasonId":"s2"'* ]]; then
            printf '{"errors":[{"message":"season unavailable"}]}' >"$4"
            echo 200
            return 1
        fi
        printf '%s' "$series_episodes" >"$4"
        echo 200
        return 0
    }
    set +e
    download_url 'https://www.ardplus.de/details/a0S010000007GcX-my-show' 1
    status=$?
    set -e
    assert_equals 'an unfetchable season fails the series' '1' "$status"
    assert_equals 'the first season is still downloaded' '3' \
        "$(produced_files "$downloads_dir" | wc -l | tr -d ' ')"
    assert_equals 'the unfetchable season is reported' \
        '1 episode download(s) failed (first: could not fetch episodes for season 2 (graphql: season unavailable))' \
        "$DOWNLOAD_FAIL_REASON"
}

test_season_selection_validation() {
    printf '\n[season selection validation]\n'
    local tmp status log runs=0
    tmp=$(mktemp -d)
    log="$tmp/log"
    content_result="$tmp/content.json"
    automatic_download=0
    batch_mode=0
    skip_existing_files=1
    file_tag='example'
    token='test-token'

    local series_details='{"data":{"movie":null,"series":{"title":"My Show","seasons":{"nodes":[{"id":"s1","title":"Staffel 1","seasonInSeries":1},{"id":"s2","title":"Staffel 2","seasonInSeries":2}]}}}}'
    local s1_episodes='{"data":{"episodes":{"nodes":[{"id":"e1","episodeInSeason":1,"title":"Eins","videoSource":{"dashUrl":"https://video/1"}}]}}}'
    local s2_episodes='{"data":{"episodes":{"nodes":[{"id":"e2","episodeInSeason":1,"title":"Zwei","videoSource":{"dashUrl":"https://video/2"}}]}}}'
    local tag='German.AAC.1080p.Web-DL.x264-example'

    fetch_graphql() {
        if [[ "$1" == 'MovieDetails' ]]; then
            printf '%s' "$series_details" >"$4"
        elif [[ "$3" == *'"seasonId":"s2"'* ]]; then
            printf '%s' "$s2_episodes" >"$4"
        else
            printf '%s' "$s1_episodes" >"$4"
        fi
        echo 200
        return 0
    }
    auth() { printf 'token=abc'; }
    cleanup() { :; }
    skip_if_exists() { return 1; }
    log_msg() { printf '%s\n' "$1" >>"$log"; }
    run_yt_dlp() {
        local target
        target=$(output_mp4_path "$2")
        mkdir -p "$(dirname "$target")"
        printf 'video' >"$target"
    }

    # The prompt reads stdin, so the selection is fed in from a file. Every run
    # gets its own downloads directory so a previous run cannot make it skip.
    run_selection() {
        local input="$1"
        runs=$((runs + 1))
        downloads_dir="$tmp/downloads-$runs"
        mkdir -p "$downloads_dir"
        : >"$log"
        set +e
        download_url 'https://www.ardplus.de/details/a0S010000007GcX-my-show' 1 <"$input"
        status=$?
        set -e
    }

    printf '\n' >"$tmp/empty-input"
    run_selection "$tmp/empty-input"
    assert_equals 'ended input fails' '1' "$status"
    assert_equals 'ended input downloads nothing' '' "$(produced_files "$downloads_dir")"
    assert_equals 'ended input is reported' 'no season selected (input ended)' "$DOWNLOAD_FAIL_REASON"

    printf 'bogus\n1\n' >"$tmp/bogus-input"
    run_selection "$tmp/bogus-input"
    assert_equals 'a non-numeric selection is retried' '1' \
        "$(grep -c 'Ungültige Staffelauswahl' "$log" | tr -d ' ')"
    assert_equals 'a retried selection downloads season 1' \
        "My.Show.S01.$tag/My.Show.S01E01.$tag.mp4" \
        "$(produced_files "$downloads_dir")"
    assert_equals 'a retried selection succeeds' '0' "$status"

    printf '99\n01\n' >"$tmp/unknown-input"
    run_selection "$tmp/unknown-input"
    assert_equals 'an unknown season is retried' '1' \
        "$(grep -c 'Ungültige Staffelauswahl' "$log" | tr -d ' ')"
    assert_equals 'a leading zero selects the right season' \
        "My.Show.S01.$tag/My.Show.S01E01.$tag.mp4" \
        "$(produced_files "$downloads_dir")"

    printf '1,2\n' >"$tmp/comma-input"
    run_selection "$tmp/comma-input"
    assert_equals 'comma separated seasons are both downloaded' \
"My.Show.S01.$tag/My.Show.S01E01.$tag.mp4
My.Show.S02.$tag/My.Show.S02E01.$tag.mp4" \
        "$(produced_files "$downloads_dir")"
    assert_equals 'a valid selection does not retry' '0' \
        "$(grep -c 'Ungültige Staffelauswahl' "$log" | tr -d ' ')"
}

test_episode_padding_in_downloads() {
    printf '\n[episode padding in downloads]\n'
    local tmp status cases=0
    tmp=$(mktemp -d)
    content_result="$tmp/content.json"
    automatic_download=1
    batch_mode=0
    skip_existing_files=1
    file_tag='example'
    token='test-token'

    local tag='German.AAC.1080p.Web-DL.x264-example'
    local series_details='{"data":{"movie":null,"series":{"title":"Sturm der Liebe","seasons":{"nodes":[{"id":"s21","title":"Staffel 21","seasonInSeries":21}]}}}}'

    fresh_downloads() {
        cases=$((cases + 1))
        downloads_dir="$tmp/downloads-$cases"
        mkdir -p "$downloads_dir"
    }
    fetch_graphql() {
        if [[ "$1" == 'MovieDetails' ]]; then
            printf '%s' "$series_details" >"$4"
        else
            printf '%s' "$EPISODES_JSON" >"$4"
        fi
        echo 200
        return 0
    }
    auth() { printf 'token=abc'; }
    cleanup() { :; }
    skip_if_exists() { return 1; }
    log_msg() { :; }
    # The padding decision happens before the download; the ffprobe path is
    # covered by test_finalize_output and test_probe_media_tag.
    probe_media_tag() { printf 'German.AAC.1080p.Web-DL.x264'; }
    run_yt_dlp() {
        local target
        target=$(output_mp4_path "$2")
        mkdir -p "$(dirname "$target")"
        printf 'video' >"$target"
        return 0
    }
    # The episode numbers decide the padding, not how many episodes the test
    # happens to download.
    season_episodes() {
        jq -nc --args '[$ARGS.positional[] | tonumber | {id: ("e" + (. | tostring)),
                                 episodeInSeason: .,
                                 title: ("Folge " + (. | tostring)),
                                 videoSource: {dashUrl: ("https://video/" + (. | tostring))}}]
                      | {data: {episodes: {nodes: .}}}' "$@"
    }

    export EPISODES_JSON
    # A season whose highest episode number reaches 151 (Sturm der Liebe S21).
    EPISODES_JSON=$(season_episodes 148 149 150 151)
    fresh_downloads
    set +e
    download_url 'https://www.ardplus.de/details/a0T01000002HaKM-sturm-der-liebe' 1
    status=$?
    set -e
    assert_equals 'a long season succeeds' '0' "$status"
    assert_equals 'the first downloaded episode uses three digits' \
        "Sturm.der.Liebe.S21.$tag/Sturm.der.Liebe.S21E148.$tag.mp4" \
        "$(produced_files "$downloads_dir" | head -n 1)"
    assert_equals 'the highest episode uses three digits' \
        "Sturm.der.Liebe.S21.$tag/Sturm.der.Liebe.S21E151.$tag.mp4" \
        "$(produced_files "$downloads_dir" | tail -n 1)"

    # Exactly 100 episodes already switch to three digits.
    EPISODES_JSON=$(season_episodes 99 100)
    fresh_downloads
    set +e
    download_url 'https://www.ardplus.de/details/a0T01000002HaKM-sturm-der-liebe' 1
    status=$?
    set -e
    assert_equals 'a season reaching episode 100 succeeds' '0' "$status"
    assert_equals 'episode 100 is padded to three digits' \
        "Sturm.der.Liebe.S21.$tag/Sturm.der.Liebe.S21E099.$tag.mp4
Sturm.der.Liebe.S21.$tag/Sturm.der.Liebe.S21E100.$tag.mp4" \
        "$(produced_files "$downloads_dir")"

    # Below 100 the numbering stays two-digit.
    EPISODES_JSON=$(season_episodes 1 2 99)
    fresh_downloads
    set +e
    download_url 'https://www.ardplus.de/details/a0T01000002HaKM-sturm-der-liebe' 1
    status=$?
    set -e
    assert_equals 'a short season succeeds' '0' "$status"
    assert_equals 'a season below 100 episodes keeps two digits' \
        "Sturm.der.Liebe.S21.$tag/Sturm.der.Liebe.S21E01.$tag.mp4
Sturm.der.Liebe.S21.$tag/Sturm.der.Liebe.S21E02.$tag.mp4
Sturm.der.Liebe.S21.$tag/Sturm.der.Liebe.S21E99.$tag.mp4" \
        "$(produced_files "$downloads_dir")"
    unset EPISODES_JSON
}

test_download_url_loop() {
    printf '\n[download_url loop]\n'
    local tmp downloaded cases=0
    tmp=$(mktemp -d)
    downloaded="$tmp/downloaded.txt"
    content_result="$tmp/content.json"
    season_result=''
    tatort_result=''
    automatic_download=1
    batch_mode=0
    skip_existing_files=1
    file_tag='example'
    token='test-token'

    local series_details='{"data":{"movie":null,"series":{"title":"My Show","seasons":{"nodes":[{"id":"season-one","title":"Staffel 1","seasonInSeries":1},{"id":"season-two","title":"Staffel 2","seasonInSeries":2}]}}}}'
    local series_episodes='{"data":{"episodes":{"nodes":[{"id":"ep1","episodeInSeason":1,"title":"Erste","videoSource":{"dashUrl":"https://video/1"}},{"id":"ep2","episodeInSeason":2,"title":"Zweite","videoSource":{"dashUrl":"https://video/2"}},{"id":"ep3","episodeInSeason":3,"title":"Dritte","videoSource":{"dashUrl":"https://video/3"}},{"id":"ep4","episodeInSeason":4,"title":"Vierte","videoSource":{"dashUrl":"https://video/4"}}]}}}'
    local tag='German.AAC.1080p.Web-DL.x264-example'

    # Every case gets its own downloads directory so the previous run cannot
    # make the next one skip.
    fresh_downloads() {
        cases=$((cases + 1))
        downloads_dir="$tmp/downloads-$cases"
        mkdir -p "$downloads_dir"
        : >"$downloaded"
    }

    # Everything below the API boundary is stubbed; the download, the media
    # probe and the final naming are the real code paths.
    fetch_graphql() {
        case "$1" in
            MovieDetails) printf '%s' "$series_details" >"$4" ;;
            EpisodesInSeasonData) printf '%s' "$series_episodes" >"$4" ;;
            *) printf '{"errors":[{"message":"unexpected operation"}]}' >"$4" ;;
        esac
        echo 200
    }
    auth() { printf 'token=abc'; }
    cleanup() { :; }
    skip_if_exists() { return 1; }
    log_msg() { :; }
    run_yt_dlp() {
        local target
        printf '%s\n' "$2" >>"$downloaded"
        target=$(output_mp4_path "$2")
        mkdir -p "$(dirname "$target")"
        printf 'video' >"$target"
    }

    fresh_downloads
    download_url 'https://www.ardplus.de/details/a0S010000007GcX-my-show' 2
    assert_equals 'skip drops episodes of the first season only' \
"My.Show.S01.$tag/My.Show.S01E03.$tag.mp4
My.Show.S01.$tag/My.Show.S01E04.$tag.mp4
My.Show.S02.$tag/My.Show.S02E01.$tag.mp4
My.Show.S02.$tag/My.Show.S02E02.$tag.mp4
My.Show.S02.$tag/My.Show.S02E03.$tag.mp4
My.Show.S02.$tag/My.Show.S02E04.$tag.mp4" \
        "$(produced_files "$downloads_dir")"

    # Every episode of the first season is dropped when the skip exceeds the
    # season length, but later seasons keep starting at episode 1.
    fresh_downloads
    download_url 'https://www.ardplus.de/details/a0S010000007GcX-my-show' 4
    assert_equals 'skip beyond the season length empties the first season' \
"My.Show.S02.$tag/My.Show.S02E01.$tag.mp4
My.Show.S02.$tag/My.Show.S02E02.$tag.mp4
My.Show.S02.$tag/My.Show.S02E03.$tag.mp4
My.Show.S02.$tag/My.Show.S02E04.$tag.mp4" \
        "$(produced_files "$downloads_dir")"

    # The default skip of 1 keeps everything in every season.
    fresh_downloads
    download_url 'https://www.ardplus.de/details/a0S010000007GcX-my-show' 1
    assert_equals 'default skip keeps all eight episodes' '8' \
        "$(produced_files "$downloads_dir" | wc -l | tr -d ' ')"
    assert_equals 'the staging directories are removed' '0' \
        "$(find "$downloads_dir" -type d -name 'Season 0*' | wc -l | tr -d ' ')"

    # A series whose output exists is skipped without a download.
    : >"$downloaded"
    download_url 'https://www.ardplus.de/details/a0S010000007GcX-my-show' 1
    assert_equals 'an existing output is not downloaded again' '0' \
        "$(wc -l <"$downloaded" | tr -d ' ')"

    # A movie URL resolves to a single file instead of a season tree.
    fresh_downloads
    fetch_graphql() {
        case "$1" in
            MovieDetails)
                printf '{"data":{"movie":{"id":"m1","title":"Lola rennt","productionYear":1998,"customData":null,"videoSource":{"dashUrl":"https://video/movie"}},"series":null}}' >"$4"
                ;;
            *) printf '{"errors":[{"message":"unexpected operation"}]}' >"$4" ;;
        esac
        echo 200
    }
    download_url 'https://www.ardplus.de/details/a0S01000000EWYi-lola-rennt' 1
    assert_equals 'movie url downloads a single file' \
        "Lola.rennt.1998/Lola.rennt.1998.$tag.mp4" \
        "$(produced_files "$downloads_dir")"

    # A Tatort category page is parsed from ld+json; the city from the API
    # metadata forms the file name.
    fresh_downloads
    cat >"$tmp/curl" <<'STUB'
#!/usr/bin/env bash
printf '%s' '<html><head><script type="application/ld+json">{"itemListElement":[{"item":{"url":"https://www.ardplus.de/details/id850"}},{"item":{"url":"https://www.ardplus.de/details/id851"}}]}</script></head></html>'
STUB
    chmod +x "$tmp/curl"
    curlBin="$tmp/curl"

    fetch_graphql() {
        local id episode year title
        if [[ "$1" != "MovieDetails" ]]; then
            printf '{"errors":[{"message":"unexpected operation"}]}' >"$4"
            echo 200
            return
        fi
        id=$(jq -r '.movieId' <<<"$3")
        case "$id" in
            id850) episode=850; year=2019; title='Der Fall' ;;
            id851) episode=851; year=2020; title='Der zweite Fall' ;;
            *)
                printf '{"errors":[{"message":"unknown id %s"}]}' "$id" >"$4"
                echo 200
                return
                ;;
        esac
        jq -nc --arg id "$id" --arg title "$title" \
            --argjson year "$year" --argjson episode "$episode" \
            '{data:{movie:{id:$id,title:$title,productionYear:$year,customData:{location:"Köln",team:"Bucerius",episodeProductionNumber:$episode},videoSource:{dashUrl:"https://video/tatort"}},series:null}}' >"$4"
        echo 200
    }
    download_url 'https://www.ardplus.de/kategorie/tatort-koeln' 1
    assert_equals 'tatort category maps every listed episode' \
"Tatort.Köln.Folge.850.2019.$tag.mp4
Tatort.Köln.Folge.851.2020.$tag.mp4" \
        "$(produced_files "$downloads_dir")"
}

test_dots_component() {
    printf '\n[dots component]\n'
    assert_equals 'spaces become dots' 'My.Show' "$(dots_component 'My Show')"
    assert_equals 'slashes become dots' 'A.B' "$(dots_component 'A/B')"
    assert_equals 'runs of spaces collapse' 'A.B' "$(dots_component 'A   B')"
    assert_equals 'surrounding dots and spaces are trimmed' 'A.B' "$(dots_component '  ..A  B.. ')"
    assert_equals 'glob characters are removed' 'AB' "$(dots_component 'A*?[]B')"
    assert_equals 'umlauts are kept' 'Köln' "$(dots_component 'Köln')"
    assert_equals 'empty value becomes placeholder' '_' "$(dots_component '')"
}

test_media_labels() {
    printf '\n[media labels]\n'
    assert_equals '2160 maps to 2160p' '2160p' "$(resolution_label 2160)"
    assert_equals '1080 maps to 1080p' '1080p' "$(resolution_label 1080)"
    assert_equals 'unknown heights use thresholds' '1080p' "$(resolution_label 1084)"
    assert_equals 'heights below 480 keep their value' '300p' "$(resolution_label 300)"
    assert_equals 'missing height is unknown' 'Unknown' "$(resolution_label '')"

    assert_equals 'h264 becomes x264' 'x264' "$(video_codec_label h264)"
    assert_equals 'hevc becomes x265' 'x265' "$(video_codec_label hevc)"
    assert_equals 'av1 stays av1' 'AV1' "$(video_codec_label av1)"
    assert_equals 'unknown video codec is uppercased' 'MPEG4' "$(video_codec_label mpeg4)"
    assert_equals 'missing video codec is unknown' 'Unknown' "$(video_codec_label '')"

    assert_equals 'aac stays aac' 'AAC' "$(audio_codec_label aac 2 '')"
    assert_equals 'stereo ac3 has no channel suffix' 'AC3' "$(audio_codec_label ac3 2 '')"
    assert_equals 'surround ac3 carries the channel count' 'AC3.6ch' "$(audio_codec_label ac3 6 '')"
    assert_equals 'eac3 carries the channel count' 'EAC3.6ch' "$(audio_codec_label eac3 6 '')"
    assert_equals 'dts-hd ma is labelled' 'DTS-HD.MA' "$(audio_codec_label dts 8 'DTS-HD MA')"
    assert_equals 'plain dts is labelled' 'DTS' "$(audio_codec_label dts 6 'DTS')"
    assert_equals 'truehd is labelled' 'TrueHD' "$(audio_codec_label truehd 8 '')"
    assert_equals 'opus is labelled' 'Opus' "$(audio_codec_label opus 2 '')"
    assert_equals 'missing audio codec is unknown' 'Unknown' "$(audio_codec_label '' 2 '')"
}

test_output_name_stems() {
    printf '\n[output names]\n'
    file_tag='example'
    assert_equals 'episode base uses dots and padded numbers' \
        'My.Show.S01E03' "$(episode_name_stem 'My Show' 1 3)"
    assert_equals 'episode base pads two-digit seasons' \
        'My.Show.S12E07' "$(episode_name_stem 'My Show' 12 7)"
    assert_equals 'episode base keeps the placeholder' \
        'My.Show.S03E??' "$(episode_name_stem 'My Show' 3 '')"
    assert_equals 'episode base honour the padding argument' \
        'My.Show.S21E001' "$(episode_name_stem 'My Show' 21 1 3)"
    assert_equals 'the placeholder ignores the padding' \
        'My.Show.S21E??' "$(episode_name_stem 'My Show' 21 '' 3)"
    assert_equals 'flat numbering keeps two digits' '2' "$(episode_padding 99)"
    assert_equals 'the 100th episode switches to three digits' '3' "$(episode_padding 100)"
    assert_equals 'a long season uses three digits' '3' "$(episode_padding 151)"
    assert_equals 'a missing maximum uses two digits' '2' "$(episode_padding '')"
    assert_equals 'season stem appends the season number' \
        'My.Show.S01' "$(season_name_stem 'My Show' 1)"
    assert_equals 'movie stem joins title and year' \
        'Lola.rennt.1998' "$(movie_name_stem 'Lola rennt' 1998)"
    assert_equals 'movie stem without a year' \
        'Lola.rennt' "$(movie_name_stem 'Lola rennt' '')"
    assert_equals 'tatort base carries city, episode and year' \
        'Tatort.Köln.Folge.850.2019' "$(tatort_name_stem 'Köln' 850 2019)"
    assert_equals 'tatort base without a year' \
        'Tatort.Köln.Folge.850' "$(tatort_name_stem 'Köln' 850 '')"
    assert_equals 'tatort base without episode and year' \
        'Tatort.Köln' "$(tatort_name_stem 'Köln' '' '')"
    assert_equals 'the Datei-Tag is appended' \
        'German.AAC.1080p.Web-DL.x264-example' "$(apply_file_tag 'German.AAC.1080p.Web-DL.x264')"
}

test_probe_media_tag() {
    printf '\n[media probe]\n'
    local tmp file saved_ffprobe
    tmp=$(mktemp -d)
    file="$tmp/video.mp4"
    printf 'video' >"$file"
    saved_ffprobe=$(declare -f ffprobe)

    ffprobe() {
        printf '%s' "$PROBE_JSON"
        [[ -n "${PROBE_STATUS:-}" ]] && return "$PROBE_STATUS"
        return 0
    }
    export PROBE_JSON='{"streams":[{"codec_type":"video","codec_name":"hevc","height":2160},{"codec_type":"audio","codec_name":"aac","channels":2,"tags":{"language":"eng"}},{"codec_type":"audio","codec_name":"ac3","channels":6,"tags":{"language":"ger"}}]}'
    assert_equals 'the German audio track is preferred' \
        'German.AC3.6ch.2160p.Web-DL.x265' "$(probe_media_tag "$file")"

    export PROBE_JSON='{"streams":[{"codec_type":"video","codec_name":"h264","height":720}]}'
    assert_equals 'a missing audio track is reported as unknown' \
        'German.Unknown.720p.Web-DL.x264' "$(probe_media_tag "$file")"

    export PROBE_JSON=''
    PROBE_STATUS=1
    assert_failure 'ffprobe failure is reported' probe_media_tag "$file"

    unset PROBE_JSON
    unset PROBE_STATUS
    eval "$saved_ffprobe"
}

test_skip_or_prepare_output() {
    printf '\n[output skip handling]\n'
    local tmp saved_log
    tmp=$(mktemp -d)
    downloads_dir="$tmp/downloads"
    mkdir -p "$downloads_dir"
    file_tag='example'
    skip_existing_files=1
    saved_log=$(declare -f log_msg)
    log_msg() { :; }

    local tag='German.AAC.1080p.Web-DL.x264-example'
    local output_dir="$downloads_dir/My.Show.S01.$tag"
    mkdir -p "$output_dir"
    local staging='My Show/Season 01/My Show S01E01 - Erste'
    local legacy
    legacy=$(output_mp4_path "$staging")

    printf 'video' >"$output_dir/My.Show.S01E01.$tag.mp4"
    assert_success 'an existing output file is skipped' \
        skip_or_prepare_output "$output_dir/My.Show.S01E01.*" "$staging"

    rm -f "$output_dir/My.Show.S01E01.$tag.mp4"
    mkdir -p "$(dirname "$legacy")"
    printf 'video' >"$legacy"
    assert_success 'an existing legacy file is skipped' \
        skip_or_prepare_output "$output_dir/My.Show.S01E01.*" "$staging"

    rm -f "$legacy"
    mkdir -p "$output_dir/My.Show.S01E01.somedir"
    assert_failure 'a matching directory is not an output' \
        skip_or_prepare_output "$output_dir/My.Show.S01E01.*" "$staging"
    rmdir "$output_dir/My.Show.S01E01.somedir"
    assert_failure 'a missing output is downloaded' \
        skip_or_prepare_output "$output_dir/My.Show.S01E01.*" "$staging"

    printf 'video' >"$output_dir/My.Show.S01E01.$tag.mp4"
    printf 'video' >"$legacy"
    skip_existing_files=0
    assert_failure 'force redownload always downloads' \
        skip_or_prepare_output "$output_dir/My.Show.S01E01.*" "$staging"
    assert_failure 'force redownload removes the output file' \
        test -f "$output_dir/My.Show.S01E01.$tag.mp4"
    assert_failure 'force redownload removes the legacy file' test -f "$legacy"

    skip_existing_files=1
    eval "$saved_log"
}

test_finalize_output() {
    printf '\n[output finalize]\n'
    local tmp saved_log
    tmp=$(mktemp -d)
    downloads_dir="$tmp/downloads"
    mkdir -p "$downloads_dir"
    file_tag='example'
    saved_log=$(declare -f log_msg)
    log_msg() { :; }

    local tag='German.AAC.1080p.Web-DL.x264-example'
    local staging='My Show/Season 01/My Show S01E01 - Erste'
    local staged
    staged=$(output_mp4_path "$staging")
    mkdir -p "$(dirname "$staged")"
    printf 'video' >"$staged"

    assert_success 'finalize moves the download' \
        finalize_output "$staging" 'My.Show.S01E01' 'My.Show.S01' 1
    assert_equals 'the output file is created' \
        "My.Show.S01.$tag/My.Show.S01E01.$tag.mp4" \
        "$(produced_files "$downloads_dir")"
    assert_failure 'the staging file is gone' test -f "$staged"
    assert_failure 'the staging directory is gone' \
        test -d "$downloads_dir/My Show/Season 01"

    # An existing season directory is reused instead of creating a second one.
    mkdir -p "$downloads_dir/My.Show.S02.German.AAC.720p.Web-DL.x264-oldgroup"
    local staging2='My Show/Season 02/My Show S02E01 - Zweite'
    local staged2
    staged2=$(output_mp4_path "$staging2")
    mkdir -p "$(dirname "$staged2")"
    printf 'video' >"$staged2"
    assert_success 'finalize reuses the season directory' \
        finalize_output "$staging2" 'My.Show.S02E01' 'My.Show.S02' 1
    assert_success 'the file lands in the existing season directory' \
        test -f "$downloads_dir/My.Show.S02.German.AAC.720p.Web-DL.x264-oldgroup/My.Show.S02E01.$tag.mp4"

    # Without stream information the file keeps the staging name.
    local saved_ffprobe
    saved_ffprobe=$(declare -f ffprobe)
    ffprobe() { return 1; }
    local staging3='My Show/Season 03/My Show S03E01 - Dritte'
    local staged3
    staged3=$(output_mp4_path "$staging3")
    mkdir -p "$(dirname "$staged3")"
    printf 'video' >"$staged3"
    assert_success 'a failed probe is not an error' \
        finalize_output "$staging3" 'My.Show.S03E01' 'My.Show.S03' 1
    assert_success 'the file keeps the staging name without a probe' test -f "$staged3"
    eval "$saved_ffprobe"

    # Pathological titles stay below NAME_MAX once tag and group are appended.
    downloads_dir="$tmp/downloads-long"
    mkdir -p "$downloads_dir"
    file_tag='abcdefghijklmnop'
    local long_title staging4 staged4 long_file
    long_title=$(printf 'Titel %.0s' {1..60})
    staging4=$(build_episode_path "$long_title" 1 1 "$long_title")
    staged4=$(output_mp4_path "$staging4")
    mkdir -p "$(dirname "$staged4")"
    printf 'video' >"$staged4"
    assert_success 'a long title still finalizes' \
        finalize_output "$staging4" "$(episode_name_stem "$long_title" 1 1)" "$(season_name_stem "$long_title" 1)" 1
    long_file=$(find "$downloads_dir" -name '*.mp4' | LC_ALL=C sort | tail -n 1)
    if [[ -n "$long_file" && "$(bytes "$(basename "$long_file")")" -le 255 ]]; then
        assert_equals 'the output file name stays below NAME_MAX' 'ok' 'ok'
    else
        fail 'the output file name stays below NAME_MAX' "${long_file:-no file}"
    fi
    file_tag='example'

    # A container other than mp4 keeps its extension.
    downloads_dir="$tmp/downloads-mkv"
    mkdir -p "$downloads_dir"
    file_tag='example'
    local staging5
    staging5='My Show/Season 05/My Show S05E01 - Fuenfte'
    mkdir -p "$downloads_dir/My Show/Season 05"
    printf 'video' >"$(resolve_download_path "$staging5").mkv"
    assert_success 'a non-mp4 container is finalized' \
        finalize_output "$staging5" 'My.Show.S05E01' 'My.Show.S05' 1
    assert_equals 'the container extension is kept' \
        "My.Show.S05.$tag/My.Show.S05E01.$tag.mkv" \
        "$(produced_files "$downloads_dir")"

    assert_failure 'a missing download is reported' \
        finalize_output 'Missing/Season 01/Missing S01E01 - Nope' 'Missing.S01E01' 'Missing.S01' 1

    eval "$saved_log"
}

test_file_tag() {
    printf '\n[file tag]\n'
    local tmp
    tmp=$(mktemp -d)

    file_tag=''
    ARD_PLUS_TAG='' ARD_PLUS_CREDENTIALS_FILE="$tmp/none" resolve_credentials
    assert_equals 'no tag stays empty' '' "$file_tag"
    rg_quiet() { resolve_file_tag 2>/dev/null; }
    assert_failure 'a missing tag is rejected' rg_quiet </dev/null

    ARD_PLUS_TAG='envgroup' ARD_PLUS_CREDENTIALS_FILE="$tmp/none" resolve_credentials
    assert_equals 'environment provides the tag' 'envgroup' "$file_tag"
    assert_success 'a valid tag is accepted' resolve_file_tag

    printf 'tag=filetag\n' >"$tmp/credentials"
    chmod 600 "$tmp/credentials"
    file_tag=''
    ARD_PLUS_CREDENTIALS_FILE="$tmp/credentials" resolve_credentials
    assert_equals 'the credentials file provides the tag' 'filetag' "$file_tag"

    file_tag='bad/tag'
    assert_failure 'a tag with a slash is rejected' rg_quiet
    file_tag='bad group'
    assert_failure 'a tag with a space is rejected' rg_quiet
    file_tag='abcdefghijklmnopq'
    assert_failure 'an over-long tag is rejected' rg_quiet
    file_tag='ok.group-1_x'
    assert_success 'dots, dashes and underscores are allowed' resolve_file_tag
    assert_equals 'the missing tag message names the options' '1' \
        "$(file_tag=''; resolve_file_tag </dev/null 2>&1 | grep -c 'Datei-Tag missing')"
    file_tag=''
}

test_file_tag_cli() {
    printf '\n[file tag cli]\n'
    local script="$ROOT_DIR/ard-plus-dl.sh"
    local tmp
    tmp=$(mktemp -d)
    mkdir -p "$tmp/bin" "$tmp/home" "$tmp/cwd"
    printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/yt-dlp"
    printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/ffmpeg"
    printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/ffprobe"
    chmod +x "$tmp/bin/yt-dlp" "$tmp/bin/ffmpeg" "$tmp/bin/ffprobe"

    run_without_group() {
        cd "$tmp/cwd" && env -i HOME="$tmp/home" PATH="$tmp/bin:$PATH" "$@" </dev/null
    }
    assert_output_contains 'a missing tag is reported' 'Datei-Tag missing!' \
        run_without_group bash "$script" 'https://www.ardplus.de/details/foo'
    assert_exit 'a missing tag exits 1' 1 \
        run_without_group bash "$script" 'https://www.ardplus.de/details/foo'
    assert_output_contains 'an invalid tag is reported' 'Invalid Datei-Tag' \
        run_without_group env ARD_PLUS_TAG='bad/group' bash "$script" 'https://www.ardplus.de/details/foo'
}

test_cleanup_tmp() {
    printf '\n[cleanup_tmp]\n'
    content_result=$(mktemp)
    season_result=$(mktemp)
    tatort_result=$(mktemp)
    cleanup_tmp
    assert_failure 'removes content_result temp file' test -f "$content_result"
    assert_failure 'removes season_result temp file' test -f "$season_result"
    assert_failure 'removes tatort_result temp file' test -f "$tatort_result"
    content_result=''
    season_result=''
    tatort_result=''
    assert_success 'is a no-op with empty paths' cleanup_tmp
}

test_credentials_file() {
    printf '\n[credentials file]\n'
    local tmp
    tmp=$(mktemp -d)

    cat >"$tmp/credentials" <<'EOF'
# comment line
username=file-user

password=file-pass
EOF
    chmod 600 "$tmp/credentials"

    username=''
    password=''
    assert_success 'loads credentials file' load_credentials_file "$tmp/credentials"
    assert_equals 'parses username line' 'file-user' "$username"
    assert_equals 'parses password line' 'file-pass' "$password"

    username='arg-user'
    password='arg-pass'
    load_credentials_file "$tmp/credentials"
    assert_equals 'does not override existing username' 'arg-user' "$username"
    assert_equals 'does not override existing password' 'arg-pass' "$password"

    assert_failure 'fails on missing file' load_credentials_file "$tmp/does-not-exist"

    printf 'ARD_PLUS_USER=env-style-user\nARD_PLUS_PASSWORD=env-style-pass\n' >"$tmp/credentials-env-style"
    chmod 600 "$tmp/credentials-env-style"
    username=''
    password=''
    load_credentials_file "$tmp/credentials-env-style"
    assert_equals 'accepts ARD_PLUS_USER key' 'env-style-user' "$username"
    assert_equals 'accepts ARD_PLUS_PASSWORD key' 'env-style-pass' "$password"

    chmod 644 "$tmp/credentials"
    username=''
    password=''
    local warning
    warning=$(load_credentials_file "$tmp/credentials" 2>&1 >/dev/null)
    assert_output_contains 'warns about loose permissions' 'readable by group/others' \
        printf '%s' "$warning"
}

test_resolve_credentials() {
    printf '\n[resolve_credentials]\n'
    local tmp
    tmp=$(mktemp -d)

    username=''
    password=''
    ARD_PLUS_USER='env-user' ARD_PLUS_PASSWORD='env-pass' \
        ARD_PLUS_CREDENTIALS_FILE="$tmp/none" resolve_credentials
    assert_equals 'takes username from environment' 'env-user' "$username"
    assert_equals 'takes password from environment' 'env-pass' "$password"

    username='arg-user'
    password='arg-pass'
    ARD_PLUS_USER='env-user' ARD_PLUS_PASSWORD='env-pass' \
        ARD_PLUS_CREDENTIALS_FILE="$tmp/none" resolve_credentials
    assert_equals 'positional username wins over environment' 'arg-user' "$username"
    assert_equals 'positional password wins over environment' 'arg-pass' "$password"

    printf 'username=file-user\npassword=file-pass\n' >"$tmp/credentials"
    chmod 600 "$tmp/credentials"
    username=''
    password=''
    ARD_PLUS_CREDENTIALS_FILE="$tmp/credentials" resolve_credentials
    assert_equals 'falls back to credentials file' 'file-user' "$username"

    username=''
    password=''
    ARD_PLUS_USER='env-user' ARD_PLUS_PASSWORD='env-pass' \
        ARD_PLUS_CREDENTIALS_FILE="$tmp/credentials" resolve_credentials
    assert_equals 'environment wins over credentials file' 'env-user' "$username"
}

test_require_auth_source() {
    printf '\n[require_auth_source]\n'
    local tmp
    tmp=$(mktemp -d)

    token_file="$tmp/token"
    printf 'sometoken\n' >"$token_file"
    username=''
    password=''
    assert_success 'token-only run is allowed' require_auth_source

    token_file="$tmp/no-token"
    username='user'
    password='pass'
    assert_success 'credentials without token are allowed' require_auth_source

    username=''
    password=''
    assert_failure 'fails without token, credentials, or tty' \
        require_auth_source </dev/null

    assert_failure 'prompt fails without tty' prompt_credentials </dev/null
}

test_cli_validation() {
    printf '\n[cli validation]\n'
    local script="$ROOT_DIR/ard-plus-dl.sh"

    assert_exit 'help exits 0' 0 bash "$script" --help
    assert_output_contains 'help shows usage' 'Usage' bash "$script" --help
    assert_exit 'short help exits 0' 0 bash "$script" -h
    assert_output_contains 'short help shows usage' 'Usage' bash "$script" -h
    assert_exit 'unknown option exits 1' 1 bash "$script" --bogus 'https://www.ardplus.de/details/foo'
    assert_output_contains 'unknown option message' 'Unknown option' bash "$script" --bogus
    assert_exit 'missing url' 1 bash "$script"
    assert_output_contains 'missing url message' 'URL or --links-file required' \
        bash "$script" '' user pass
    assert_output_contains 'links-file requires argument' '--links-file requires a file argument' \
        bash "$script" --links-file
    assert_output_contains 'links file not found' 'Links file missing or not found' \
        bash "$script" --links-file "$FIXTURES_DIR/missing.txt" user pass
    assert_output_contains 'links file not found without credentials' 'Links file missing or not found' \
        bash "$script" --links-file "$FIXTURES_DIR/missing.txt"
    assert_output_contains 'rejects non-numeric skip' 'Invalid skip value' \
        bash "$script" 'https://www.ardplus.de/details/foo' user pass nonsense

    # End-to-end: no token, no credentials, no tty -> credentials error.
    # Stub yt-dlp and ffmpeg keep the dependency check happy in minimal environments.
    local tmp
    tmp=$(mktemp -d)
    mkdir -p "$tmp/bin" "$tmp/home" "$tmp/cwd"
    printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/yt-dlp"
    chmod +x "$tmp/bin/yt-dlp"
    printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/ffmpeg"
    printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/ffprobe"
    chmod +x "$tmp/bin/ffmpeg" "$tmp/bin/ffprobe"

    run_isolated() {
        cd "$tmp/cwd" && env -i HOME="$tmp/home" PATH="$tmp/bin:$PATH" ARD_PLUS_TAG='example' \
            bash "$script" "$@" </dev/null
    }
    assert_output_contains 'fails without any credential source' 'Credentials missing!' \
        run_isolated 'https://www.ardplus.de/details/foo'
    assert_exit 'exits 1 without any credential source' 1 \
        run_isolated 'https://www.ardplus.de/details/foo'
    assert_output_contains 'single extra argument is treated as username' 'Credentials missing!' \
        run_isolated 'https://www.ardplus.de/details/foo' someuser
    assert_exit 'username without password exits 1' 1 \
        run_isolated 'https://www.ardplus.de/details/foo' someuser
}

test_main_fail_fast() {
    printf '\n[main fail fast]\n'
    local script="$ROOT_DIR/ard-plus-dl.sh"
    local tmp
    tmp=$(mktemp -d)
    mkdir -p "$tmp/bin" "$tmp/home" "$tmp/cwd"
    printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/yt-dlp"
    printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/ffmpeg"
    printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/ffprobe"
    chmod +x "$tmp/bin/yt-dlp" "$tmp/bin/ffmpeg" "$tmp/bin/ffprobe"

    run_isolated_main() {
        cd "$tmp/cwd" && env -i HOME="$tmp/home" PATH="$tmp/bin:$PATH" ARD_PLUS_TAG='example' "$@" </dev/null
    }

    # The URL check runs before check_dependencies, so it needs no tooling.
    assert_output_contains 'invalid URL is rejected before dependency checks' \
        'Invalid URL: unsupported host: example.com' \
        run_isolated_main bash "$script" 'https://example.com/details/x'
    assert_exit 'invalid URL exits 1' 1 \
        run_isolated_main bash "$script" 'https://example.com/details/x'

    # A regular file where the downloads directory should be makes mkdir fail.
    : >"$tmp/blocker"
    assert_output_contains 'unwritable downloads directory is reported' \
        'Downloads directory is not writable' \
        run_isolated_main env DOWNLOADS_DIR="$tmp/blocker/downloads" \
            bash "$script" 'https://www.ardplus.de/details/x'
    assert_exit 'unwritable downloads directory exits 1' 1 \
        run_isolated_main env DOWNLOADS_DIR="$tmp/blocker/downloads" \
            bash "$script" 'https://www.ardplus.de/details/x'
}

test_batch_exit_code() {
    printf '\n[batch exit code]\n'
    local tmp dep
    tmp=$(mktemp -d)
    mkdir -p "$tmp/bin" "$tmp/home" "$tmp/cwd"
    printf 'https://www.ardplus.de/details/one\nhttps://www.ardplus.de/details/two\n' >"$tmp/links.txt"

    # Only stub what the host lacks; every real dependency stays usable.
    for dep in yt-dlp ffmpeg ffprobe; do
        if ! command -v "$dep" >/dev/null 2>&1; then
            printf '#!/bin/sh\nexit 0\n' >"$tmp/bin/$dep"
            chmod +x "$tmp/bin/$dep"
        fi
    done

    # main() runs in a subshell with download_url and ensure_token replaced, so
    # nothing touches the network; the subshell status is main()'s exit status.
    run_batch() {
        local download_status="$1"
        (
            # shellcheck source=../ard-plus-dl.sh
            source "$ROOT_DIR/ard-plus-dl.sh"
            set +e
            download_url() {
                DOWNLOAD_FAIL_REASON='stub failure'
                return "$download_status"
            }
            ensure_token() { :; }
            export ARD_PLUS_USER='u'
            export ARD_PLUS_PASSWORD='p'
            export ARD_PLUS_TAG='example'
            export HOME="$tmp/home"
            export XDG_STATE_HOME="$tmp/home/.local/state"
            export PATH="$tmp/bin:$PATH"
            cd "$tmp/cwd" || exit 1
            main --links-file "$tmp/links.txt" </dev/null
        )
    }

    assert_exit 'batch run exits 1 when a link fails' 1 run_batch 1
    assert_exit 'batch run exits 0 when all links succeed' 0 run_batch 0
}

main() {
    printf 'Running ard-plus-dl tests...\n'
    test_normalize_url
    test_decode_jwt_header
    test_resolve_token_file
    test_sanitize_path_component
    test_dots_component
    test_graphql_response_ok
    test_download_paths
    test_skip_logic
    test_build_path_functions
    test_output_name_stems
    test_media_labels
    test_probe_media_tag
    test_path_length_limit
    test_episode_tail_from
    test_session_payloads
    test_session_renewal
    test_run_yt_dlp_log_capture
    test_skip_or_prepare_output
    test_finalize_output
    test_cleanup_tmp
    test_credentials_file
    test_resolve_credentials
    test_file_tag
    test_require_auth_source
    test_cli_validation
    test_main_fail_fast
    test_file_tag_cli
    test_validate_content_url
    test_batch_exit_code
    # Must run before the download_url tests: they replace fetch_graphql.
    test_fetch_graphql_retry_policy
    test_download_url_unsupported_url
    test_download_url_episode_failures
    test_season_selection_validation
    test_episode_padding_in_downloads
    # Runs last: it replaces fetch_graphql/auth/cleanup/run_yt_dlp globally.
    test_download_url_loop

    printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
    [[ $FAIL -eq 0 ]]
}

main "$@"
