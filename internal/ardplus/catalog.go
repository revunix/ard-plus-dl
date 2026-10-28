package ardplus

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// The catalog comes from the same GraphQL API the web client uses. Its list
// queries need no login: they return the metadata of every title, not the
// streams.

// DetailsURL is the address of the detail pages of the web client.
const DetailsURL = "https://www.ardplus.de/details"

// Kinds of catalog items.
const (
	CatalogMovie  = "movie"
	CatalogSeries = "series"
)

// CatalogItem is one title of the ARD Plus catalog: a movie or a series.
type CatalogItem struct {
	ID       string
	Title    string
	Kind     string
	Year     int
	Runtime  int
	Seasons  int
	Synopsis string
	Genres   []string
}

// URL returns the detail page of the title, written like the web client
// writes it.
func (item CatalogItem) URL() string {
	slug := Slugify(item.Title)
	if slug == "" {
		return DetailsURL + "/" + item.ID
	}
	return DetailsURL + "/" + item.ID + "-" + slug
}

// catalogMoviesQuery lists the movies that can be played right now. The
// web client hides the other ones as "nicht verfügbar".
const catalogMoviesQuery = `
query CatalogMovies {
  allCmsMovies(filter: {isStreamable: {equalTo: true}}, orderBy: TITLE_ASC) {
    nodes {
      id
      title
      productionYear
      runtimeInSeconds
      shortSynopsis
      cmsMovieContentCategoriesByContentMovieId { nodes { category { title } } }
    }
  }
}`

// catalogSeriesQuery lists the series. The streamable flag of a series is
// computed over all its episodes and too slow to ask for, so every series
// is listed.
const catalogSeriesQuery = `
query CatalogSeries {
  allCmsSeries(orderBy: TITLE_ASC) {
    nodes {
      id
      title
      productionYear
      shortSynopsis
      cmsSeasonsBySeriesId { totalCount }
      cmsMovieContentCategoriesByContentSeriesId { nodes { category { title } } }
    }
  }
}`

type catalogCategories struct {
	Nodes []*struct {
		Category *struct {
			Title string `json:"title"`
		} `json:"category"`
	} `json:"nodes"`
}

// titles returns the names of the categories in their order. Some titles
// are linked to the same category twice; the name stands only once.
func (c catalogCategories) titles() []string {
	var titles []string
	seen := map[string]bool{}
	for _, node := range c.Nodes {
		if node == nil || node.Category == nil || node.Category.Title == "" || seen[node.Category.Title] {
			continue
		}
		seen[node.Category.Title] = true
		titles = append(titles, node.Category.Title)
	}
	return titles
}

type catalogMoviesPayload struct {
	AllCmsMovies struct {
		Nodes []struct {
			ID               string            `json:"id"`
			Title            string            `json:"title"`
			ProductionYear   json.RawMessage   `json:"productionYear"`
			RuntimeInSeconds int               `json:"runtimeInSeconds"`
			ShortSynopsis    string            `json:"shortSynopsis"`
			Categories       catalogCategories `json:"cmsMovieContentCategoriesByContentMovieId"`
		} `json:"nodes"`
	} `json:"allCmsMovies"`
}

type catalogSeriesPayload struct {
	AllCmsSeries struct {
		Nodes []struct {
			ID             string          `json:"id"`
			Title          string          `json:"title"`
			ProductionYear json.RawMessage `json:"productionYear"`
			ShortSynopsis  string          `json:"shortSynopsis"`
			Seasons        struct {
				TotalCount int `json:"totalCount"`
			} `json:"cmsSeasonsBySeriesId"`
			Categories catalogCategories `json:"cmsMovieContentCategoriesByContentSeriesId"`
		} `json:"nodes"`
	} `json:"allCmsSeries"`
}

// Catalog returns every series and every playable movie of ARD Plus,
// ordered by title.
func (c *Client) Catalog(ctx context.Context) ([]CatalogItem, error) {
	var series catalogSeriesPayload
	if err := c.queryGraphQLWithText(ctx, "CatalogSeries", catalogSeriesQuery, map[string]any{}, &series); err != nil {
		return nil, fmt.Errorf("Serienkatalog: %w", err)
	}
	var movies catalogMoviesPayload
	if err := c.queryGraphQLWithText(ctx, "CatalogMovies", catalogMoviesQuery, map[string]any{}, &movies); err != nil {
		return nil, fmt.Errorf("Filmkatalog: %w", err)
	}

	items := make([]CatalogItem, 0, len(series.AllCmsSeries.Nodes)+len(movies.AllCmsMovies.Nodes))
	for _, node := range series.AllCmsSeries.Nodes {
		if node.ID == "" || node.Title == "" {
			continue
		}
		items = append(items, CatalogItem{
			ID:       node.ID,
			Title:    strings.TrimSpace(node.Title),
			Kind:     CatalogSeries,
			Year:     rawInt(node.ProductionYear),
			Seasons:  node.Seasons.TotalCount,
			Synopsis: cleanSynopsis(node.ShortSynopsis),
			Genres:   node.Categories.titles(),
		})
	}
	for _, node := range movies.AllCmsMovies.Nodes {
		if node.ID == "" || node.Title == "" {
			continue
		}
		items = append(items, CatalogItem{
			ID:       node.ID,
			Title:    strings.TrimSpace(node.Title),
			Kind:     CatalogMovie,
			Year:     rawInt(node.ProductionYear),
			Runtime:  node.RuntimeInSeconds,
			Synopsis: cleanSynopsis(node.ShortSynopsis),
			Genres:   node.Categories.titles(),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
}

// rawInt reads a JSON number that may also come as a string or null.
func rawInt(raw json.RawMessage) int {
	value := strings.Trim(rawString(raw), `"`)
	var number int
	if _, err := fmt.Sscanf(value, "%d", &number); err != nil {
		return 0
	}
	return number
}

// cleanSynopsis folds the line breaks and indentation of the editors into
// single spaces.
func cleanSynopsis(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// Slugify writes a title the way the web client writes it in the address of
// a detail page: lower case, umlauts spelled out, every other character a
// dash.
func Slugify(title string) string {
	replacer := strings.NewReplacer(
		"ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss", "&", " and ",
		// Apostrophes and dots join the parts of a word.
		"'", "", "’", "", "´", "", "`", "", ".", "",
		"à", "a", "á", "a", "â", "a", "ã", "a", "å", "a", "æ", "ae",
		"ç", "c", "è", "e", "é", "e", "ê", "e", "ë", "e",
		"ì", "i", "í", "i", "î", "i", "ï", "i", "ñ", "n",
		"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ø", "o", "œ", "oe",
		"ù", "u", "ú", "u", "û", "u", "ý", "y", "ÿ", "y")
	var builder strings.Builder
	dash := false
	for _, r := range replacer.Replace(strings.ToLower(title)) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			builder.WriteRune(r)
			dash = false
			continue
		}
		if !dash && builder.Len() > 0 {
			builder.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(builder.String(), "-")
}
