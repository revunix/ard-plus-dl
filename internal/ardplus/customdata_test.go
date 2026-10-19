package ardplus

import (
	"context"
	"testing"
)

// The ARD Plus API sends customData as a JSON string holding the object, which
// used to fail the whole MovieDetails call.
func TestMovieDetailsAcceptsCustomDataAsString(t *testing.T) {
	body := `{"data":{"movie":{"id":"m1","title":"Die Schöpfung","productionYear":2026,
        "customData":"{\"lra\":\"wdr\",\"team\":\"Ballauf und Schenk\",\"location\":\"Köln\",\"drmEncrypted\":false,\"specialCategory\":\"Tatort\"}",
        "videoSource":{"dashUrl":"https://cdn.test/m.mpd"}}}}`
	_, srv := newSequenceServer(t, step{status: 200, body: body})
	client := newClient(srv, nil)

	movie, series, err := client.MovieDetails(context.Background(), "m1")
	if err != nil {
		t.Fatalf("MovieDetails: %v", err)
	}
	if series != nil {
		t.Fatalf("unexpected series: %+v", series)
	}
	if movie == nil || movie.Title != "Die Schöpfung" || movie.ProductionYear != "2026" {
		t.Fatalf("movie = %+v", movie)
	}
	if movie.CustomData.Team != "Ballauf und Schenk" || movie.CustomData.Location != "Köln" {
		t.Fatalf("customData = %+v", movie.CustomData)
	}
	if movie.CustomData.EpisodeProductionNumber != "" {
		t.Fatalf("episodeProductionNumber = %q, want empty", movie.CustomData.EpisodeProductionNumber)
	}
}

func TestMovieDetailsAcceptsCustomDataAsObject(t *testing.T) {
	body := `{"data":{"movie":{"id":"m2","title":"Fiderallala","productionYear":2025,
        "customData":{"team":"Thiel und Boerne","location":"Münster","episodeProductionNumber":"1200"},
        "videoSource":{"dashUrl":"https://cdn.test/m2.mpd"}}}}`
	_, srv := newSequenceServer(t, step{status: 200, body: body})
	client := newClient(srv, nil)

	movie, _, err := client.MovieDetails(context.Background(), "m2")
	if err != nil {
		t.Fatalf("MovieDetails: %v", err)
	}
	if movie.CustomData.Team != "Thiel und Boerne" || movie.CustomData.EpisodeProductionNumber != "1200" {
		t.Fatalf("customData = %+v", movie.CustomData)
	}
}

func TestMovieDetailsAcceptsMissingCustomData(t *testing.T) {
	for name, custom := range map[string]string{
		"null":      `null`,
		"absent":    ``,
		"empty":     `""`,
		"plaintext": `"not json"`,
	} {
		t.Run(name, func(t *testing.T) {
			field := ""
			if custom != "" {
				field = `,"customData":` + custom
			}
			body := `{"data":{"movie":{"id":"m3","title":"Ohne Zusatz","productionYear":null` + field +
				`,"videoSource":{"dashUrl":"https://cdn.test/m3.mpd"}}}}`
			_, srv := newSequenceServer(t, step{status: 200, body: body})
			client := newClient(srv, nil)
			movie, _, err := client.MovieDetails(context.Background(), "m3")
			if err != nil {
				t.Fatalf("MovieDetails: %v", err)
			}
			if movie.CustomData.Team != "" || movie.CustomData.Location != "" {
				t.Fatalf("customData = %+v, want empty", movie.CustomData)
			}
			if movie.ProductionYear != "" {
				t.Fatalf("productionYear = %q, want empty for null", movie.ProductionYear)
			}
		})
	}
}
