package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// withWindowsNaming flips the platform switch so the Windows-only rules can be
// exercised on any host.
func withWindowsNaming(t *testing.T, on bool) {
	t.Helper()
	prev := windowsNaming
	windowsNaming = on
	t.Cleanup(func() { windowsNaming = prev })
}

func TestNormalizeURL(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"strips trailing slash", "https://www.ardplus.de/details/foo/", "https://www.ardplus.de/details/foo"},
		{"strips comments", "https://www.ardplus.de/details/foo # note", "https://www.ardplus.de/details/foo"},
		{"trims whitespace", "  https://www.ardplus.de/details/foo  ", "https://www.ardplus.de/details/foo"},
		{"strips query string", "https://www.ardplus.de/details/a0S01000000EWYi-lola-rennt?utm_source=share", "https://www.ardplus.de/details/a0S01000000EWYi-lola-rennt"},
		{"strips query and trailing slash", "https://www.ardplus.de/details/foo/?ref=1", "https://www.ardplus.de/details/foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeURL(tc.in); got != tc.want {
				t.Fatalf("NormalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizePathComponent(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"replaces single slash", "A/B", "A B"},
		{"replaces all slashes", "A/B/C", "A B C"},
		{"leaves title without slashes unchanged", "Normal Title", "Normal Title"},
		{"strips glob and bracket characters", "A*?[]C", "AC"},
		{"strips control characters and newlines", "A\nB", "AB"},
		{"trims surrounding dots and spaces", " ..Title.. ", "Title"},
		{"trims leading dots", "..Title", "Title"},
		{"empty title becomes placeholder", "", "_"},
		{"dotted title becomes placeholder", " ... ", "_"},
		{"collapses runs of spaces", "A   B", "A B"},
		{"collapses spaces left by slash replacement", "A / B", "A B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizePathComponent(tc.in); got != tc.want {
				t.Fatalf("SanitizePathComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizePathComponentWindows(t *testing.T) {
	withWindowsNaming(t, true)
	cases := []struct{ name, in, want string }{
		{"strips reserved characters", `A<B>C:D"E\F|G`, "ABCDEFG"},
		{"trims trailing dots", "Name...", "Name"},
		{"trims trailing spaces", "Name   ", "Name"},
		{"prefixes the CON device name", "con", "_con"},
		{"prefixes a device name with extension", "NUL.txt", "_NUL.txt"},
		{"prefixes numbered COM ports", "COM1", "_COM1"},
		{"prefixes numbered LPT ports", "lpt9.log", "_lpt9.log"},
		{"keeps a name that only starts like a device", "CONSOLE", "CONSOLE"},
		{"keeps other names", "Tatort Köln", "Tatort Köln"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizePathComponent(tc.in); got != tc.want {
				t.Fatalf("SanitizePathComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizePathComponentUnixKeepsPlatformNeutralCharacters(t *testing.T) {
	withWindowsNaming(t, false)
	cases := []struct{ name, in, want string }{
		{"keeps Windows-reserved characters", `A<B>C:D"E\F|G`, `A<B>C:D"E\F|G`},
		{"does not prefix device names", "con", "con"},
		{"does not prefix numbered device names", "COM1", "COM1"},
		{"still trims trailing dots", "Name...", "Name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizePathComponent(tc.in); got != tc.want {
				t.Fatalf("SanitizePathComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDotsComponent(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"spaces become dots", "My Show", "My.Show"},
		{"slashes become dots", "A/B", "A.B"},
		{"runs of spaces collapse", "A   B", "A.B"},
		{"surrounding dots and spaces are trimmed", "  ..A  B.. ", "A.B"},
		{"glob characters are removed", "A*?[]B", "AB"},
		{"umlauts are kept", "Köln", "Köln"},
		{"empty value becomes placeholder", "", "_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DotsComponent(tc.in); got != tc.want {
				t.Fatalf("DotsComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCapComponentBytes(t *testing.T) {
	t.Run("caps an over-long component at the byte limit", func(t *testing.T) {
		if got := len(CapComponentBytes(200, strings.Repeat("a", 400))); got != 200 {
			t.Fatalf("len = %d, want 200", got)
		}
	})
	t.Run("keeps a short component unchanged", func(t *testing.T) {
		if got := CapComponentBytes(200, "short"); got != "short" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("caps multi-byte text within the byte limit", func(t *testing.T) {
		capped := CapComponentBytes(200, strings.Repeat("ä", 300))
		if len(capped) > 200 {
			t.Fatalf("len = %d, want <= 200", len(capped))
		}
		if !utf8.ValidString(capped) {
			t.Fatalf("truncation produced invalid UTF-8: %q", capped)
		}
		// A byte-wise cut would leave a partial character behind, which would
		// survive this substitution.
		if rest := strings.ReplaceAll(capped, "ä", ""); rest != "" {
			t.Fatalf("truncation kept partial characters: %q", rest)
		}
	})
}

func TestTrimPathComponent(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"trims a dot exposed by a space", " .A. ", "A"},
		{"trims alternating dots and spaces", ". .A. .", "A"},
		{"collapses inner space runs", "A    B", "A B"},
		{"keeps inner dots", "A.B", "A.B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TrimPathComponent(tc.in); got != tc.want {
				t.Fatalf("TrimPathComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildMoviePath(t *testing.T) {
	cases := []struct{ name, title, year, want string }{
		{"movie path nests the title under title and year", "Lola rennt", "1998", "Lola rennt (1998)/Lola rennt"},
		{"movie path without a year omits the year", "Lola rennt", "", "Lola rennt/Lola rennt"},
		{"movie title slashes stay inside one directory", "Der Fall/X: Y", "2020", "Der Fall X: Y (2020)/Der Fall X: Y"},
		{"quotes in a movie title survive", `Say "Hi"`, "", `Say "Hi"/Say "Hi"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildMoviePath(tc.title, tc.year); got != tc.want {
				t.Fatalf("BuildMoviePath(%q, %q) = %q, want %q", tc.title, tc.year, got, tc.want)
			}
		})
	}
}

func TestBuildEpisodePath(t *testing.T) {
	no := func(n int) *int { return &n }
	cases := []struct {
		name    string
		show    string
		season  int
		episode *int
		title   string
		want    string
	}{
		{"episode path numbers season and episode", "Gegen den Wind", 1, no(3), "Schönes Wochenende", "Gegen den Wind/Season 01/Gegen den Wind S01E03 - Schönes Wochenende"},
		{"episode path pads season and episode to two digits", "Show", 12, no(7), "Finale", "Show/Season 12/Show S12E07 - Finale"},
		{"episode without a number falls back to a placeholder", "Show", 3, nil, "Pilot", "Show/Season 03/Show S03E?? - Pilot"},
		{"episode title is sanitised inside the path", "Show", 1, no(1), "a/b?c", "Show/Season 01/Show S01E01 - a bc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildEpisodePath(tc.show, tc.season, tc.episode, tc.title); got != tc.want {
				t.Fatalf("BuildEpisodePath(%q, %d, %v, %q) = %q, want %q", tc.show, tc.season, tc.episode, tc.title, got, tc.want)
			}
		})
	}
}

func TestBuildTatortPath(t *testing.T) {
	cases := []struct{ name, city, team, episodeNo, title, year, want string }{
		{"tatort path with team, episode and year", "Köln", "Bucerius", "850", "Der Fall", "2019", "Tatort Köln (Bucerius) - Folge 850 - Der Fall (2019)"},
		{"tatort path without a team drops the team", "Bremen", "", "", "Der Fall", "", "Tatort Bremen - Der Fall"},
		{"tatort path without an episode drops the Folge part", "Bremen", "Team X", "", "Der Fall", "", "Tatort Bremen (Team X) - Der Fall"},
		{"tatort path keeps the title year", "Köln", "", "1", "Der Fall", "2020", "Tatort Köln - Folge 1 - Der Fall (2020)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildTatortPath(tc.city, tc.team, tc.episodeNo, tc.title, tc.year); got != tc.want {
				t.Fatalf("BuildTatortPath(%q, %q, %q, %q, %q) = %q, want %q",
					tc.city, tc.team, tc.episodeNo, tc.title, tc.year, got, tc.want)
			}
		})
	}
}

func TestPathLengthLimitIsCreatableOnDisk(t *testing.T) {
	longTitle := strings.Repeat("Titel ", 60)
	tmp := t.TempDir()

	cases := []struct{ name, staging string }{
		{"movie path with a 360-byte title is creatable", BuildMoviePath(longTitle, "2024")},
		{"episode path with a 360-byte title is creatable", BuildEpisodePath(longTitle, 1, new(1), longTitle)},
		{"tatort path with a 360-byte title is creatable", BuildTatortPath("München", "Leitmayr", "1200", longTitle, "2024")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(tmp, filepath.FromSlash(tc.staging)+".mp4")
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(out, []byte("video"), 0o644); err != nil {
				t.Fatalf("composed path is not creatable: %v", err)
			}
		})
	}
}

func TestEpisodePadding(t *testing.T) {
	cases := []struct {
		name       string
		maxEpisode int
		want       int
	}{
		{"flat numbering keeps two digits", 99, 2},
		{"the 100th episode switches to three digits", 100, 3},
		{"a long season uses three digits", 151, 3},
		{"a missing maximum uses two digits", 0, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EpisodePadding(tc.maxEpisode); got != tc.want {
				t.Fatalf("EpisodePadding(%d) = %d, want %d", tc.maxEpisode, got, tc.want)
			}
		})
	}
}

func TestOutputNameStems(t *testing.T) {
	t.Run("episode base uses dots and padded numbers", func(t *testing.T) {
		if got := EpisodeNameStem("My Show", 1, new(3), 2); got != "My.Show.S01E03" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("episode base pads two-digit seasons", func(t *testing.T) {
		if got := EpisodeNameStem("My Show", 12, new(7), 2); got != "My.Show.S12E07" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("episode base keeps the placeholder", func(t *testing.T) {
		if got := EpisodeNameStem("My Show", 3, nil, 2); got != "My.Show.S03E??" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("episode base honours the padding argument", func(t *testing.T) {
		if got := EpisodeNameStem("My Show", 21, new(1), 3); got != "My.Show.S21E001" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("the placeholder ignores the padding", func(t *testing.T) {
		if got := EpisodeNameStem("My Show", 21, nil, 3); got != "My.Show.S21E??" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("season stem appends the season number", func(t *testing.T) {
		if got := SeasonNameStem("My Show", 1); got != "My.Show.S01" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("movie stem joins title and year", func(t *testing.T) {
		if got := MovieNameStem("Lola rennt", "1998"); got != "Lola.rennt.1998" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("movie stem without a year", func(t *testing.T) {
		if got := MovieNameStem("Lola rennt", ""); got != "Lola.rennt" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("tatort base carries city, episode and year", func(t *testing.T) {
		if got := TatortNameStem("Köln", "850", "2019"); got != "Tatort.Köln.Folge.850.2019" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("tatort base without a year", func(t *testing.T) {
		if got := TatortNameStem("Köln", "850", ""); got != "Tatort.Köln.Folge.850" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("tatort base without episode and year", func(t *testing.T) {
		if got := TatortNameStem("Köln", "", ""); got != "Tatort.Köln" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("the Datei-Tag is appended", func(t *testing.T) {
		if got := ApplyFileTag("German.AAC.1080p.Web-DL.x264", "example"); got != "German.AAC.1080p.Web-DL.x264-example" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestMediaLabels(t *testing.T) {
	t.Run("resolution labels", func(t *testing.T) {
		cases := []struct {
			name   string
			height int
			want   string
		}{
			{"2160 maps to 2160p", 2160, "2160p"},
			{"1080 maps to 1080p", 1080, "1080p"},
			{"unknown heights use thresholds", 1084, "1080p"},
			{"heights below 480 keep their value", 300, "300p"},
			{"missing height is unknown", 0, "Unknown"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := ResolutionLabel(tc.height); got != tc.want {
					t.Fatalf("ResolutionLabel(%d) = %q, want %q", tc.height, got, tc.want)
				}
			})
		}
	})
	t.Run("video codec labels", func(t *testing.T) {
		cases := []struct{ name, codec, want string }{
			{"h264 becomes x264", "h264", "x264"},
			{"hevc becomes x265", "hevc", "x265"},
			{"av1 stays av1", "av1", "AV1"},
			{"unknown video codec is uppercased", "mpeg4", "MPEG4"},
			{"missing video codec is unknown", "", "Unknown"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := VideoCodecLabel(tc.codec); got != tc.want {
					t.Fatalf("VideoCodecLabel(%q) = %q, want %q", tc.codec, got, tc.want)
				}
			})
		}
	})
	t.Run("audio codec labels", func(t *testing.T) {
		cases := []struct {
			name     string
			codec    string
			channels int
			profile  string
			want     string
		}{
			{"aac stays aac", "aac", 2, "", "AAC"},
			{"stereo ac3 has no channel suffix", "ac3", 2, "", "AC3"},
			{"surround ac3 carries the channel count", "ac3", 6, "", "AC3.6ch"},
			{"eac3 carries the channel count", "eac3", 6, "", "EAC3.6ch"},
			{"dts-hd ma is labelled", "dts", 8, "DTS-HD MA", "DTS-HD.MA"},
			{"dts-hd hra is labelled", "dts", 8, "DTS-HD HRA", "DTS-HD.HRA"},
			{"plain dts is labelled", "dts", 6, "DTS", "DTS"},
			{"truehd is labelled", "truehd", 8, "", "TrueHD"},
			{"opus is labelled", "opus", 2, "", "Opus"},
			{"mp3 is labelled", "mp3", 2, "", "MP3"},
			{"flac is labelled", "flac", 2, "", "FLAC"},
			{"unknown audio codec is uppercased", "dtsx", 2, "", "DTSX"},
			{"missing audio codec is unknown", "", 2, "", "Unknown"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := AudioCodecLabel(tc.codec, tc.channels, tc.profile); got != tc.want {
					t.Fatalf("AudioCodecLabel(%q, %d, %q) = %q, want %q", tc.codec, tc.channels, tc.profile, got, tc.want)
				}
			})
		}
	})
}

func TestSanitizeToken(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"keeps a year", "2024", "2024"},
		{"keeps an episode number", "1200", "1200"},
		{"drops path separators", "../../etc", "etc"},
		{"drops a leading dot run", "../../../etc/passwd", "etcpasswd"},
		{"collapses dot runs", "a..b", "a.b"},
		{"drops spaces and slashes", "12/34 56", "123456"},
		{"trims outer dots", ".hidden.", "hidden"},
		{"empty stays empty", "", ""},
		{"only separators becomes empty", "../..", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeToken(tc.in); got != tc.want {
				t.Fatalf("SanitizeToken(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The year and the episode number come from the API and end up in file and
// directory names. They must never be able to leave the downloads directory.
func TestAPIValuesCannotEscapeDownloadsDir(t *testing.T) {
	const downloads = "/data/downloads"
	hostile := []string{
		"../../../../../../etc/cron.d/pwn",
		"..",
		"/etc/passwd",
		"....//....//etc",
		"a/b/../../../c",
	}

	for _, value := range hostile {
		t.Run(value, func(t *testing.T) {
			paths := []string{
				BuildMoviePath("Some Movie", value),
				BuildTatortPath("Köln", "Team", value, "Episode", "2024"),
				BuildTatortPath("Köln", "Team", "1200", "Episode", value),
				MovieNameStem("Some Movie", value),
				TatortNameStem("Köln", value, "2024"),
				TatortNameStem("Köln", "1200", value),
			}
			for _, path := range paths {
				joined := filepath.Join(downloads, filepath.FromSlash(path))
				rel, err := filepath.Rel(downloads, joined)
				if err != nil {
					t.Fatalf("Rel(%q, %q): %v", downloads, joined, err)
				}
				if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					t.Errorf("%q escapes the downloads directory: %q", path, joined)
				}
			}
		})
	}
}
