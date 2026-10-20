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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/revunix/ard-plus-dl/internal/auth"
)

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
