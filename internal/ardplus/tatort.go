// Package ardplus is the ARD Plus API client: the GraphQL metadata API, the
// playback authorization API and the Tatort category pages.
package ardplus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

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
