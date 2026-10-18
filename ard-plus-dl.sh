#!/bin/bash
set -u -o pipefail

# GraphQL documents for ARD Plus (POST, no persisted-query hashes). They are
# inline so a standalone copy of this script runs without any neighbouring
# file.
# shellcheck disable=SC2016
GRAPHQL_MOVIE_DETAILS='query MovieDetails($movieId: String!, $externalId: String!, $slug: String!, $potentialMovieId: String!) {
  movie: getMovieDetails(idList: [$movieId, $potentialMovieId, $externalId, $slug]) {
    id
    title
    productionYear
    customData
    videoSource {
      dashUrl
    }
  }
  series: getSeriesDetails(idList: [$movieId, $potentialMovieId, $externalId, $slug]) {
    title
    seasons: cmsSeasonsBySeriesId(orderBy: SEASON_IN_SERIES_ASC) {
      nodes {
        id
        title
        seasonInSeries
      }
    }
  }
}'

# shellcheck disable=SC2016
GRAPHQL_EPISODES_IN_SEASON='query EpisodesInSeasonData($seasonId: Guid!) {
  episodes: allCmsEpisodes(condition: { seasonId: $seasonId }, orderBy: EPISODE_IN_SEASON_ASC) {
    nodes {
      id
      episodeInSeason
      title
      videoSource {
        dashUrl
      }
    }
  }
}'

curlBin=''
# use snap curl version if your OS is outdated
#curlBin=/snap/bin/curl
token_file=''
automatic_download=0
batch_mode=0
links_file=''
DOWNLOAD_FAIL_REASON=''
skip_existing_files=1
curl_common_opts=(--connect-timeout 10 --max-time 120)
batch_failure_count=0
# Byte limit for a single path component. 200 bytes plus ".mp4" and yt-dlp's
# temporary suffixes stay below the 255-byte NAME_MAX of APFS/ext4/tmpfs.
path_component_max_bytes=200
# Stems are shorter than a plain component because the media tag and the
# file tag is appended: 170 + tag (<= 62) + ".mp4" stays below NAME_MAX.
stem_max_bytes=170
episode_failure_count=0
episode_failure_first_reason=''
session_renewed=0
# Datei-Tag: a self-chosen suffix appended to every output name. Mandatory.
file_tag=''

check_dependencies() {
    local dep missing=()
    for dep in curl jq yt-dlp ffmpeg ffprobe perl column; do
        if ! command -v "$dep" >/dev/null 2>&1; then
            missing+=("$dep")
        fi
    done
    if [[ ${#missing[@]} -gt 0 ]]; then
        printf 'Missing required dependencies: %s\n' "${missing[*]}" >&2
        exit 1
    fi
    curlBin=$(command -v curl)
}

log_msg() {
    printf '%s\n' "$1"
    if [[ $batch_mode -eq 1 ]]; then
        printf '%s\n' "$1" >> "$LOG_FILE"
    fi
}

graphql_response_ok() {
    local outfile="$1"
    local error_msg
    [[ -f "$outfile" ]] || return 1
    error_msg=$(jq -r '.errors[0].message // empty' "$outfile" 2>/dev/null)
    [[ -n "$error_msg" ]] && return 1
    jq -e '.data | values | length > 0' "$outfile" >/dev/null 2>&1
}

fetch_graphql() {
    local operation_name="$1"
    local query="$2"
    local variables_json="$3"
    local outfile="$4"
    local max_retries=5
    local attempt=1
    local status error_msg payload

    payload=$(jq -nc \
        --arg query "$query" \
        --argjson variables "$variables_json" \
        --arg operationName "$operation_name" \
        '{query: $query, variables: $variables, operationName: $operationName}')

    while [[ $attempt -le $max_retries ]]; do
        status=$("$curlBin" -s -o "$outfile" -w "%{http_code}" \
            "${curl_common_opts[@]}" \
            -X POST 'https://data.ardplus.de/ard/graphql' \
            -H 'authority: data.ardplus.de' \
            -H 'content-type: application/json' \
            -H "cookie: sid=$token" \
            -H 'origin: https://www.ardplus.de' \
            -H 'referer: https://www.ardplus.de/' \
            -H 'user-agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36' \
            --data-raw "$payload")

        if [[ "$status" == "200" ]] && graphql_response_ok "$outfile"; then
            echo "$status"
            return 0
        fi

        error_msg=$(jq -r '.errors[0].message // empty' "$outfile" 2>/dev/null)

        # A GraphQL error body on HTTP 200 is an API answer, not a transport
        # problem: retrying only wastes the backoff the same way every time.
        if [[ "$status" == "200" && -n "$error_msg" ]]; then
            log_msg "GraphQL request failed permanently (HTTP 200, error: ${error_msg}), not retrying" >&2
            return 1
        fi

        log_msg "GraphQL request failed (attempt ${attempt}/${max_retries}, HTTP ${status}, error: ${error_msg:-empty data}), retrying..." >&2

        if [[ $attempt -lt $max_retries ]]; then
            sleep $((2 ** (attempt - 1)))
        fi
        attempt=$((attempt + 1))
    done

    return 1
}

resolve_download_path() {
    echo "${downloads_dir}/${1}"
}

output_mp4_path() {
    echo "$(resolve_download_path "$1").mp4"
}

has_incomplete_artifacts() {
    local filename="$1"
    local output_path dir base
    output_path=$(output_mp4_path "$filename")
    dir=$(dirname "$output_path")
    base=$(basename "$output_path" .mp4)

    # *.part* is only a resumable download (yt-dlp continues it with
    # --continue), so it does not mark the target as incomplete anymore.
    compgen -G "${dir}/${base}"*.ytdl > /dev/null && return 0
    [[ -f "${output_path}.ytdl" ]] && return 0
    compgen -G "${dir}/${base}".f[0-9]* > /dev/null && return 0
    return 1
}

cleanup_incomplete_files() {
    local filename="$1"
    local output_path dir base f
    output_path=$(output_mp4_path "$filename")
    dir=$(dirname "$output_path")
    base=$(basename "$output_path" .mp4)

    # *.part* files are kept on purpose: yt-dlp resumes them with --continue.
    shopt -s nullglob
    for f in "${dir}/${base}"*.ytdl "${output_path}.ytdl"; do
        [[ -e "$f" ]] || continue
        log_msg "Removing yt-dlp metadata file: ${f}"
        rm -f -- "$f"
    done
    for f in "${dir}/${base}".f[0-9]*; do
        [[ -e "$f" ]] || continue
        log_msg "Removing yt-dlp fragment: ${f}"
        rm -f -- "$f"
    done
    shopt -u nullglob
}

is_complete_file() {
    local filename="$1"
    local output_path
    output_path=$(output_mp4_path "$filename")

    [[ -f "$output_path" && -s "$output_path" ]] || return 1
    has_incomplete_artifacts "$filename" && return 1
    return 0
}

skip_if_exists() {
    local filename="$1"
    local output_path dir base f
    output_path=$(output_mp4_path "$filename")
    dir=$(dirname "$output_path")
    base=$(basename "$output_path" .mp4)

    if [[ $skip_existing_files -eq 1 ]]; then
        if [[ -f "$output_path" && -s "$output_path" ]] && has_incomplete_artifacts "$filename"; then
            cleanup_incomplete_files "$filename"
            log_msg "SKIP (already exists): ${output_path}"
            return 0
        fi
        if is_complete_file "$filename"; then
            log_msg "SKIP (already exists): ${output_path}"
            return 0
        fi

        # Stale yt-dlp bookkeeping files are dropped, *.part* is kept so
        # yt-dlp can continue the interrupted download.
        cleanup_incomplete_files "$filename"
        if [[ -f "$output_path" ]]; then
            log_msg "Incomplete file, re-downloading: ${output_path}"
        fi
        return 1
    fi

    # --force-redownload: drop the finished file and every partial artifact
    # of the target so it is fetched from scratch.
    if [[ -f "$output_path" ]]; then
        log_msg "Force re-download, removing: ${output_path}"
    fi
    rm -f -- "$output_path"
    shopt -s nullglob
    for f in "${dir}/${base}"*.part*; do
        rm -f -- "$f"
    done
    shopt -u nullglob
    cleanup_incomplete_files "$filename"
    return 1
}

normalize_url() {
    local url="$1"
    url="${url%%#*}"
    url="${url%%\?*}"
    url="${url%"${url##*[![:space:]]}"}"
    url="${url#"${url%%[![:space:]]*}"}"
    url="${url%/}"
    echo "$url"
}

sanitize_path_component() {
    local s="${1//\// }"
    s="${s//[[:cntrl:]]/}"
    s="${s//\*/}"
    s="${s//\?/}"
    s="${s//\[/}"
    s="${s//\]/}"
    s=$(trim_path_component "$s")
    [[ -z "$s" ]] && s='_'
    printf '%s' "$s"
}

# Trims leading/trailing spaces and dots and collapses inner space runs,
# without removing any other characters. Used on composed path components,
# where sanitize_path_component has already been applied to the parts.
trim_path_component() {
    local s="$1" lead trail
    # Trimming repeats: removing a dot can expose a space, and the other way round.
    while [[ "$s" == [[:space:]]* || "$s" == .* || "$s" == *[[:space:]] || "$s" == *. ]]; do
        lead="${s%%[![:space:]]*}"
        s="${s#"$lead"}"
        trail="${s##*[![:space:]]}"
        s="${s%"$trail"}"
        [[ "$s" == .* ]] && s="${s#.}"
        [[ "$s" == *. ]] && s="${s%.}"
    done
    while [[ "$s" == *"  "* ]]; do
        s="${s//  / }"
    done
    printf '%s' "$s"
}

# cap_component_bytes <max-bytes> <value>
# Truncates on a character boundary so the result stays valid UTF-8.
cap_component_bytes() {
    local max="$1" s="$2" cut
    if [[ "$(printf '%s' "$s" | wc -c | tr -d ' ')" -le "$max" ]]; then
        printf '%s' "$s"
        return 0
    fi
    cut="${s:0:max}"
    while [[ "$(printf '%s' "$cut" | wc -c | tr -d ' ')" -gt "$max" ]]; do
        cut="${cut%?}"
    done
    printf '%s' "$cut"
}

# Output paths (relative to downloads_dir) for the three content types. The
# builders take the raw API strings and sanitise them themselves, so the
# on-disk layout has exactly one definition that the tests can call.

# build_movie_path <title> [year]
build_movie_path() {
    local safe
    safe=$(sanitize_path_component "$1")
    # Trimming again cleans up a trailing space or dot exposed by truncation.
    safe=$(trim_path_component "$(cap_component_bytes "$path_component_max_bytes" "$safe")")
    if [[ -n "$2" ]]; then
        printf '%s (%s)/%s' "$safe" "$2" "$safe"
    else
        printf '%s/%s' "$safe" "$safe"
    fi
}

# build_episode_path <series title> <season> [episode no] <episode title>
build_episode_path() {
    local safe_show safe_title season episode_label stem
    safe_show=$(sanitize_path_component "$1")
    # The series title is the Season directory name, so it needs the same cap.
    safe_show=$(trim_path_component "$(cap_component_bytes "$path_component_max_bytes" "$safe_show")")
    safe_title=$(sanitize_path_component "$4")
    season=$(printf '%02d' "$2")
    if [[ -n "$3" ]]; then
        episode_label=$(printf '%02d' "$3")
    else
        episode_label='??'
    fi
    stem="${safe_show} S${season}E${episode_label} - ${safe_title}"
    # Trimming again cleans up a trailing space or dot exposed by truncation.
    stem=$(trim_path_component "$(cap_component_bytes "$path_component_max_bytes" "$stem")")
    printf '%s/Season %s/%s' "$safe_show" "$season" "$stem"
}

# build_tatort_path <city> [team] [episode no] <title> [year]
build_tatort_path() {
    local safe_city safe_team safe_name out
    safe_city=$(sanitize_path_component "$1")
    safe_team=$(sanitize_path_component "$2")
    safe_name=$(sanitize_path_component "$4")

    out=$(printf 'Tatort %s' "$safe_city")
    [[ -n "$2" ]] && out="${out} (${safe_team})"
    [[ -n "$3" ]] && out="${out} - Folge $3"
    if [[ -n "$5" ]]; then
        out="${out} - ${safe_name} ($5)"
    else
        out="${out} - ${safe_name}"
    fi
    # The whole Tatort name is a single path component, so the limit applies to
    # the composed string. Trimming again cleans up the truncation edge.
    printf '%s' "$(trim_path_component "$(cap_component_bytes "$path_component_max_bytes" "$out")")"
}

# Output-name helpers: the title has its spaces replaced by dots, media tags
# come from ffprobe and every name ends with the Datei-Tag.

# dots_component <value>
dots_component() {
    local s
    s=$(sanitize_path_component "$1")
    s="${s// /.}"
    while [[ "$s" == *..* ]]; do
        s="${s//../.}"
    done
    s=$(trim_path_component "$s")
    [[ -z "$s" ]] && s='_'
    printf '%s' "$s"
}

resolution_label() {
    local height="${1:-0}"
    if [[ ! "$height" =~ ^[0-9]+$ ]]; then
        printf 'Unknown'
        return 0
    fi
    case "$height" in
        2160) printf '2160p' ;;
        1440) printf '1440p' ;;
        1080) printf '1080p' ;;
        720) printf '720p' ;;
        576) printf '576p' ;;
        480) printf '480p' ;;
        360) printf '360p' ;;
        *)
            if [[ "$height" -ge 2160 ]]; then
                printf '2160p'
            elif [[ "$height" -ge 1440 ]]; then
                printf '1440p'
            elif [[ "$height" -ge 1080 ]]; then
                printf '1080p'
            elif [[ "$height" -ge 720 ]]; then
                printf '720p'
            elif [[ "$height" -ge 576 ]]; then
                printf '576p'
            elif [[ "$height" -ge 480 ]]; then
                printf '480p'
            elif [[ "$height" -gt 0 ]]; then
                printf '%sp' "$height"
            else
                printf 'Unknown'
            fi
            ;;
    esac
}

video_codec_label() {
    local codec="${1:-}"
    case "$codec" in
        h264) printf 'x264' ;;
        hevc) printf 'x265' ;;
        av1) printf 'AV1' ;;
        '') printf 'Unknown' ;;
        *) printf '%s' "$codec" | tr '[:lower:]' '[:upper:]' ;;
    esac
}

audio_codec_label() {
    local codec="${1:-}" channels="${2:-0}" profile="${3:-}" label
    case "$codec" in
        aac) label='AAC' ;;
        ac3) label='AC3' ;;
        eac3) label='EAC3' ;;
        dts)
            case "$profile" in
                *MA*) label='DTS-HD.MA' ;;
                *HRA*) label='DTS-HD.HRA' ;;
                *) label='DTS' ;;
            esac
            ;;
        truehd) label='TrueHD' ;;
        opus) label='Opus' ;;
        mp3) label='MP3' ;;
        flac) label='FLAC' ;;
        '') label='Unknown' ;;
        *) label=$(printf '%s' "$codec" | tr '[:lower:]' '[:upper:]') ;;
    esac
    if [[ "$channels" =~ ^[0-9]+$ ]] && [[ "$channels" -gt 2 ]] && { [[ "$label" == 'AC3' ]] || [[ "$label" == 'EAC3' ]]; }; then
        label="${label}.${channels}ch"
    fi
    printf '%s' "$label"
}

# probe_media_tag <file>
# Prints "German.<audio>.<resolution>.Web-DL.<video>" for a finished file.
# The German audio track is preferred; otherwise the first audio track is used.
probe_media_tag() {
    local file="$1" json video audio
    local vcodec height acodec channels profile
    json=$(ffprobe -v error \
        -show_entries stream=codec_type,codec_name,profile,height,channels:stream_tags=language \
        -of json "$file" 2>/dev/null) || return 1
    [[ -n "$json" ]] || return 1

    video=$(printf '%s' "$json" | jq -r '
        [.streams[]? | select(.codec_type == "video")][0] // {}
        | "\(.codec_name // "")|\(.height // 0)"')
    audio=$(printf '%s' "$json" | jq -r '
        [.streams[]? | select(.codec_type == "audio")] as $tracks
        | (($tracks | map(select((.tags.language // "") | test("^(ger|deu|de)$"; "i"))) | .[0])
           // $tracks[0] // {})
        | "\(.codec_name // "")|\(.channels // 0)|\(.profile // "")"')

    IFS='|' read -r vcodec height <<<"$video"
    IFS='|' read -r acodec channels profile <<<"$audio"

    printf 'German.%s.%s.Web-DL.%s' \
        "$(audio_codec_label "$acodec" "$channels" "$profile")" \
        "$(resolution_label "$height")" \
        "$(video_codec_label "$vcodec")"
}

# apply_file_tag <media-tag>
apply_file_tag() {
    printf '%s-%s' "$1" "$file_tag"
}

# episode_padding <highest episode number in the season>
# Seasons with 100 or more episodes are numbered with three digits
# (S21E001 ... S21E151), like the rename scripts.
episode_padding() {
    if [[ "${1:-}" =~ ^[0-9]+$ ]] && [[ "$1" -ge 100 ]]; then
        printf '3'
    else
        printf '2'
    fi
}

# episode_name_stem <show> <season> [episode no] [padding]
episode_name_stem() {
    local show season label padding="${4:-2}"
    show=$(dots_component "$1")
    season=$(printf '%02d' "$2")
    if [[ -n "${3:-}" ]]; then
        label=$(printf '%0*d' "$padding" "$3")
    else
        label='??'
    fi
    cap_component_bytes "$stem_max_bytes" "${show}.S${season}E${label}"
}

# season_name_stem <show> <season>
season_name_stem() {
    local stem
    stem="$(dots_component "$1").S$(printf '%02d' "$2")"
    cap_component_bytes "$stem_max_bytes" "$stem"
}

# movie_name_stem <title> [year]
movie_name_stem() {
    local stem
    stem=$(dots_component "$1")
    [[ -n "${2:-}" ]] && stem="${stem}.$2"
    cap_component_bytes "$stem_max_bytes" "$stem"
}

# tatort_name_stem <city> [episode no] [year]
tatort_name_stem() {
    local base
    base="Tatort.$(dots_component "$1")"
    [[ -n "${2:-}" ]] && base="${base}.Folge.${2}"
    [[ -n "${3:-}" ]] && base="${base}.${3}"
    cap_component_bytes "$stem_max_bytes" "$base"
}

# has_output <glob>
# Only regular, non-empty files count; a matching directory is not an output.
has_output() {
    local f
    while IFS= read -r f; do
        [[ -f "$f" && -s "$f" ]] && return 0
    done < <(compgen -G "$1")
    return 1
}

# remove_outputs <glob>
remove_outputs() {
    local f
    while IFS= read -r f; do
        [[ -f "$f" ]] || continue
        log_msg "Removing previous output: ${f}"
        rm -f -- "$f"
    done < <(compgen -G "$1")
}

# skip_or_prepare_output <output-glob> <staging-filename>
# Returns 0 when the item is already present. Otherwise cleans up for a fresh
# download and returns 1. An empty glob disables the output-name check.
skip_or_prepare_output() {
    local output_glob="$1" staging="$2"
    if [[ $skip_existing_files -eq 0 ]]; then
        [[ -n "$output_glob" ]] && remove_outputs "$output_glob"
        skip_if_exists "$staging"
        return 1
    fi
    if [[ -n "$output_glob" ]] && has_output "$output_glob"; then
        log_msg "SKIP (already exists): ${output_glob}"
        return 0
    fi
    # A file in the previous naming scheme still counts as present so an
    # existing library is not downloaded again.
    skip_if_exists "$staging"
}

# first_output_dir <dir-stem>
first_output_dir() {
    local f
    while IFS= read -r f; do
        if [[ -d "$f" ]]; then
            basename "$f"
            return 0
        fi
    done < <(compgen -G "${downloads_dir}/${1}.*")
    return 1
}

# finalize_output <staging-filename> <base-stem> <dir-stem> <dir-uses-tag>
# Probes the finished download and moves it to its final name. When
# ffprobe cannot report the streams the file keeps the staging name.
finalize_output() {
    local staging="$1" base="$2" dir_stem="$3" dir_uses_tag="$4"
    local staging_base staging_path ext tag dir target stale_dir candidate
    staging_base=$(resolve_download_path "$staging")
    # yt-dlp writes <target>.mp4 for the merged mp4; any other container it
    # produces is picked up here and keeps its extension.
    staging_path=''
    if [[ -f "${staging_base}.mp4" ]]; then
        staging_path="${staging_base}.mp4"
    else
        while IFS= read -r candidate; do
            if [[ -f "$candidate" ]]; then
                staging_path="$candidate"
                break
            fi
        done < <(compgen -G "${staging_base}.*")
    fi
    if [[ -z "$staging_path" ]]; then
        log_msg "Download finished without a file: ${staging_base}"
        return 1
    fi
    ext=".${staging_path##*.}"
    if ! tag=$(probe_media_tag "$staging_path"); then
        log_msg "WARNUNG: Medieninformationen nicht ermittelbar, Datei behaelt den bisherigen Namen: ${staging_path}"
        return 0
    fi
    tag=$(apply_file_tag "$tag")
    dir=''
    if [[ -n "$dir_stem" ]]; then
        if [[ "$dir_uses_tag" -eq 1 ]]; then
            dir=$(first_output_dir "$dir_stem") || dir="${dir_stem}.${tag}"
        else
            dir="$dir_stem"
        fi
    fi
    if [[ -n "$dir" ]]; then
        target="${downloads_dir}/${dir}/${base}.${tag}${ext}"
    else
        target="${downloads_dir}/${base}.${tag}${ext}"
    fi
    if [[ "$target" == "$staging_path" ]]; then
        return 0
    fi
    mkdir -p "$(dirname "$target")"
    if ! mv -f -- "$staging_path" "$target"; then
        log_msg "Could not move ${staging_path} to ${target}"
        return 1
    fi
    log_msg "Benannt: ${target}"
    # Drop the staging directories when nothing else lives there.
    stale_dir=$(dirname "$staging_path")
    while [[ "$stale_dir" != "$downloads_dir" && -d "$stale_dir" ]]; do
        rmdir "$stale_dir" 2>/dev/null || break
        stale_dir=$(dirname "$stale_dir")
    done
    return 0
}

# Number of leading episodes to drop, as fed to `tail -n +…`.
# $1 = skip count, $2 = 1 while the skip is still to be applied (first season).
# A skip of 1 is the "do not skip" default, so it is not special-cased by the caller.
episode_tail_from() {
    if [[ "$1" != "1" && "$2" -eq 1 ]]; then
        echo $(($1 + 1))
    else
        echo 1
    fi
}

credentials_file_default() {
    printf '%s' "${XDG_CONFIG_HOME:-${HOME:-}/.config}/ard-plus-dl/credentials"
}

load_credentials_file() {
    local file="$1" line key value perms
    [[ -n "$file" && -f "$file" && -r "$file" ]] || return 1
    perms=$(stat -c '%a' "$file" 2>/dev/null || stat -f '%Lp' "$file" 2>/dev/null || true)
    if [[ -n "$perms" && ! "$perms" =~ 00$ ]]; then
        echo "Warning: credentials file ${file} is readable by group/others (chmod 600 recommended)." >&2
    fi
    while IFS= read -r line || [[ -n "$line" ]]; do
        line="${line%$'\r'}"
        [[ -z "$line" || "$line" == \#* ]] && continue
        key="${line%%=*}"
        value="${line#*=}"
        case "$key" in
            username|ARD_PLUS_USER)
                [[ -z "$username" ]] && username="$value"
                ;;
            password|ARD_PLUS_PASSWORD)
                [[ -z "$password" ]] && password="$value"
                ;;
            tag|ARD_PLUS_TAG)
                [[ -z "$file_tag" ]] && file_tag="$value"
                ;;
        esac
    done < "$file"
    return 0
}

# Fill in missing credentials from (in order): positional arguments (already
# assigned), environment variables, credentials file.
resolve_credentials() {
    if [[ -z "$username" ]]; then
        username="${ARD_PLUS_USER:-}"
    fi
    if [[ -z "$password" ]]; then
        password="${ARD_PLUS_PASSWORD:-}"
    fi
    if [[ -z "$file_tag" ]]; then
        file_tag="${ARD_PLUS_TAG:-}"
    fi
    if [[ -z "$username" || -z "$password" || -z "$file_tag" ]]; then
        load_credentials_file "${ARD_PLUS_CREDENTIALS_FILE:-$(credentials_file_default)}" || true
    fi
}

# The Datei-Tag is mandatory: it is part of every output file name. It is
# resolved like the credentials (environment, credentials file) and prompted
# for interactively when neither provided it.
resolve_file_tag() {
    if [[ -z "$file_tag" && $batch_mode -eq 0 && -t 0 ]]; then
        printf '%s' "Datei-Tag (z. B. example): "
        read -r file_tag || file_tag=''
    fi
    if [[ -z "$file_tag" ]]; then
        echo "Datei-Tag missing! Set ARD_PLUS_TAG, add 'tag=...' to $(credentials_file_default), or answer the prompt." >&2
        return 1
    fi
    if [[ ! "$file_tag" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,15}$ ]]; then
        echo "Invalid Datei-Tag: ${file_tag} (allowed: letters, digits, '.', '_', '-')" >&2
        return 1
    fi
    return 0
}

prompt_credentials() {
    [[ -t 0 ]] || return 1
    if [[ -z "$username" ]]; then
        read -r -p "ARD Plus username: " username
    fi
    if [[ -z "$password" ]]; then
        read -rs -p "ARD Plus password: " password
        echo
    fi
    [[ -n "$username" && -n "$password" ]]
}

# A run can proceed if a session token already exists, credentials are known,
# or we can still prompt for them interactively.
require_auth_source() {
    [[ -f "$token_file" ]] && return 0
    [[ -n "$username" && -n "$password" ]] && return 0
    [[ -t 0 ]] && return 0
    return 1
}

resolve_token_file() {
    local state_dir="${XDG_STATE_HOME:-${HOME:-}/.local/state}/ard-plus-dl"
    local token_dir
    # umask 077 keeps a freshly created directory private; the chmod below is
    # for a state directory that already exists with looser permissions.
    if [[ -n "${HOME:-}" ]] && (umask 077 && mkdir -p "$state_dir") 2>/dev/null; then
        chmod 700 "$state_dir" 2>/dev/null
        token_file="${state_dir}/token"
    else
        token_file="${work_dir}/.ard-plus-dl/token"
        token_dir=$(dirname "$token_file")
        (umask 077 && mkdir -p "$token_dir")
        chmod 700 "$token_dir" 2>/dev/null
    fi
}

decode_jwt_header() {
    local jwt="$1" header padded mod
    header="${jwt%%.*}"
    [[ -z "$header" ]] && return 1
    padded=$(printf '%s' "$header" | tr '_-' '/+')
    mod=$((${#padded} % 4))
    if [[ $mod -eq 2 ]]; then
        padded="${padded}=="
    elif [[ $mod -eq 3 ]]; then
        padded="${padded}="
    fi
    printf '%s' "$padded" | base64 -d 2>/dev/null | jq -r '.typ // empty'
}

write_token_file() {
    local saved_umask tmp_file token_dir
    saved_umask=$(umask)
    umask 077
    token_dir=$(dirname "$token_file")
    mkdir -p "$token_dir"
    chmod 700 "$token_dir" 2>/dev/null
    # Write to a temporary file in the same directory and move it into place
    # so the token file is never read half-written.
    tmp_file="${token_file}.tmp.$$"
    printf '%s\n' "$token" >"$tmp_file"
    chmod 600 "$tmp_file" 2>/dev/null
    mv -f "$tmp_file" "$token_file"
    chmod 600 "$token_file" 2>/dev/null
    umask "$saved_umask"
}

record_success() {
    echo "$1" >> "$SUCCESS_FILE"
    log_msg "Recorded success: $1"
}

record_failure() {
    local url="$1"
    local reason="$2"
    reason="${reason//$'\t'/ }"
    reason="${reason//$'\n'/ }"
    printf '%s\t%s\n' "$url" "$reason" >> "$FAILED_FILE"
    log_msg "Recorded failure: $url ($reason)"
    batch_failure_count=$((batch_failure_count + 1))
}

# A single failing episode must not abort the remaining ones: failures are
# counted here and summarised once the episode loop has finished.
record_episode_failure() {
    local reason="$1"
    episode_failure_count=$((episode_failure_count + 1))
    [[ -z "$episode_failure_first_reason" ]] && episode_failure_first_reason="$reason"
    log_msg "FEHLER: $reason"
    return 0
}

run_yt_dlp() {
    local downloadUrl="$1"
    local filename="$2"
    local label="$3"
    local output_path log_out='' status
    local -a ytdlp_args
    output_path=$(resolve_download_path "$filename")
    ytdlp_args=(--quiet --progress --no-warnings --audio-multistreams \
        -f "bv+mergeall[vcodec=none]" --sub-langs "en.*,de.*" --embed-subs \
        --merge-output-format mp4)
    if [[ $skip_existing_files -eq 0 ]]; then
        ytdlp_args+=(--force-overwrites)
    fi
    # The URL goes in over stdin: a playback URL can start with "-" and must
    # not be parsed as an option. In batch mode yt-dlp's stderr is captured, so
    # the reason a download failed reaches the log file.
    if [[ $batch_mode -eq 1 ]]; then
        log_out=$(mktemp)
        printf '%s\n' "$downloadUrl" | yt-dlp "${ytdlp_args[@]}" -a - -o "$output_path" 2>"$log_out"
        status=$?
    else
        printf '%s\n' "$downloadUrl" | yt-dlp "${ytdlp_args[@]}" -a - -o "$output_path"
        status=$?
    fi
    if [[ $status -ne 0 ]]; then
        DOWNLOAD_FAIL_REASON="yt-dlp download failed for ${label}"
        if [[ -n "$log_out" ]]; then
            log_msg "yt-dlp output for ${label}:"
            # Progress is written with carriage returns, so normalise it and
            # keep only the tail to bound the log size.
            tr '\r' '\n' <"$log_out" | grep -v '^$' | tail -n 20 | while IFS= read -r line; do
                log_msg "  ${line}"
            done
        fi
        [[ -n "$log_out" ]] && rm -f "$log_out"
        return 1
    fi
    [[ -n "$log_out" ]] && rm -f "$log_out"
    return 0
}

# login only if necessary
# Returns 1 instead of exiting so a failed renewal mid-run can be handled by
# the caller.
login() {
    if [[ -z "$username" || -z "$password" ]]; then
        if ! prompt_credentials; then
            echo "No valid session token and no credentials available." >&2
            echo "Set ARD_PLUS_USER / ARD_PLUS_PASSWORD, create $(credentials_file_default)," >&2
            echo "or pass username and password as arguments." >&2
            return 1
        fi
    fi
    encoded_username=$(printf %s "$username" | jq -s -R -r @uri)
    encoded_password=$(printf %s "$password" | jq -s -R -r @uri)
    # The password goes in over stdin (--data @-): it must not show up in the
    # process list.
    token=$(printf 'username=%s&password=%s' "$encoded_username" "$encoded_password" | "$curlBin" -is "${curl_common_opts[@]}" 'https://auth.ardplus.de/auth/login?plainRedirect=true&redirectURL=https%3A%2F%2Fwww.ardplus.de%2Flogin%2Fcallback&errorRedirectURL=https%3A%2F%2Fwww.ardplus.de%2Fanmeldung%3Ferror%3Dtrue' \
    -H 'authority: auth.ardplus.de' \
    -H 'content-type: application/x-www-form-urlencoded' \
    -H 'origin: https://www.ardplus.de' \
    -H 'referer: https://www.ardplus.de/' \
    -H 'user-agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36' \
    --data @- | grep -i '^authorization:' | head -n 1 | awk '{print $3}' | tr -d '\r')
    token=$(printf '%s' "$token" | tr -d '\r')
    tokenType=$(decode_jwt_header "$token")
    if [[ "$tokenType" == "JWT" ]]; then
        write_token_file
    else
        echo "Login not possible! Please check credentials and subscription for user $username." >&2
        return 1
    fi
}

# Deletes the stored token and logs in again. Returns 1 when the new login
# fails; the caller decides whether that ends the run.
renew_session() {
    log_msg "Session abgelaufen oder ungültig, neuer Login..."
    rm -f "$token_file"
    login || return 1
    token=$(<"$token_file")
    token=$(printf '%s' "$token" | tr -d '\r\n')
    return 0
}

# Prints the playback URL parameters. When the API rejects the stored session,
# the login is renewed once before giving up.
ensure_playback_authorization() {
    local url_param
    url_param=$(auth)
    if [[ -n "$url_param" && "$url_param" != null ]]; then
        session_renewed=0
        printf '%s' "$url_param"
        return 0
    fi
    if [[ $session_renewed -eq 1 ]]; then
        return 1
    fi
    renew_session || return 1
    session_renewed=1
    url_param=$(auth)
    [[ -n "$url_param" && "$url_param" != null ]] || return 1
    printf '%s' "$url_param"
}

# cleanup after each episode and at the end
cleanup() {
    [[ -n "$movieId" && -n "$contentType" ]] || return 0
    local payload
    payload=$(jq -nc \
        --arg contentId "$movieId" \
        --arg contentType "$contentType" \
        '{contentId: $contentId, contentType: $contentType}')
    "$curlBin" -s "${curl_common_opts[@]}" 'https://token.ardplus.de/token/session/playback/delete' \
    -H 'authority: token.ardplus.de' \
    -H 'content-type: application/json' \
    -H "cookie: sid=$token" \
    -H 'origin: https://www.ardplus.de' \
    -H 'referer: https://www.ardplus.de/' \
    -H 'user-agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36' \
    --data-raw "$payload" \
    --compressed >/dev/null
}

# get authorization for content
auth() {
    [[ -n "$movieId" && -n "$contentType" ]] || { echo ""; return 0; }
    local payload auth_response urlParam
    payload=$(jq -nc \
        --arg contentId "$movieId" \
        --arg contentType "$contentType" \
        '{
            contentId: $contentId,
            contentType: $contentType,
            download: false,
            appInfo: {platform: "web", appVersion: "1.0.0", build: "web", bundleIdentifier: "web"},
            deviceInfo: {
                isTouchDevice: false,
                isTablet: false,
                isFireOS: false,
                appPlatform: "web",
                isIOS: false,
                isCastReceiver: false,
                isSafari: false,
                isFirefox: false
            }
        }')
    auth_response=$("$curlBin" -s "${curl_common_opts[@]}" 'https://token.ardplus.de/token/session' \
        -H 'authority: token.ardplus.de' \
        -H 'content-type: application/json' \
        -H "cookie: sid=$token" \
        -H 'origin: https://www.ardplus.de' \
        -H 'referer: https://www.ardplus.de/' \
        -H 'user-agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36' \
        --data-raw "$payload" \
        --compressed)
    urlParam=$(echo "$auth_response" | jq -r '.authorizationParams // empty')
    echo "$urlParam"
}

ensure_token() {
    if [[ -f "$token_file" ]]; then
        token=$(<"$token_file")
        token=$(printf '%s' "$token" | tr -d '\r\n')
    else
        login || {
            echo "Login not possible! Please check credentials and subscription for user ${username}." >&2
            exit 1
        }
        token=$(<"$token_file")
        token=$(printf '%s' "$token" | tr -d '\r\n')
    fi

    movieId="a0S010000007GcX"
    contentType="CmsMovie"
    urlParam=$(auth)
    if [[ "$urlParam" == null || -z "$urlParam" ]]; then
        renew_session || {
            echo "Login not possible! Please check credentials and subscription for user ${username}." >&2
            exit 1
        }
        urlParam=$(auth)
        if [[ "$urlParam" == null || -z "$urlParam" ]]; then
            echo "Login not possible! Please check credentials and subscription for user ${username}." >&2
            exit 1
        fi
    fi
    session_renewed=0
    cleanup
}

# Prints a failure reason on stdout and returns 1 when the URL is not an
# ARD Plus content URL.
validate_content_url() {
    local url="$1" host path
    if [[ ! "$url" =~ ^https?://([^/]+)(/.*)?$ ]]; then
        printf 'not an absolute http(s) URL: %s' "$url"
        return 1
    fi
    host="${BASH_REMATCH[1]}"
    path="${BASH_REMATCH[2]:-}"
    case "$host" in
        www.ardplus.de|ardplus.de) ;;
        *) printf 'unsupported host: %s' "$host"; return 1 ;;
    esac
    case "$path" in
        /details/*|/kategorie/*) return 0 ;;
        *) printf 'unsupported path: %s' "$path"; return 1 ;;
    esac
}

download_url() {
    local ardPlusUrl="$1"
    local episode_skip="$2"
    local showPath showId content_variables contentResult movie tvshow url_reason

    DOWNLOAD_FAIL_REASON=''
    episode_failure_count=0
    episode_failure_first_reason=''
    if ! url_reason=$(validate_content_url "$ardPlusUrl"); then
        DOWNLOAD_FAIL_REASON="invalid URL: ${url_reason}"
        return 1
    fi
    showPath=$(echo "$ardPlusUrl" | rev | cut -d "/" -f1 | rev)
    # Tatort category pages are handled from the page's ld+json and never need
    # the MovieDetails lookup, which cannot resolve the category slug.
    if [[ "$showPath" == tatort-* ]]; then
        download_tatort_category "$showPath" "$episode_skip"
        return $?
    fi
    showId=$(echo "$showPath" | cut -d "-" -f1)
    if [[ -z "$showId" ]]; then
        DOWNLOAD_FAIL_REASON="could not parse content ID from URL"
        return 1
    fi

    content_variables=$(jq -nc \
        --arg movieId "$showId" \
        '{movieId: $movieId, externalId: "", slug: "", potentialMovieId: ""}')
    if ! fetch_graphql "MovieDetails" "$GRAPHQL_MOVIE_DETAILS" "$content_variables" "$content_result" >/dev/null; then
        local metadata_error
        metadata_error=$(jq -r '.errors[0].message // empty' "$content_result" 2>/dev/null)
        if [[ -n "$metadata_error" ]]; then
            DOWNLOAD_FAIL_REASON="could not fetch content metadata (graphql: ${metadata_error})"
        else
            DOWNLOAD_FAIL_REASON="could not fetch content metadata (retries exhausted)"
        fi
        return 1
    fi

    contentResult=$(cat "$content_result")
    movie=$(echo "$contentResult" | jq '.data.movie')
    tvshow=$(echo "$contentResult" | jq '.data.series')

    if [[ "$movie" != null ]]; then
        movieId=$(echo "$movie" | jq -r '.id')
        contentType="CmsMovie"
        name=$(echo "$movie" | jq -r '.title // empty')
        videoUrl=$(echo "$movie" | jq -r '.videoSource.dashUrl')
        year=$(echo "$movie" | jq -r '.productionYear // empty')
        filename=$(build_movie_path "$name" "$year")
        movie_stem=$(movie_name_stem "$name" "$year")
        if skip_or_prepare_output "${downloads_dir}/${movie_stem}/${movie_stem}.*" "$filename"; then
            return 0
        fi
        urlParam=$(ensure_playback_authorization)
        if [[ -z "$urlParam" ]]; then
            DOWNLOAD_FAIL_REASON="no playback authorization for movie ${name}"
            cleanup
            return 1
        fi
        if [[ -z "$videoUrl" || "$videoUrl" == null ]]; then
            DOWNLOAD_FAIL_REASON="no video URL in API response for movie ${name}"
            cleanup
            return 1
        fi
        downloadUrl="${videoUrl}?${urlParam}"
        log_msg "Lade Film ${filename}..."
        if ! run_yt_dlp "$downloadUrl" "$filename" "$name"; then
            cleanup
            return 1
        fi
        if ! finalize_output "$filename" "$movie_stem" "$movie_stem" 0; then
            cleanup
            return 1
        fi
        cleanup
        return 0
    elif [[ "$tvshow" != null ]]; then
        local requestedShow seasonIds seasonOutput selectedSeasonList selectedSeason
        requestedShow=$(echo "$contentResult" | jq -r '.data.series.title // empty')
        seasonIds=$(echo "$contentResult" | jq '[.data.series.seasons.nodes[] | { season: .seasonInSeries, seasonId: .id, title: .title }]')
        seasonOutput=$(echo "$seasonIds" | jq '[.[] | { Option: .season, Titel: .title }]' | jq -r '(.[0]|keys_unsorted|(.,map(length*"-"))),.[]|map(.)|@tsv'|column -ts $'\t')
        log_msg ""
        log_msg "Gewünschte Serie: $requestedShow"
        log_msg ""
        log_msg "$seasonOutput"
        log_msg ""

        if [ $automatic_download -eq 0 ]; then
            local selection_input season_input season_canonical validated_list
            while true; do
                printf '%s' "Welche Staffel möchtest du runterladen? "
                if ! read -r selection_input; then
                    DOWNLOAD_FAIL_REASON="no season selected (input ended)"
                    return 1
                fi
                # Comma and whitespace both separate season numbers.
                selection_input="${selection_input//,/ }"
                validated_list=''
                for season_input in $selection_input; do
                    if ! [[ "$season_input" =~ ^[0-9]+$ ]] ||
                       ! echo "$seasonIds" | jq -e --arg s "$season_input" \
                             'any(.[]; .season == ($s | tonumber))' >/dev/null; then
                        validated_list=''
                        break
                    fi
                    # 10# drops leading zeros so the season list stays valid JSON.
                    season_canonical=$((10#$season_input))
                    validated_list="${validated_list}${validated_list:+ }${season_canonical}"
                done
                if [[ -n "$validated_list" ]]; then
                    selectedSeasonList="$validated_list"
                    break
                fi
                log_msg "Ungültige Staffelauswahl, bitte erneut versuchen."
            done
        else
            selectedSeasonList=$(echo "$seasonIds" | jq -r '.[].season')
        fi

        local apply_episode_skip=1
        for selectedSeason in $selectedSeasonList
        do
            local selectedSeasonId seasonData episodes amount episode_line tail_from season_padding
            selectedSeasonId=$(echo "$seasonIds" | jq -r --argjson s "$selectedSeason" '.[] | select(.season == $s) | .seasonId')
            if [[ -z "$selectedSeasonId" || "$selectedSeasonId" == "null" ]]; then
                record_episode_failure "season ${selectedSeason} not found"
                continue
            fi

            local season_result season_variables
            season_result=$(mktemp)
            season_variables=$(jq -nc --arg seasonId "$selectedSeasonId" '{seasonId: $seasonId}')
            if ! fetch_graphql "EpisodesInSeasonData" "$GRAPHQL_EPISODES_IN_SEASON" "$season_variables" "$season_result" >/dev/null; then
                local episode_error
                episode_error=$(jq -r '.errors[0].message // empty' "$season_result" 2>/dev/null)
                rm -f "$season_result"
                if [[ -n "$episode_error" ]]; then
                    record_episode_failure "could not fetch episodes for season ${selectedSeason} (graphql: ${episode_error})"
                else
                    record_episode_failure "could not fetch episodes for season ${selectedSeason} (retries exhausted)"
                fi
                continue
            fi
            seasonData=$(cat "$season_result")
            rm -f "$season_result"
            episodes=$(echo "$seasonData" | jq '[.data.episodes.nodes[]? | { id: .id, episodeNo: .episodeInSeason, title: .title, videoUrl: .videoSource.dashUrl }]')
            amount=$(echo "$episodes" | jq '. | length')
            if [[ "$amount" == "0" ]]; then
                record_episode_failure "no episodes returned for season ${selectedSeason}"
                continue
            fi
            log_msg ""
            # Three digits for seasons with 100 or more episodes.
            season_padding=$(episode_padding "$(echo "$episodes" | jq '[.[] | (.episodeNo // 0) | (tonumber? // 0)] | max // 0')")
            log_msg "Staffel $selectedSeason hat $amount Folgen."
            tail_from=$(episode_tail_from "$episode_skip" "$apply_episode_skip")
            if [[ $tail_from -ne 1 ]]; then
                log_msg "Überspringe $episode_skip Episode(n) in der ersten Staffel."
                apply_episode_skip=0
            fi

            while read -r episode_line
            do
                local name videoUrl episode filename urlParam downloadUrl episode_stem season_stem
                movieId=$(echo "$episode_line" | jq -r '.id')
                contentType="CmsEpisode"
                name=$(echo "$episode_line" | jq -r '.title // empty')
                videoUrl=$(echo "$episode_line" | jq -r '.videoUrl')
                episode=$(echo "$episode_line" | jq -r '.episodeNo // empty')
                filename=$(build_episode_path "$requestedShow" "$selectedSeason" "$episode" "$name")
                episode_stem=$(episode_name_stem "$requestedShow" "$selectedSeason" "$episode" "$season_padding")
                season_stem=$(season_name_stem "$requestedShow" "$selectedSeason")
                if skip_or_prepare_output "${downloads_dir}/${season_stem}.*/${episode_stem}.*" "$filename"; then
                    continue
                fi
                urlParam=$(ensure_playback_authorization)
                if [[ -z "$urlParam" ]]; then
                    record_episode_failure "no playback authorization for ${filename}"
                    cleanup
                    continue
                fi
                if [[ -z "$videoUrl" || "$videoUrl" == null ]]; then
                    record_episode_failure "no video URL in API response for ${filename}"
                    cleanup
                    continue
                fi
                downloadUrl="${videoUrl}?${urlParam}"
                log_msg "Lade ${filename}..."
                if ! run_yt_dlp "$downloadUrl" "$filename" "$filename"; then
                    record_episode_failure "$DOWNLOAD_FAIL_REASON"
                    cleanup
                    continue
                fi
                if ! finalize_output "$filename" "$episode_stem" "$season_stem" 1; then
                    record_episode_failure "could not name ${filename}"
                    cleanup
                    continue
                fi
                cleanup
            done < <(echo "$episodes" | jq -c '.[]' | tail -n +"$tail_from")
        done

        if [[ $episode_failure_count -gt 0 ]]; then
            DOWNLOAD_FAIL_REASON="${episode_failure_count} episode download(s) failed (first: ${episode_failure_first_reason})"
            return 1
        fi
        return 0
    else
        if [[ -n "$(echo "$contentResult" | jq -r '.errors[0].message // empty')" ]]; then
            DOWNLOAD_FAIL_REASON="graphql API error: $(echo "$contentResult" | jq -r '.errors[0].message')"
        else
            DOWNLOAD_FAIL_REASON="invalid content (no movie or series in API response)"
        fi
        log_msg "$DOWNLOAD_FAIL_REASON"
        return 1
    fi
}

# Tatort category pages (/kategorie/tatort-<city>): the episode list comes from
# the page's ld+json, the per-episode metadata from the GraphQL API.
download_tatort_category() {
    local showPath="$1" episode_skip="$2"
    local tatortCity tatortResponse tatortCityEpisodes amount cityCapitalized episode_line tatort_result
    tatort_result=$(mktemp)
    tatortCity=$(echo "$showPath" | cut -d "-" -f2)
    tatortResponse=$("$curlBin" -s "${curl_common_opts[@]}" "https://www.ardplus.de/kategorie/$showPath" \
    --header 'authority: data.ardplus.de' \
    --header 'content-type: application/json' \
    --header "cookie: sid=$token" \
    --header 'origin: https://www.ardplus.de' \
    --header 'referer: https://www.ardplus.de/' \
    --header 'user-agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36')

    tatortCityEpisodes=$(echo "$tatortResponse" | perl -0777 -ne 'print "$1\n" if /<script type="application\/ld\+json">\s*(.*?)\s*<\/script>/s')
    if [[ -z "$tatortCityEpisodes" ]]; then
        rm -f "$tatort_result"
        DOWNLOAD_FAIL_REASON="could not parse Tatort episode list from category page"
        return 1
    fi

    amount=$(echo "$tatortCityEpisodes" | jq '.itemListElement | length')
    cityCapitalized=$(echo "${tatortCity}" | awk '{$1=toupper(substr($1,0,1))substr($1,2)}1')
    log_msg "Der Tatort ${cityCapitalized} hat $amount Episoden."
    if [ $automatic_download -eq 0 ]; then
        echo -n "Wie viele Episoden möchtest du überspringen? (0=alle laden) "
        read -r episode_skip
        if ! [[ "$episode_skip" =~ ^[0-9]+$ ]]; then
            log_msg "Ungültige Eingabe, es werden 0 Episoden übersprungen."
            episode_skip=0
        fi
        log_msg "Überspringe $episode_skip Episode(n)."
    else
        episode_skip=0
    fi
    episode_skip=$((episode_skip + 1))

    while read -r episode_line
    do
        local episodeId episode_variables episodeDetails name videoUrl year customData episode team city filename urlParam downloadUrl tatort_base tatort_output_glob
        episodeId=$(echo "$episode_line" | jq -r '.item.url' | sed -E 's#.*/details/([^/-]+).*#\1#')
        episode_variables=$(jq -nc \
            --arg movieId "$episodeId" \
            '{movieId: $movieId, externalId: "", slug: "", potentialMovieId: ""}')

        if ! fetch_graphql "MovieDetails" "$GRAPHQL_MOVIE_DETAILS" "$episode_variables" "$tatort_result" >/dev/null; then
            local tatort_error
            tatort_error=$(jq -r '.errors[0].message // empty' "$tatort_result" 2>/dev/null)
            if [[ -n "$tatort_error" ]]; then
                record_episode_failure "could not fetch Tatort episode details (graphql: ${tatort_error})"
            else
                record_episode_failure "could not fetch Tatort episode details (retries exhausted)"
            fi
            rm -f "$tatort_result"
            continue
        fi

        episodeDetails=$(cat "$tatort_result")
        movieId=$(echo "$episodeDetails" | jq -r '.data.movie.id')
        contentType="CmsMovie"
        name=$(echo "$episodeDetails" | jq -r '.data.movie.title // empty')
        videoUrl=$(echo "$episodeDetails" | jq -r '.data.movie.videoSource.dashUrl')
        year=$(echo "$episodeDetails" | jq -r '.data.movie.productionYear // empty')
        customData=$(echo "$episodeDetails" | jq '.data.movie.customData // {}')
        episode=$(echo "$customData" | jq -r '.episodeProductionNumber // empty')
        team=$(echo "$customData" | jq -r '.team // empty')
        city=$(echo "$customData" | jq -r '.location // empty')
        filename=$(build_tatort_path "$city" "$team" "$episode" "$name" "$year")
        if [[ -z "$movieId" || "$movieId" == null ]]; then
            record_episode_failure "no movie metadata for Tatort episode ${episodeId}"
            continue
        fi
        tatort_base=$(tatort_name_stem "${city:-$tatortCity}" "$episode" "$year")
        # Without an episode number the output name cannot be matched reliably.
        if [[ -n "$episode" ]]; then
            tatort_output_glob="${downloads_dir}/${tatort_base}.*"
        else
            tatort_output_glob=''
        fi
        if skip_or_prepare_output "$tatort_output_glob" "$filename"; then
            continue
        fi
        urlParam=$(ensure_playback_authorization)
        if [[ -z "$urlParam" ]]; then
            record_episode_failure "no playback authorization for ${filename}"
            cleanup
            continue
        fi
        if [[ -z "$videoUrl" || "$videoUrl" == null ]]; then
            record_episode_failure "no video URL in API response for ${filename}"
            cleanup
            continue
        fi
        downloadUrl="${videoUrl}?${urlParam}"
        log_msg "Lade ${filename}..."
        if ! run_yt_dlp "$downloadUrl" "$filename" "$filename"; then
            record_episode_failure "$DOWNLOAD_FAIL_REASON"
            cleanup
            continue
        fi
        if ! finalize_output "$filename" "$tatort_base" '' 0; then
            record_episode_failure "could not name ${filename}"
            cleanup
            continue
        fi
        cleanup
        sleep 1
    done < <(echo "$tatortCityEpisodes" | jq -c '.itemListElement[]' | tail -n +"$episode_skip")
    rm -f "$tatort_result"

    if [[ $episode_failure_count -gt 0 ]]; then
        DOWNLOAD_FAIL_REASON="${episode_failure_count} episode download(s) failed (first: ${episode_failure_first_reason})"
        return 1
    fi
    return 0
}

cleanup_tmp() {
    # season_result/tatort_result are locals of download_url and
    # download_tatort_category; they are still in scope when a signal
    # interrupts the download loops and empty otherwise.
    local f
    for f in "${content_result:-}" "${season_result:-}" "${tatort_result:-}"; do
        [[ -n "$f" ]] && rm -f "$f"
    done
    return 0
}

handle_interrupt() {
    echo "CTRL+C pressed. Cleanup and exit!"
    cleanup
    exit 130
}

print_usage() {
    printf '%s\n' \
        "Usage: ./ard-plus-dl.sh [options] <url> [username password] [skip]" \
        "       ./ard-plus-dl.sh [options] --links-file <file> [username password] [skip]" \
        "" \
        "Options (they must come before the URL):" \
        "  --automatic            run without interactive prompts" \
        "  --links-file <file>    read URLs from <file> (batch mode)" \
        "  --force-redownload     delete existing files and download again" \
        "  -h, --help             show this help and exit" \
        "" \
        "Environment: ARD_PLUS_USER, ARD_PLUS_PASSWORD, ARD_PLUS_TAG (Datei-Tag is mandatory)"
}

main() {
# parse input parameters
while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            print_usage
            exit 0
            ;;
        --automatic)
            automatic_download=1
            shift
            ;;
        --links-file)
            if [[ -z "${2:-}" || "${2:-}" == -* ]]; then
                echo "Error: --links-file requires a file argument" >&2
                exit 1
            fi
            batch_mode=1
            links_file=$2
            shift 2
            ;;
        --force-redownload)
            skip_existing_files=0
            shift
            ;;
        --)
            shift
            break
            ;;
        *)
            if [[ "$1" == -* ]]; then
                echo "Unknown option: $1" >&2
                exit 1
            fi
            break
            ;;
    esac
done

# Credentials may come from positional arguments (legacy), the
# ARD_PLUS_USER/ARD_PLUS_PASSWORD environment variables, or a credentials
# file. When they are omitted, a lone trailing numeric argument is the skip
# count.
if [[ $batch_mode -eq 1 ]]; then
    if [[ $# -ge 2 ]]; then
        username=${1:-}
        password=${2:-}
        skip=${3:-}
    else
        username=''
        password=''
        skip=${1:-}
    fi
else
    ardPlusUrl=${1:-}
    if [[ $# -ge 3 ]]; then
        username=${2:-}
        password=${3:-}
        skip=${4:-}
    else
        username=''
        password=''
        skip=${2:-}
    fi
fi
movieId=''
contentType=''
token=''

resolve_credentials

if [[ $batch_mode -eq 1 ]]; then
    if [[ -z "$links_file" || ! -f "$links_file" ]]; then
        echo "Links file missing or not found: $links_file" >&2
        exit 1
    fi
    automatic_download=1
    links_dir=$(cd "$(dirname "$links_file")" && pwd)
    links_file="$links_dir/$(basename "$links_file")"
elif [[ -z "$ardPlusUrl" ]]; then
    echo "URL or --links-file required." >&2
    exit 1
fi

# Validated before check_dependencies: a malformed URL needs no tooling.
if [[ $batch_mode -eq 0 ]]; then
    ardPlusUrl=$(normalize_url "$ardPlusUrl")
    if ! url_reason=$(validate_content_url "$ardPlusUrl"); then
        echo "Invalid URL: ${url_reason}" >&2
        exit 1
    fi
fi

if [[ -z "$skip" ]]; then
    skip=1
elif ! [[ "$skip" =~ ^[0-9]+$ ]]; then
    if [[ -z "$username" && -z "$password" ]]; then
        # A lone trailing argument is the username, not a skip count.
        username="$skip"
        skip=1
    else
        echo "Invalid skip value: $skip (expected a non-negative number)" >&2
        exit 1
    fi
fi

check_dependencies

resolve_file_tag || exit 1

work_dir=$(pwd)
if [[ $batch_mode -eq 1 ]]; then
    work_dir="$links_dir"
fi
downloads_dir="${DOWNLOADS_DIR:-${work_dir}/downloads}"
mkdir -p "$downloads_dir"
if [[ ! -d "$downloads_dir" || ! -w "$downloads_dir" ]]; then
    echo "Downloads directory is not writable: ${downloads_dir}" >&2
    exit 1
fi

resolve_token_file

if ! require_auth_source; then
    echo "Credentials missing! Provide them via one of:"
    echo "  - environment variables: ARD_PLUS_USER and ARD_PLUS_PASSWORD"
    echo "  - credentials file: $(credentials_file_default) (chmod 600, lines: username=... / password=...)"
    echo "  - positional arguments (visible to other processes via ps - not recommended):"
    echo "      ./ard-plus-dl.sh [--automatic] <ard-plus-url> <username> <password> [skip]"
    echo "      ./ard-plus-dl.sh --links-file <file> <username> <password> [skip]"
    exit 1
fi

trap cleanup_tmp EXIT
trap handle_interrupt INT TERM
content_result=$(mktemp)
RUN_TS=$(date +%Y-%m-%d_%H-%M-%S)

if [[ $batch_mode -eq 1 ]]; then
    logs_dir="$links_dir/logs"
    mkdir -p "$logs_dir" || { echo "Logs directory is not writable: ${logs_dir}" >&2; exit 1; }
    SUCCESS_FILE="$logs_dir/successful_links_${RUN_TS}.txt"
    FAILED_FILE="$logs_dir/failed_links_${RUN_TS}.txt"
    LOG_FILE="$logs_dir/download_log_${RUN_TS}.txt"
    touch "$SUCCESS_FILE" "$FAILED_FILE" "$LOG_FILE"
fi

ensure_token

echo "Downloads directory: ${downloads_dir}"

if [[ $batch_mode -eq 1 ]]; then
    log_msg "Batch download started at ${RUN_TS}"
    log_msg "Links file: ${links_file}"
    log_msg "Downloads directory: ${downloads_dir}"
    log_msg "Logs directory: ${logs_dir}"
    log_msg "Download log: ${LOG_FILE}"
    log_msg "Success log: ${SUCCESS_FILE}"
    log_msg "Failed log: ${FAILED_FILE}"
    log_msg ""

    while IFS= read -r link_line || [[ -n "$link_line" ]]; do
        link_line="${link_line%%#*}"
        link_line="${link_line%"${link_line##*[![:space:]]}"}"
        link_line="${link_line#"${link_line%%[![:space:]]*}"}"
        [[ -z "$link_line" ]] && continue

        normalized_link=$(normalize_url "$link_line")
        log_msg "Processing: ${normalized_link}"

        if download_url "$normalized_link" "$skip"; then
            record_success "$normalized_link"
        else
            if [[ -z "$DOWNLOAD_FAIL_REASON" ]]; then
                DOWNLOAD_FAIL_REASON="unknown error"
            fi
            record_failure "$normalized_link" "$DOWNLOAD_FAIL_REASON"
        fi
        log_msg ""
    done < "$links_file"

    log_msg "Batch download finished."
    log_msg "Logs directory: ${logs_dir}"
    log_msg "Successful links: ${SUCCESS_FILE}"
    log_msg "Failed links: ${FAILED_FILE}"
    if [[ $batch_failure_count -gt 0 ]]; then
        exit 1
    fi
else
    if download_url "$ardPlusUrl" "$skip"; then
        cleanup
    else
        cleanup
        exit 1
    fi
fi
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
