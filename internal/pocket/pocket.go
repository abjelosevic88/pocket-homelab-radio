// Package pocket talks to sibling Pocket apps: Pocket Music (is a song heard
// on the radio in your library, keep it in a playlist). Same bearer-token
// JSON API everywhere.
package pocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a bearer-token JSON client for one Pocket app.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New creates a client.
func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: timeout}}
}

// Configured reports whether URL and token are set.
func (c *Client) Configured() bool { return c != nil && c.BaseURL != "" && c.Token != "" }

// ErrNotFound is a 404 from the sibling app.
var ErrNotFound = errors.New("not found")

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, v any) error {
	if !c.Configured() {
		return errors.New("not configured")
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errors.New("token rejected (401)")
	}
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2000))
		_ = json.Unmarshal(b, &e)
		if e.Error != "" {
			return fmt.Errorf("HTTP %d: %s", res.StatusCode, e.Error)
		}
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if v == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// Health is the connection test for any Pocket app: /healthz plus a token check.
func (c *Client) Health(ctx context.Context) (string, error) {
	var out struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if !c.Configured() {
		return "", errors.New("not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
	if err != nil {
		return "", err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	var me map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/v1/auth/me", nil, nil, &me); err != nil {
		return out.Version, err
	}
	return out.Version, nil
}

// ---------------- Pocket Music ----------------

// Song is a song in Pocket Music's library (the fields Radio reads).
type Song struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Album    string `json:"album"`
	AlbumID  string `json:"album_id"`
	Artist   string `json:"artist"`
	ArtistID string `json:"artist_id"`
	Year     int    `json:"year"`
	Duration int    `json:"duration"`
}

// Playlist is a Pocket Music playlist.
type Playlist struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SongCount int    `json:"songCount"`
}

// SearchSongs finds songs in the person's library.
func (c *Client) SearchSongs(ctx context.Context, q string, limit int) ([]Song, error) {
	var out struct {
		Songs []Song `json:"songs"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/search", url.Values{"q": {q}, "limit": {fmt.Sprint(limit)}}, nil, &out)
	return out.Songs, err
}

// Playlists lists the person's playlists.
func (c *Client) Playlists(ctx context.Context) ([]Playlist, error) {
	var out []Playlist
	err := c.do(ctx, http.MethodGet, "/api/v1/playlists", nil, nil, &out)
	return out, err
}

// CreatePlaylist makes a playlist with these songs.
func (c *Client) CreatePlaylist(ctx context.Context, name, comment string, songIDs []string) (*Playlist, error) {
	var out Playlist
	err := c.do(ctx, http.MethodPost, "/api/v1/playlists", nil, map[string]any{"name": name, "comment": comment, "song_ids": songIDs}, &out)
	return &out, err
}

// AddToPlaylist appends songs to a playlist.
func (c *Client) AddToPlaylist(ctx context.Context, id string, songIDs []string) error {
	return c.do(ctx, http.MethodPut, "/api/v1/playlists/"+url.PathEscape(id), nil, map[string]any{"add": songIDs}, nil)
}
