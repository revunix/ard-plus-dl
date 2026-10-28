package ardplus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSlugifyWritesTheAddressOfTheWebClient(t *testing.T) {
	cases := map[string]string{
		"Die Bülent Ceylan Show":                 "die-buelent-ceylan-show",
		"1.000 Tode":                             "1000-tode",
		"Abby's fliegende Feenschule":            "abbys-fliegende-feenschule",
		"Angélique - Eine große Liebe in Gefahr": "angelique-eine-grosse-liebe-in-gefahr",
		"Belle & Sebastian":                      "belle-and-sebastian",
		"Tatort: Der stille Gast":                "tatort-der-stille-gast",
	}
	for title, want := range cases {
		if got := Slugify(title); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", title, got, want)
		}
	}
	item := CatalogItem{ID: "a0T010000008GkR", Title: "Die Bülent Ceylan Show"}
	if got, want := item.URL(), "https://www.ardplus.de/details/a0T010000008GkR-die-buelent-ceylan-show"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got := DetailID(item.URL()); got != item.ID {
		t.Fatalf("DetailID of the catalog URL = %q, want %q", got, item.ID)
	}
}

func TestCatalogListsSeriesAndPlayableMovies(t *testing.T) {
	var operations []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var request struct {
			OperationName string `json:"operationName"`
			Query         string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		operations = append(operations, request.OperationName)
		switch request.OperationName {
		case "CatalogSeries":
			_, _ = w.Write([]byte(`{"data":{"allCmsSeries":{"nodes":[
				{"id":"a0T1","title":"Zeta Serie","productionYear":2018,"shortSynopsis":"Eine\n   Serie.",
				 "cmsSeasonsBySeriesId":{"totalCount":2},
				 "cmsMovieContentCategoriesByContentSeriesId":{"nodes":[{"category":{"title":"Drama"}},null,{"category":{"title":"Drama"}}]}}]}}}`))
		case "CatalogMovies":
			if !strings.Contains(request.Query, "isStreamable") {
				t.Errorf("movie query does not filter on playable movies: %s", request.Query)
			}
			_, _ = w.Write([]byte(`{"data":{"allCmsMovies":{"nodes":[
				{"id":"a0S1","title":"Alpha Film","productionYear":"2014","runtimeInSeconds":3808,"shortSynopsis":"Ein Film.",
				 "cmsMovieContentCategoriesByContentMovieId":{"nodes":[{"category":{"title":"Krimi"}},{"category":{"title":"Tatort: 2010 - 2015"}}]}}]}}}`))
		default:
			t.Errorf("unexpected operation %q", request.OperationName)
		}
	}))
	defer srv.Close()

	client := &Client{GraphQLURL: srv.URL}
	items, err := client.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(operations) != 2 {
		t.Fatalf("operations = %v, want two queries", operations)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(items), items)
	}
	movie, series := items[0], items[1]
	if movie.Title != "Alpha Film" || movie.Kind != CatalogMovie || movie.Year != 2014 || movie.Runtime != 3808 {
		t.Fatalf("movie = %+v", movie)
	}
	if strings.Join(movie.Genres, "|") != "Krimi|Tatort: 2010 - 2015" {
		t.Fatalf("movie genres = %v", movie.Genres)
	}
	if series.Title != "Zeta Serie" || series.Kind != CatalogSeries || series.Seasons != 2 || series.Synopsis != "Eine Serie." {
		t.Fatalf("series = %+v", series)
	}
	if strings.Join(series.Genres, "|") != "Drama" {
		t.Fatalf("series genres = %v", series.Genres)
	}
}
