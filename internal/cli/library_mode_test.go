package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revunix/ard-plus-dl/internal/store"
)

// TestPrintLibraryJSON checks the machine readable list of the library.
func TestPrintLibraryJSON(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer state.Close()
	if _, err := state.UpsertSeries("https://www.ardplus.de/details/x", "Test Show", store.KindSeries); err != nil {
		t.Fatalf("add series: %v", err)
	}
	entries, err := state.SeriesList()
	if err != nil {
		t.Fatalf("read library: %v", err)
	}

	var out strings.Builder
	if code := printLibraryJSON(&out, entries); code != 0 {
		t.Fatalf("exit code = %d, output: %s", code, out.String())
	}
	var list []struct {
		ID       int64  `json:"id"`
		URL      string `json:"url"`
		Title    string `json:"title"`
		Kind     string `json:"kind"`
		Enabled  bool   `json:"enabled"`
		NewCount int    `json:"new_count"`
		AddedAt  string `json:"added_at"`
	}
	if err := json.Unmarshal([]byte(out.String()), &list); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if len(list) != 1 {
		t.Fatalf("list holds %d entries, want 1", len(list))
	}
	if list[0].Title != "Test Show" || list[0].URL != "https://www.ardplus.de/details/x" {
		t.Fatalf("entry = %+v, want the stored series", list[0])
	}
	if list[0].Kind != store.KindSeries {
		t.Fatalf("kind = %q, want %q", list[0].Kind, store.KindSeries)
	}
	if !list[0].Enabled {
		t.Fatal("the entry should be enabled")
	}
}
