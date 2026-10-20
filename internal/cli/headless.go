// Package cli is the headless entry point: argument handling, dependency
// checks, wiring and exit codes.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/revunix/ard-plus-dl/internal/app"
	"github.com/revunix/ard-plus-dl/internal/config"
)

type headlessUI struct {
	out         io.Writer
	in          io.Reader
	interactive bool
}

func (u *headlessUI) Start(context.Context, *config.Options) error { return nil }

func (u *headlessUI) Event(event app.Event) {
	if event.Kind == app.EventFailed && event.Message != "" {
		fmt.Fprintf(u.out, "%s\n", event.Message)
	}
}

func (u *headlessUI) SelectSeasons(ctx context.Context, show string, seasons []app.Season) ([]int, error) {
	if !u.interactive {
		return nil, errors.New("no season selected (input ended)")
	}
	for _, season := range seasons {
		fmt.Fprintf(u.out, "%5d\t%s\n", season.Number, season.Title)
	}
	for {
		fmt.Fprintf(u.out, "Welche Staffel möchtest du runterladen? ")
		raw := readLine(ctx, u.in)
		if raw == "" {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, errors.New("no season selected (input ended)")
		}
		var selected []int
		valid := true
		for _, field := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
			number, err := parseSeasonNumber(field)
			if err != nil || !hasSeason(seasons, number) {
				valid = false
				break
			}
			selected = append(selected, number)
		}
		if valid && len(selected) > 0 {
			return selected, nil
		}
		fmt.Fprintln(u.out, "Ungültige Staffelauswahl, bitte erneut versuchen.")
	}
}

func (u *headlessUI) SelectCategories(ctx context.Context, title string, categories []app.Category) ([]string, error) {
	if !u.interactive {
		return nil, errors.New("no category selected (input ended)")
	}
	fmt.Fprintf(u.out, "%s:\n", title)
	for i, category := range categories {
		fmt.Fprintf(u.out, "%4d\t%s\n", i+1, category.Title)
	}
	for {
		fmt.Fprintf(u.out, "Welche Kategorien? (Nummern, 'alle') ")
		raw := strings.ToLower(readLine(ctx, u.in))
		if raw == "" {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, errors.New("no category selected (input ended)")
		}
		if raw == "alle" || raw == "all" {
			slugs := make([]string, 0, len(categories))
			for _, category := range categories {
				slugs = append(slugs, category.Slug)
			}
			return slugs, nil
		}
		var slugs []string
		valid := true
		for _, field := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
			number, err := parseSeasonNumber(field)
			if err != nil || number < 1 || number > len(categories) {
				valid = false
				break
			}
			slugs = append(slugs, categories[number-1].Slug)
		}
		if valid && len(slugs) > 0 {
			return slugs, nil
		}
		fmt.Fprintln(u.out, "Ungültige Auswahl, bitte erneut versuchen.")
	}
}

func (u *headlessUI) AskTatortSkip(ctx context.Context, _ string, _ int) (int, error) {
	if !u.interactive {
		return 0, nil
	}
	fmt.Fprintf(u.out, "Wie viele Episoden möchtest du überspringen? (0=alle laden) ")
	raw := readLine(ctx, u.in)
	if raw == "" {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	number, err := parseSeasonNumber(raw)
	if err != nil {
		return 0, nil
	}
	return number, nil
}

func parseSeasonNumber(field string) (int, error) {
	if field == "" {
		return 0, errors.New("empty")
	}
	number := 0
	for _, r := range field {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		number = number*10 + int(r-'0')
	}
	return number, nil
}

func hasSeason(seasons []app.Season, number int) bool {
	for _, season := range seasons {
		if season.Number == number {
			return true
		}
	}
	return false
}
