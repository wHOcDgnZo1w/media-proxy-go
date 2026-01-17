// Package stremio provides a Stremio addon for DVR recordings.
package stremio

// Manifest is the Stremio addon manifest.
var Manifest = map[string]interface{}{
	"id":          "org.stremio.dvr-local",
	"version":     "1.0.0",
	"name":        "DVR Recordings",
	"description": "Local addon for MediaProxy DVR recordings",
	"resources":   []string{"catalog", "stream", "meta"},
	"types":       []string{"tv"},
	"catalogs": []map[string]interface{}{
		{
			"type": "tv",
			"id":   "dvr-recordings",
			"name": "DVR Recordings",
			"extra": []map[string]interface{}{
				{
					"name":       "genre",
					"isRequired": false,
					"options":    []string{"All Recordings"},
				},
				{
					"name":       "search",
					"isRequired": false,
				},
			},
		},
	},
	"idPrefixes": []string{"dvr:"},
}

// Meta represents a Stremio catalog item.
type Meta struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Poster      string `json:"poster,omitempty"`
	Description string `json:"description,omitempty"`
	ReleaseInfo string `json:"releaseInfo,omitempty"`
	Runtime     string `json:"runtime,omitempty"`
}

// Stream represents a Stremio stream item.
type Stream struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}
