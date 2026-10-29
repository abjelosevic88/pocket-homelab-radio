package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

const channelCols = `id, user_id, name, description, rules, color, bitrate, listen_token, last_played_at, created_at, updated_at`

func scanChannel(sc interface{ Scan(...any) error }) (*Channel, error) {
	var c Channel
	var rules string
	var played sql.NullString
	if err := sc.Scan(&c.ID, &c.UserID, &c.Name, &c.Description, &rules, &c.Color, &c.Bitrate, &c.ListenToken, &played, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(rules), &c.Rules)
	c.Rules.Clean()
	if played.Valid {
		c.LastPlayedAt = &played.String
	}
	return &c, nil
}

// Clean makes empty lists non-nil (so they encode as []) and keeps the
// numbers sane.
func (r *ChannelRules) Clean() {
	for _, l := range []*[]string{&r.Genres, &r.ExcludeGenres, &r.Artists, &r.ExcludeArtists, &r.Playlists} {
		out := []string{}
		seen := map[string]bool{}
		for _, v := range *l {
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
		*l = out
	}
	if r.YearFrom < 0 || r.YearFrom > 3000 {
		r.YearFrom = 0
	}
	if r.YearTo < 0 || r.YearTo > 3000 {
		r.YearTo = 0
	}
	if r.YearFrom > 0 && r.YearTo > 0 && r.YearFrom > r.YearTo {
		r.YearFrom, r.YearTo = r.YearTo, r.YearFrom
	}
	if r.AvoidHours < 0 {
		r.AvoidHours = 0
	}
	if r.AvoidHours > 24*30 {
		r.AvoidHours = 24 * 30
	}
	if r.ArtistGap < 0 {
		r.ArtistGap = 0
	}
	if r.ArtistGap > 20 {
		r.ArtistGap = 20
	}
}

// ListChannels lists one person's channels.
func (s *Store) ListChannels(ctx context.Context, uid int64) ([]Channel, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+channelCols+` FROM channels WHERE user_id = ? ORDER BY name COLLATE NOCASE`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetChannel returns one of the person's channels.
func (s *Store) GetChannel(ctx context.Context, uid, id int64) (*Channel, error) {
	c, err := scanChannel(s.DB.QueryRowContext(ctx, `SELECT `+channelCols+` FROM channels WHERE user_id = ? AND id = ?`, uid, id))
	return c, notFound(err)
}

// ChannelByToken finds a channel by its listen address (any person: the
// token is the secret that stands in for signing in).
func (s *Store) ChannelByToken(ctx context.Context, token string) (*Channel, error) {
	if len(token) < 16 {
		return nil, ErrNotFound
	}
	c, err := scanChannel(s.DB.QueryRowContext(ctx, `SELECT `+channelCols+` FROM channels WHERE listen_token = ?`, token))
	return c, notFound(err)
}

// CreateChannel adds a channel with a fresh listen token.
func (s *Store) CreateChannel(ctx context.Context, uid int64, c *Channel) error {
	c.Rules.Clean()
	rules, _ := json.Marshal(c.Rules)
	now := Now()
	c.ListenToken = RandomToken(18)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO channels (user_id, name, description, rules, color, bitrate, listen_token, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, c.Name, c.Description, string(rules), c.Color, c.Bitrate, c.ListenToken, now, now)
	if err != nil {
		return conflict(err)
	}
	c.ID, _ = res.LastInsertId()
	c.UserID, c.CreatedAt, c.UpdatedAt = uid, now, now
	return nil
}

// UpdateChannel saves a channel's name, rules and sound.
func (s *Store) UpdateChannel(ctx context.Context, uid int64, c *Channel) error {
	c.Rules.Clean()
	rules, _ := json.Marshal(c.Rules)
	c.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, `UPDATE channels SET name = ?, description = ?, rules = ?, color = ?, bitrate = ?, updated_at = ?
		WHERE user_id = ? AND id = ?`, c.Name, c.Description, string(rules), c.Color, c.Bitrate, c.UpdatedAt, uid, c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// NewChannelToken replaces the listen address (the old one stops working).
func (s *Store) NewChannelToken(ctx context.Context, uid, id int64) (string, error) {
	tok := RandomToken(18)
	res, err := s.DB.ExecContext(ctx, `UPDATE channels SET listen_token = ? WHERE user_id = ? AND id = ?`, tok, uid, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return tok, nil
}

// DeleteChannel removes a channel and what it played.
func (s *Store) DeleteChannel(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM channels WHERE user_id = ? AND id = ?`, uid, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ChannelPlayed notes a song the channel played.
func (s *Store) ChannelPlayed(ctx context.Context, uid, channelID int64, songID, artist string) error {
	now := Now()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO channel_plays (user_id, channel_id, song_id, artist, played_at) VALUES (?, ?, ?, ?, ?)`,
		uid, channelID, songID, artist, now); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE channels SET last_played_at = ? WHERE user_id = ? AND id = ?`, now, uid, channelID)
	return err
}

// ChannelPlay is a song a channel played.
type ChannelPlay struct {
	SongID   string
	Artist   string
	PlayedAt string
}

// RecentChannelPlays lists what the channel played since, latest first.
func (s *Store) RecentChannelPlays(ctx context.Context, uid, channelID int64, since string, limit int) ([]ChannelPlay, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT song_id, artist, played_at FROM channel_plays WHERE user_id = ? AND channel_id = ? AND played_at >= ?
		ORDER BY played_at DESC LIMIT ?`, uid, channelID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelPlay
	for rows.Next() {
		var p ChannelPlay
		if err := rows.Scan(&p.SongID, &p.Artist, &p.PlayedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PruneChannelPlays forgets plays older than before.
func (s *Store) PruneChannelPlays(ctx context.Context, before string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM channel_plays WHERE played_at < ?`, before)
	return err
}
