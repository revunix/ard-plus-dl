// Package ardplus is the ARD Plus API client: the GraphQL metadata API, the
// playback authorization API and the Tatort category pages.
package ardplus

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/revunix/ard-plus-dl/internal/auth"
)

// Production endpoints.
const (
	GraphQLURL  = "https://data.ardplus.de/ard/graphql"
	TokenURL    = "https://token.ardplus.de"
	CategoryURL = "https://www.ardplus.de/kategorie"
)

// GraphQL operations use the persisted-query protocol published by ARD Plus.
const (
	operationMovieDetails  = "MovieDetails"
	operationEpisodes      = "EpisodesInSeasonData"
	movieDetailsHash       = "b84f6a7001135d9c5af93441657145372aba540e4e61bbd8098dda69c060870e"
	episodesInSeasonHash   = "33e68739ed1344c3f791f0c66b7ce2b73d759f7b3897cae7967af724f9231795"
	graphQLMaxAttempts     = 5
	graphQLRetryBaseSecond = 1
)

// episodesInSeasonQuery is the full EpisodesInSeasonData document as the web
// client prints it, including Apollo's added __typename fields. It is sent
// once when the server reports that it does not know the persisted hash.
const episodesInSeasonQuery = `
query EpisodesInSeasonData($seasonId: Guid!) {
  episodes: allCmsEpisodes(
    filter: { seasonId: { equalTo: $seasonId } }
    orderBy: EPISODE_IN_SEASON_ASC
  ) {
    nodes {
      ...EpisodeDetailSliderData
      __typename
    }
    __typename
  }
}

fragment EpisodeDetailSliderData on CmsEpisode {
  id
  episodeInSeason
  title
  runtimeInSeconds
  longSynopsis
  mediumSynopsis
  shortSynopsis
  supershortSynopsis
  fskRating
  flags
  licenses: cmsLicensesByContentEpisodeId(
    filter: { isActive: { equalTo: true } }
  ) {
    nodes {
      properties
      __typename
    }
    __typename
  }
  coverImage {
    ...ImageData
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  videoSource {
    hlsUrl
    dashUrl
    dashContentId
    pmdUrl
    __typename
  }
  dashAudioInfo {
    language
    isStereo
    isSurround
    type
    __typename
  }
  dashTextTrackInfo {
    language
    isForced
    type
    __typename
  }
  __typename
}

fragment ImageData on CmsImage {
  masterUrl
  width
  height
  name
  __typename
}
`

// movieDetailsQuery is the full MovieDetails document as the web client sends
// it, including Apollo's added __typename fields. It is sent once when the
// server reports that it does not know the persisted hash.
const movieDetailsQuery = `
query MovieDetails(
  $movieId: String!
  $externalId: String!
  $slug: String!
  $potentialMovieId: String!
) {
  movie: getMovieDetails(
    idList: [$movieId, $potentialMovieId, $externalId, $slug]
  ) {
    ...MovieDetailData
    __typename
  }
  series: getSeriesDetails(
    idList: [$movieId, $potentialMovieId, $externalId, $slug]
  ) {
    ...SeriesDetailData
    __typename
  }
}

fragment ImageData on CmsImage {
  masterUrl
  width
  height
  name
  __typename
}

fragment SeriesDetailData on CmsSery {
  id
  externalIdentifier
  slug
  title
  slug
  productionYear
  fskRating
  flags
  longSynopsis
  mediumSynopsis
  shortSynopsis
  supershortSynopsis
  copyright
  rating

  runtimeInSeconds
  productionCountry

  originalTitle
  originalLanguage
  customData
  isPurchasable
  isRentable
  isStreamable
  licenses: cmsLicensesByContentSeriesId(
    filter: { isActive: { equalTo: true } }
  ) {
    nodes {
      properties
      __typename
    }
    __typename
  }
  movieProducts: cmsMovieContentProductsByContentSeriesId {
    nodes {
      isActive
      product {
        paymentProvider
        price
        currencyCode
        type
        available
        description
        qualityLevel
        description
        __typename
      }
      id
      __typename
    }
    __typename
  }
  dashTextTrackInfo {
    language
    isForced
    __typename
  }
  hlsTextTrackInfo {
    language
    isForced
    __typename
  }
  dashAudioInfo {
    language
    isStereo
    isSurround
    type
    __typename
  }
  hlsAudioInfo {
    language
    isStereo
    isSurround
    type
    __typename
  }

  logo {
    ...ImageData
    __typename
  }
  coverImage {
    ...ImageData
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  headerImage {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  headerImage24By9 {
    ...ImageData
    __typename
  }

  cast: cmsMovieContentPeopleByContentSeriesId(
    condition: { connectionType: "actor" }
  ) {
    nodes {
      roleName
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  writers: cmsMovieContentPeopleByContentSeriesId(
    condition: { connectionType: "writer" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }

  directors: cmsMovieContentPeopleByContentSeriesId(
    condition: { connectionType: "director" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  seoTags {
    tag {
      label
      __typename
    }
    __typename
  }
  seoTitle
  seoDescription
  seoSharingImage {
    ...ImageData
    __typename
  }

  videoSource {
    dashUrl
    dashContentId
    hlsUrl
    pmdUrl
    __typename
  }

  seasons: cmsSeasonsBySeriesId(orderBy: SEASON_IN_SERIES_ASC) {
    nodes {
      ...SeasonDetailData
      __typename
    }
    __typename
  }

  clips: cmsClipsByParentSeriesId {
    nodes {
      ...ClipData
      __typename
    }
    __typename
  }

  categories: cmsMovieContentCategoriesByContentSeriesId {
    nodes {
      category {
        id
        title
        slug
        fskRating
        parentId
        parent {
          id
          slug
          __typename
        }
        __typename
      }
      __typename
    }
    __typename
  }
  __typename
}

fragment SeasonDetailData on CmsSeason {
  id
  title
  slug
  seasonInSeries

  productionYear
  fskRating
  flags
  longSynopsis
  mediumSynopsis
  shortSynopsis
  supershortSynopsis
  copyright
  rating

  runtimeInSeconds
  productionYear
  productionCountry
  coverImage {
    ...ImageData
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  headerImage {
    ...ImageData
    __typename
  }
  headerImage24By9 {
    ...ImageData
    __typename
  }
  cast: cmsMovieContentPeopleByContentSeasonId(
    condition: { connectionType: "actor" }
  ) {
    nodes {
      roleName
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  writers: cmsMovieContentPeopleByContentSeasonId(
    condition: { connectionType: "writer" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }

  directors: cmsMovieContentPeopleByContentSeasonId(
    condition: { connectionType: "director" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  seoTags {
    tag {
      label
      __typename
    }
    __typename
  }
  # required for directly link via a play button
  firstEpisode: cmsEpisodesBySeasonId(
    first: 1
    orderBy: EPISODE_IN_SEASON_ASC
  ) {
    nodes {
      displayAudioLanguages
      productionYear
      id
      dashAudioInfo {
        language
        isStereo
        isSurround
        type
        __typename
      }
      hlsAudioInfo {
        language
        isStereo
        isSurround
        type
        __typename
      }
      __typename
    }
    __typename
  }
  __typename
}

fragment MovieDetailData on CmsMovie {
  id
  externalIdentifier
  slug
  title
  slug
  productionYear
  fskRating
  flags
  longSynopsis
  mediumSynopsis
  shortSynopsis
  supershortSynopsis
  copyright
  rating
  runtimeInSeconds
  productionCountry
  originalTitle
  originalLanguage
  customData
  isPurchasable
  isRentable
  isStreamable
  movieProducts: cmsMovieContentProductsByContentMovieId {
    nodes {
      isActive
      product {
        paymentProvider
        price
        currencyCode
        type
        available
        description
        qualityLevel
        description
        __typename
      }
      id
      __typename
    }
    __typename
  }
  displayAudioLanguages
  dashTextTrackInfo {
    language
    isForced
    type
    __typename
  }
  hlsTextTrackInfo {
    language
    isForced
    type
    __typename
  }
  dashAudioInfo {
    language
    isStereo
    isSurround
    type
    __typename
  }
  hlsAudioInfo {
    language
    isStereo
    isSurround
    type
    __typename
  }

  logo {
    ...ImageData
    __typename
  }
  coverImage {
    ...ImageData
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  headerImage {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  headerImage24By9 {
    ...ImageData
    __typename
  }

  cast: cmsMovieContentPeopleByContentMovieId(
    condition: { connectionType: "actor" }
  ) {
    nodes {
      roleName
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  writers: cmsMovieContentPeopleByContentMovieId(
    condition: { connectionType: "writer" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }

  directors: cmsMovieContentPeopleByContentMovieId(
    condition: { connectionType: "director" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  seoTags {
    tag {
      label
      __typename
    }
    __typename
  }
  seoTitle
  seoDescription
  seoSharingImage {
    ...ImageData
    __typename
  }

  videoSource {
    dashUrl
    dashContentId
    hlsUrl
    pmdUrl
    __typename
  }

  clips: cmsClipsByParentMovieId {
    nodes {
      ...ClipData
      __typename
    }
    __typename
  }
  trailers: cmsTrailersByParentMovieId {
    nodes {
      ...TrailerData
      __typename
    }
    __typename
  }

  categories: cmsMovieContentCategoriesByContentMovieId {
    nodes {
      category {
        id
        title
        slug
        fskRating
        parentId
        parent {
          id
          slug
          __typename
        }
        __typename
      }
      __typename
    }
    __typename
  }
  __typename
}

fragment ClipData on CmsClip {
  id
  title

  supershortSynopsis
  shortSynopsis
  mediumSynopsis
  longSynopsis
  runtimeInSeconds
  fskRating
  flags

  videoSource {
    hlsUrl
    dashUrl
    dashContentId
    pmdUrl
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  coverImage {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  __typename
}

fragment TrailerData on CmsTrailer {
  id
  title

  supershortSynopsis
  shortSynopsis
  mediumSynopsis
  longSynopsis
  runtimeInSeconds
  fskRating
  flags

  videoSource {
    hlsUrl
    dashUrl
    dashContentId
    pmdUrl
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  coverImage {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  __typename
}
`

// CustomData carries the Tatort specific fields. The API sends them either as
// an object or as a JSON string containing that object, so both forms are
// accepted.
type CustomData struct {
	Team                    string `json:"team"`
	Location                string `json:"location"`
	EpisodeProductionNumber string `json:"episodeProductionNumber"`
}

// UnmarshalJSON accepts an object, a JSON string holding an object, or null.
func (c *CustomData) UnmarshalJSON(data []byte) error {
	payload := bytes.TrimSpace(data)
	if len(payload) == 0 || string(payload) == "null" {
		return nil
	}
	if payload[0] == '"' {
		var inner string
		if err := json.Unmarshal(payload, &inner); err != nil {
			return err
		}
		inner = strings.TrimSpace(inner)
		if inner == "" {
			return nil
		}
		payload = []byte(inner)
	}
	if payload[0] != '{' {
		// Anything else (a number, a plain string) carries no Tatort fields.
		return nil
	}
	// The fields are not consistently typed: the episode number arrives as a
	// number for some episodes and as a string for others.
	var raw struct {
		Team                    any `json:"team"`
		Location                any `json:"location"`
		EpisodeProductionNumber any `json:"episodeProductionNumber"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	c.Team = scalarString(raw.Team)
	c.Location = scalarString(raw.Location)
	c.EpisodeProductionNumber = scalarString(raw.EpisodeProductionNumber)
	return nil
}

// scalarString renders a loosely typed JSON scalar as a string.
func scalarString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		if typed == math.Trunc(typed) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	}
	return ""
}

// Movie is a movie or a Tatort episode.
type Movie struct {
	ID             string
	Title          string
	ProductionYear string
	CustomData     CustomData
	DashURL        string
}

// Season is one season of a series.
type Season struct {
	ID             string
	Title          string
	SeasonInSeries int
}

// Series is the series part of a MovieDetails response.
type Series struct {
	Title   string
	Seasons []Season
}

// Episode is one episode inside a season.
type Episode struct {
	ID              string
	EpisodeInSeason *int
	Title           string
	DashURL         string
}

// Client talks to the ARD Plus API.
type Client struct {
	HTTP        *http.Client
	GraphQLURL  string
	TokenURL    string
	CategoryURL string
	UA          string

	// token is the session cookie value. It is written by SetToken, which the
	// session calls when it renews the login, while requests read it
	// concurrently. The mutex keeps that safe.
	tokenMu sync.RWMutex
	token   string

	// Attempts and Sleep are injectable for tests.
	Attempts int
	Sleep    func(context.Context, time.Duration)

	Logf func(format string, args ...any)
	// Debugf receives diagnostic lines when ARD_PLUS_DEBUG_DIR is set.
	Debugf func(format string, args ...any)
}

// SetToken updates the session cookie value.
func (c *Client) SetToken(token string) {
	c.tokenMu.Lock()
	c.token = token
	c.tokenMu.Unlock()
}

// Token returns the current session cookie value.
func (c *Client) Token() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

func (c *Client) ua() string {
	if c.UA != "" {
		return c.UA
	}
	return auth.DefaultUA
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// readResponseBody reads a response body and unpacks it when the server sent
// it gzip-compressed. Go's transport only decompresses transparently when it
// added the Accept-Encoding header itself, which is not the case for the
// requests that set their own headers.
func readResponseBody(resp *http.Response) ([]byte, error) {
	var reader io.Reader = resp.Body
	if strings.EqualFold(strings.TrimSpace(resp.Header.Get("Content-Encoding")), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip response: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	return io.ReadAll(io.LimitReader(reader, 4<<20))
}

func (c *Client) sleep(ctx context.Context, d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(ctx, d)
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (c *Client) graphQLURL() string {
	if c.GraphQLURL != "" {
		return c.GraphQLURL
	}
	return GraphQLURL
}

func (c *Client) tokenURL() string {
	if c.TokenURL != "" {
		return c.TokenURL
	}
	return TokenURL
}

func (c *Client) categoryURL() string {
	if c.CategoryURL != "" {
		return c.CategoryURL
	}
	return CategoryURL
}

// applyHeaders sets the ARD Plus request headers. The Host header is never
// overridden: ARD's CDN answers a foreign Host with 403.
func (c *Client) applyHeaders(req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := c.Token(); token != "" {
		req.Header.Set("Cookie", "sid="+token)
	}
	req.Header.Set("Origin", "https://www.ardplus.de")
	req.Header.Set("Referer", "https://www.ardplus.de/")
	req.Header.Set("User-Agent", c.ua())
}

type graphQLResponse struct {
	Errors []struct {
		Message    string `json:"message"`
		Extensions struct {
			Code string `json:"code"`
		} `json:"extensions"`
	} `json:"errors"`
	Data json.RawMessage `json:"data"`
}

// isPersistedQueryNotFound reports whether a GraphQL error means that the
// server cannot resolve the persisted-query hash.
func isPersistedQueryNotFound(message, code string) bool {
	if strings.EqualFold(code, "PERSISTED_QUERY_NOT_FOUND") {
		return true
	}
	return strings.Contains(strings.ToLower(message), strings.ToLower("PersistedQueryNotFound"))
}

// graphQLDataOK mirrors `jq -e '.data | values | length > 0'`: at least one
// top-level value that is not null.
func graphQLDataOK(data json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return false
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		// Non-object data can still hold a value (jq's values is identity).
		return true
	}
	for _, value := range object {
		if strings.TrimSpace(string(value)) != "null" {
			return true
		}
	}
	return false
}

// queryGraphQL sends one persisted GraphQL operation, retrying transient
// failures but never retrying GraphQL errors or permanent HTTP client errors.
func (c *Client) queryGraphQL(ctx context.Context, operation string, variables map[string]any, out any) error {
	var hash string
	switch operation {
	case operationMovieDetails:
		hash = movieDetailsHash
	case operationEpisodes:
		hash = episodesInSeasonHash
	default:
		return fmt.Errorf("unsupported GraphQL operation %q", operation)
	}

	variableJSON, err := json.Marshal(variables)
	if err != nil {
		return err
	}
	endpoint, err := url.Parse(c.graphQLURL())
	if err != nil {
		return err
	}
	query := endpoint.Query()
	query.Set("extensions", fmt.Sprintf(`{"persistedQuery":{"version":1,"sha256Hash":%q}}`, hash))
	query.Set("variables", string(variableJSON))
	query.Set("operationName", operation)
	endpoint.RawQuery = query.Encode()

	attempts := c.Attempts
	if attempts <= 0 {
		attempts = graphQLMaxAttempts
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return err
		}
		c.applyHeaders(req)

		resp, err := c.httpClient().Do(req)
		status := 0
		var body []byte
		if err == nil {
			body, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			resp.Body.Close()
			status = resp.StatusCode
		}

		var parsed graphQLResponse
		if status == http.StatusOK {
			if jsonErr := json.Unmarshal(body, &parsed); jsonErr == nil {
				if len(parsed.Errors) > 0 && parsed.Errors[0].Message != "" {
					if isPersistedQueryNotFound(parsed.Errors[0].Message, parsed.Errors[0].Extensions.Code) {
						queryText := episodesInSeasonQuery
						if operation == operationMovieDetails {
							queryText = movieDetailsQuery
						}
						c.logf("GraphQL persisted query missing (HTTP 200, error: %s), retrying with the full query", parsed.Errors[0].Message)
						return c.queryGraphQLWithText(ctx, operation, queryText, variables, out)
					}
					c.logf("GraphQL request failed permanently (HTTP 200, error: %s), not retrying", parsed.Errors[0].Message)
					return fmt.Errorf("%s", parsed.Errors[0].Message)
				}
				if graphQLDataOK(parsed.Data) {
					if out != nil {
						if unmarshalErr := json.Unmarshal(parsed.Data, out); unmarshalErr != nil {
							return unmarshalErr
						}
					}
					return nil
				}
			}
		}

		message := ""
		if json.Unmarshal(body, &parsed) == nil && len(parsed.Errors) > 0 {
			message = parsed.Errors[0].Message
		}
		if err != nil {
			lastErr = err
			message = err.Error()
		} else {
			lastErr = fmt.Errorf("HTTP %d", status)
		}
		if status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests {
			if message == "" {
				message = http.StatusText(status)
			}
			c.logf("GraphQL request rejected (HTTP %d, error: %s), not retrying", status, message)
			return fmt.Errorf("GraphQL request rejected (HTTP %d: %s)", status, message)
		}
		if message == "" {
			message = "empty data"
		}
		if status == http.StatusTooManyRequests {
			c.logf("GraphQL request rate-limited (attempt %d/%d, HTTP %d, error: %s), retrying...", attempt, attempts, status, message)
		} else {
			c.logf("GraphQL request failed (attempt %d/%d, HTTP %d, error: %s), retrying...", attempt, attempts, status, message)
		}
		if attempt < attempts {
			delay := time.Duration(graphQLRetryBaseSecond) * time.Duration(1<<(attempt-1)) * time.Second
			if status == http.StatusTooManyRequests {
				delay = time.Duration(1<<attempt) * time.Second
				if delay > 16*time.Second {
					delay = 16 * time.Second
				}
			}
			c.sleep(ctx, delay)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("graphql request failed")
	}
	return lastErr
}

// queryGraphQLWithText sends one operation as a plain POST with its full query
// document. It is used only to recover from a missing persisted-query hash.
func (c *Client) queryGraphQLWithText(ctx context.Context, operation, queryText string, variables map[string]any, out any) error {
	payload, err := json.Marshal(struct {
		OperationName string         `json:"operationName"`
		Query         string         `json:"query"`
		Variables     map[string]any `json:"variables"`
	}{
		OperationName: operation,
		Query:         queryText,
		Variables:     variables,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphQLURL(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	c.applyHeaders(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GraphQL request rejected (HTTP %d)", resp.StatusCode)
	}
	var parsed graphQLResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return err
	}
	if len(parsed.Errors) > 0 && parsed.Errors[0].Message != "" {
		return fmt.Errorf("%s", parsed.Errors[0].Message)
	}
	if !graphQLDataOK(parsed.Data) {
		return errors.New("empty data")
	}
	if out != nil {
		return json.Unmarshal(parsed.Data, out)
	}
	return nil
}

type movieDetailsPayload struct {
	Movie *struct {
		ID             string          `json:"id"`
		Title          string          `json:"title"`
		ProductionYear json.RawMessage `json:"productionYear"`
		CustomData     CustomData      `json:"customData"`
		VideoSource    struct {
			DashURL string `json:"dashUrl"`
		} `json:"videoSource"`
	} `json:"movie"`
	Series *struct {
		Title   string `json:"title"`
		Seasons struct {
			Nodes []struct {
				ID             string `json:"id"`
				Title          string `json:"title"`
				SeasonInSeries int    `json:"seasonInSeries"`
			} `json:"nodes"`
		} `json:"seasons"`
	} `json:"series"`
}

// rawString renders a JSON scalar as a string; null and absent become "".
func rawString(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

// MovieDetails resolves a content id into either a movie or a series.
func (c *Client) MovieDetails(ctx context.Context, id string) (*Movie, *Series, error) {
	variables := map[string]any{
		"movieId":          id,
		"externalId":       id,
		"slug":             id,
		"potentialMovieId": id,
	}
	var payload movieDetailsPayload
	if err := c.queryGraphQL(ctx, operationMovieDetails, variables, &payload); err != nil {
		return nil, nil, err
	}
	var movie *Movie
	if payload.Movie != nil {
		movie = &Movie{
			ID:             payload.Movie.ID,
			Title:          payload.Movie.Title,
			ProductionYear: rawString(payload.Movie.ProductionYear),
			CustomData:     payload.Movie.CustomData,
			DashURL:        payload.Movie.VideoSource.DashURL,
		}
	}
	var series *Series
	if payload.Series != nil {
		series = &Series{Title: payload.Series.Title}
		for _, node := range payload.Series.Seasons.Nodes {
			series.Seasons = append(series.Seasons, Season{
				ID:             node.ID,
				Title:          node.Title,
				SeasonInSeries: node.SeasonInSeries,
			})
		}
	}
	if movie == nil && series == nil {
		return nil, nil, errors.New("invalid content (no movie or series in API response)")
	}
	return movie, series, nil
}

type episodesPayload struct {
	Episodes struct {
		Nodes []struct {
			ID              string          `json:"id"`
			EpisodeInSeason json.RawMessage `json:"episodeInSeason"`
			Title           string          `json:"title"`
			VideoSource     struct {
				DashURL string `json:"dashUrl"`
			} `json:"videoSource"`
		} `json:"nodes"`
	} `json:"episodes"`
}

// Episodes returns the episodes of one season in broadcast order.
func (c *Client) Episodes(ctx context.Context, seasonID string) ([]Episode, error) {
	var payload episodesPayload
	if err := c.queryGraphQL(ctx, operationEpisodes, map[string]any{"seasonId": seasonID}, &payload); err != nil {
		return nil, err
	}
	episodes := make([]Episode, 0, len(payload.Episodes.Nodes))
	for _, node := range payload.Episodes.Nodes {
		episode := Episode{
			ID:      node.ID,
			Title:   node.Title,
			DashURL: node.VideoSource.DashURL,
		}
		if number := rawString(node.EpisodeInSeason); number != "" {
			if parsed, err := strconv.Atoi(number); err == nil {
				value := parsed
				episode.EpisodeInSeason = &value
			}
		}
		episodes = append(episodes, episode)
	}
	return episodes, nil
}

type playbackRequest struct {
	ContentID   string      `json:"contentId"`
	ContentType string      `json:"contentType"`
	Download    bool        `json:"download"`
	AppInfo     appInfo     `json:"appInfo"`
	DeviceInfo  deviceInfo  `json:"deviceInfo"`
	Extra       interface{} `json:"-"`
}

type appInfo struct {
	Platform         string `json:"platform"`
	AppVersion       string `json:"appVersion"`
	Build            string `json:"build"`
	BundleIdentifier string `json:"bundleIdentifier"`
}

type deviceInfo struct {
	IsTouchDevice  bool   `json:"isTouchDevice"`
	IsTablet       bool   `json:"isTablet"`
	IsFireOS       bool   `json:"isFireOS"`
	AppPlatform    string `json:"appPlatform"`
	IsIOS          bool   `json:"isIOS"`
	IsCastReceiver bool   `json:"isCastReceiver"`
	IsSafari       bool   `json:"isSafari"`
	IsFirefox      bool   `json:"isFirefox"`
}

// PlaybackAuth requests the playback URL parameters for one content item.
func (c *Client) PlaybackAuth(ctx context.Context, contentID, contentType string) (string, error) {
	if contentID == "" || contentType == "" {
		return "", nil
	}
	payload := playbackRequest{
		ContentID:   contentID,
		ContentType: contentType,
		Download:    false,
		AppInfo: appInfo{
			Platform:         "web",
			AppVersion:       "1.0.0",
			Build:            "web",
			BundleIdentifier: "web",
		},
		DeviceInfo: deviceInfo{AppPlatform: "web"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL()+"/token/session", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	c.applyHeaders(req)
	// The Accept-Encoding header is deliberately not set: Go only decompresses
	// a gzip response transparently when it added the header itself. The
	// response is therefore unpacked by hand below.

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := readResponseBody(resp)
	if err != nil {
		return "", err
	}
	var parsed struct {
		AuthorizationParams *string `json:"authorizationParams"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", err
	}
	if c.Debugf != nil {
		var shape map[string]json.RawMessage
		_ = json.Unmarshal(data, &shape)
		keys := make([]string, 0, len(shape))
		for key := range shape {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		c.Debugf("token/session: HTTP %d, response keys: %s", resp.StatusCode, strings.Join(keys, ","))
	}
	if parsed.AuthorizationParams == nil {
		return "", nil
	}
	return *parsed.AuthorizationParams, nil
}

// DeletePlayback ends the tracked playback session for one content item.
func (c *Client) DeletePlayback(ctx context.Context, contentID, contentType string) error {
	if contentID == "" || contentType == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]string{"contentId": contentID, "contentType": contentType})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL()+"/token/session/playback/delete", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	c.applyHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

var ldJSONPattern = regexp.MustCompile(`(?s)<script type="application/ld\+json">\s*(.*?)\s*</script>`)
var tatortCategoryPattern = regexp.MustCompile(`"title":"((?:[^"\\]|\\.)*)","slug":"(tatort-[a-z0-9-]+)"`)

const tatortCityCollectionSlug = "tatort-cities"

// TatortCategory is one city in the Tatort cities collection.
type TatortCategory struct {
	Slug  string
	Title string
}

// CategoryPageURL returns the category page address for a slug.
func (c *Client) CategoryPageURL(slug string) string {
	return c.categoryURL() + "/" + slug
}

// TatortCategories reads only the city slugs from ARD Plus's "Tatort Städte"
// collection, not the many commissioner/team collections on the main hub.
func (c *Client) TatortCategories(ctx context.Context) ([]TatortCategory, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.CategoryPageURL(tatortCityCollectionSlug), nil)
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Tatort-Städteliste: HTTP %d", resp.StatusCode)
	}

	var categories []TatortCategory
	seen := make(map[string]bool)
	for _, match := range tatortCategoryPattern.FindAllSubmatch(body, -1) {
		slug := string(match[2])
		if seen[slug] {
			continue
		}
		seen[slug] = true
		title := string(match[1])
		var decoded string
		if err := json.Unmarshal([]byte(`"`+title+`"`), &decoded); err == nil {
			title = decoded
		}
		categories = append(categories, TatortCategory{Slug: slug, Title: title})
	}
	if len(categories) == 0 {
		return nil, errors.New("Tatort-Städteliste konnte nicht gelesen werden")
	}
	return categories, nil
}

// TatortEpisode is one entry of a Tatort category page.
type TatortEpisode struct {
	URL  string
	Name string
}

// TatortEpisodes reads the episode list of a Tatort category page.
func (c *Client) TatortEpisodes(ctx context.Context, slug string) ([]TatortEpisode, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.categoryURL()+"/"+slug, nil)
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Tatort-Kategorieseite %s: HTTP %d", slug, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var itemListJSON []byte
	for _, m := range ldJSONPattern.FindAllSubmatch(body, -1) {
		if bytes.Contains(m[1], []byte("itemListElement")) {
			itemListJSON = m[1]
			break
		}
	}
	if itemListJSON == nil {
		return nil, errors.New("could not parse Tatort episode list from category page")
	}
	// The list elements are schema.org ListItems that carry their Movie in a
	// nested "item" object; older pages put name and url at the top level.
	var parsed struct {
		ItemListElement []struct {
			URL  string `json:"url"`
			Name string `json:"name"`
			Item struct {
				URL  string `json:"url"`
				Name string `json:"name"`
			} `json:"item"`
		} `json:"itemListElement"`
	}
	if err := json.Unmarshal(itemListJSON, &parsed); err != nil {
		return nil, errors.New("could not parse Tatort episode list from category page")
	}
	episodes := make([]TatortEpisode, 0, len(parsed.ItemListElement))
	for _, element := range parsed.ItemListElement {
		episode := TatortEpisode{URL: element.URL, Name: element.Name}
		if element.Item.URL != "" {
			episode.URL = element.Item.URL
			episode.Name = element.Item.Name
		}
		episodes = append(episodes, episode)
	}
	return episodes, nil
}

var detailIDPattern = regexp.MustCompile(`.*/details/([^/-]+).*`)

// DetailID extracts the content id from an ARD Plus detail URL.
func DetailID(url string) string {
	m := detailIDPattern.FindStringSubmatch(url)
	if m == nil {
		return ""
	}
	return m[1]
}
