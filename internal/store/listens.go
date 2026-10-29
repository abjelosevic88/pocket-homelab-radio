package store

import (
	"context"
	"database/sql"
	"strings"
)

// ---- listening ----

// AddListen records a stretch of listening.
func (s *Store) AddListen(ctx context.Context, uid int64, l *Listen) error {
	if l.Client == "" {
		l.Client = "web"
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO listens (user_id, station_id, channel_id, name, client, started_at, ended_at, seconds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, uid, nullI(l.StationID), nullI(l.ChannelID), l.Name, l.Client, l.StartedAt, l.EndedAt, l.Seconds)
	if err != nil {
		return err
	}
	l.ID, _ = res.LastInsertId()
	l.UserID = uid
	return nil
}

// ExtendListen moves the end of a listen that is still going.
func (s *Store) ExtendListen(ctx context.Context, uid, id int64, ended string, seconds int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE listens SET ended_at = ?, seconds = ? WHERE user_id = ? AND id = ?`, ended, seconds, uid, id)
	return err
}

// ListListens lists listens that started in [from, to) (RFC 3339 times), latest first.
func (s *Store) ListListens(ctx context.Context, uid int64, from, to string, limit int) ([]Listen, error) {
	q := `SELECT id, user_id, station_id, channel_id, name, client, started_at, ended_at, seconds FROM listens WHERE user_id = ?`
	args := []any{uid}
	if from != "" {
		q += ` AND started_at >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND started_at < ?`
		args = append(args, to)
	}
	q += ` ORDER BY started_at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Listen{}
	for rows.Next() {
		var l Listen
		var st, ch sql.NullInt64
		if err := rows.Scan(&l.ID, &l.UserID, &st, &ch, &l.Name, &l.Client, &l.StartedAt, &l.EndedAt, &l.Seconds); err != nil {
			return nil, err
		}
		if st.Valid {
			l.StationID = &st.Int64
		}
		if ch.Valid {
			l.ChannelID = &ch.Int64
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteListen removes one listen.
func (s *Store) DeleteListen(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM listens WHERE user_id = ? AND id = ?`, uid, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- heard songs ----

// AddHeard records a song unless the same source sent the same title in the
// last 20 minutes (stations repeat their title on every reconnect). It
// returns the row and whether it is new.
func (s *Store) AddHeard(ctx context.Context, uid int64, h *Heard) (bool, error) {
	if h.HeardAt == "" {
		h.HeardAt = Now()
	}
	var id int64
	var since string
	_ = s.DB.QueryRowContext(ctx, `SELECT strftime('%Y-%m-%dT%H:%M:%fZ', ?, '-20 minutes')`, h.HeardAt).Scan(&since)
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM heard WHERE user_id = ? AND source = ? AND title = ? AND heard_at >= ? ORDER BY heard_at DESC LIMIT 1`,
		uid, h.Source, h.Title, since).Scan(&id)
	if err == nil {
		h.ID = id
		return false, nil
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO heard (user_id, station_id, channel_id, source, title, artist, song, song_id, saved, heard_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, nullI(h.StationID), nullI(h.ChannelID), h.Source, h.Title, h.Artist, h.Song, h.SongID,
		boolInt(h.Saved), h.HeardAt)
	if err != nil {
		return false, err
	}
	h.ID, _ = res.LastInsertId()
	h.UserID = uid
	return true, nil
}

// HeardFilter narrows the heard list.
type HeardFilter struct {
	Saved     bool
	Query     string
	StationID int64
	ChannelID int64
	Before    string // heard_at < this (paging)
	From, To  string // heard_at in [From, To)
	Limit     int
}

func scanHeard(sc interface{ Scan(...any) error }) (*Heard, error) {
	var h Heard
	var st, ch sql.NullInt64
	var saved int
	if err := sc.Scan(&h.ID, &h.UserID, &st, &ch, &h.Source, &h.Title, &h.Artist, &h.Song, &h.SongID, &saved, &h.HeardAt); err != nil {
		return nil, err
	}
	if st.Valid {
		h.StationID = &st.Int64
	}
	if ch.Valid {
		h.ChannelID = &ch.Int64
	}
	h.Saved = saved == 1
	return &h, nil
}

const heardCols = `id, user_id, station_id, channel_id, source, title, artist, song, song_id, saved, heard_at`

// ListHeard lists heard songs, latest first.
func (s *Store) ListHeard(ctx context.Context, uid int64, f HeardFilter) ([]Heard, error) {
	q := `SELECT ` + heardCols + ` FROM heard WHERE user_id = ?`
	args := []any{uid}
	if f.Saved {
		q += ` AND saved = 1`
	}
	if t := strings.TrimSpace(f.Query); t != "" {
		q += ` AND (title LIKE '%' || ? || '%' OR source LIKE '%' || ? || '%')`
		args = append(args, t, t)
	}
	if f.StationID > 0 {
		q += ` AND station_id = ?`
		args = append(args, f.StationID)
	}
	if f.ChannelID > 0 {
		q += ` AND channel_id = ?`
		args = append(args, f.ChannelID)
	}
	if f.Before != "" {
		q += ` AND heard_at < ?`
		args = append(args, f.Before)
	}
	if f.From != "" {
		q += ` AND heard_at >= ?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND heard_at < ?`
		args = append(args, f.To)
	}
	q += ` ORDER BY heard_at DESC`
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	q += ` LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Heard{}
	for rows.Next() {
		h, err := scanHeard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *h)
	}
	return out, rows.Err()
}

// GetHeard returns one heard song.
func (s *Store) GetHeard(ctx context.Context, uid, id int64) (*Heard, error) {
	h, err := scanHeard(s.DB.QueryRowContext(ctx, `SELECT `+heardCols+` FROM heard WHERE user_id = ? AND id = ?`, uid, id))
	return h, notFound(err)
}

// SetHeardSaved keeps or un-keeps a heard song.
func (s *Store) SetHeardSaved(ctx context.Context, uid, id int64, saved bool) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE heard SET saved = ? WHERE user_id = ? AND id = ?`, boolInt(saved), uid, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteHeard removes a heard song.
func (s *Store) DeleteHeard(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM heard WHERE user_id = ? AND id = ?`, uid, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneHeard forgets a person's unsaved heard songs older than before.
func (s *Store) PruneHeard(ctx context.Context, uid int64, before string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM heard WHERE user_id = ? AND saved = 0 AND heard_at < ?`, uid, before)
	return err
}
