// Package ardplus is the ARD Plus API client: the GraphQL metadata API, the
// playback authorization API and the Tatort category pages.
package ardplus

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

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
