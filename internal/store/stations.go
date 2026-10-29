package store

import (
	"context"
	"database/sql"
	"strings"
)

const stationCols = `id, user_id, name, url, url_resolved, homepage, logo_url, tags, country_code, language, codec, bitrate, hls,
	directory_id, favorite, position, notes, source, play_count, last_played_at, health, fail_count, checked_at, health_note,
	created_at, updated_at`

func scanStation(sc interface{ Scan(...any) error }) (*Station, error) {
	var st Station
	var hls, fav int
	var played, checked sql.NullString
	if err := sc.Scan(&st.ID, &st.UserID, &st.Name, &st.URL, &st.URLResolved, &st.Homepage, &st.LogoURL, &st.Tags, &st.CountryCode,
		&st.Language, &st.Codec, &st.Bitrate, &hls, &st.DirectoryID, &fav, &st.Position, &st.Notes, &st.Source, &st.PlayCount,
		&played, &st.Health, &st.FailCount, &checked, &st.HealthNote, &st.CreatedAt, &st.UpdatedAt); err != nil {
		return nil, err
	}
	st.HLS, st.Favorite = hls == 1, fav == 1
	if played.Valid {
		st.LastPlayedAt = &played.String
	}
	if checked.Valid {
		st.CheckedAt = &checked.String
	}
	return &st, nil
}

func (s *Store) queryStations(ctx context.Context, q string, args ...any) ([]Station, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Station{}
	for rows.Next() {
		st, err := scanStation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

// StationFilter narrows a station list.
type StationFilter struct {
	Favorites bool   // only favourites
	Recent    bool   // only ones played, latest first
	Query     string // name or tag contains
	Limit     int
}

// ListStations lists one person's stations: favourites in their order, or
// the recently played ones.
func (s *Store) ListStations(ctx context.Context, uid int64, f StationFilter) ([]Station, error) {
	q := `SELECT ` + stationCols + ` FROM stations WHERE user_id = ?`
	args := []any{uid}
	if f.Favorites {
		q += ` AND favorite = 1`
	}
	if f.Recent {
		q += ` AND last_played_at IS NOT NULL`
	}
	if t := strings.TrimSpace(f.Query); t != "" {
		q += ` AND (name LIKE '%' || ? || '%' OR tags LIKE '%' || ? || '%')`
		args = append(args, t, t)
	}
	if f.Recent {
		q += ` ORDER BY last_played_at DESC`
	} else {
		q += ` ORDER BY favorite DESC, position, name COLLATE NOCASE`
	}
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	return s.queryStations(ctx, q, args...)
}

// GetStation returns one of the person's stations.
func (s *Store) GetStation(ctx context.Context, uid, id int64) (*Station, error) {
	st, err := scanStation(s.DB.QueryRowContext(ctx, `SELECT `+stationCols+` FROM stations WHERE user_id = ? AND id = ?`, uid, id))
	return st, notFound(err)
}

// StationByURL finds the person's station with this address.
func (s *Store) StationByURL(ctx context.Context, uid int64, url string) (*Station, error) {
	st, err := scanStation(s.DB.QueryRowContext(ctx, `SELECT `+stationCols+` FROM stations WHERE user_id = ? AND url = ?`, uid, url))
	return st, notFound(err)
}

// StationByDirectoryID finds the person's station from the directory entry.
func (s *Store) StationByDirectoryID(ctx context.Context, uid int64, dirID string) (*Station, error) {
	if dirID == "" {
		return nil, ErrNotFound
	}
	st, err := scanStation(s.DB.QueryRowContext(ctx, `SELECT `+stationCols+` FROM stations WHERE user_id = ? AND directory_id = ? LIMIT 1`, uid, dirID))
	return st, notFound(err)
}

// CreateStation adds a station at the end of the favourites.
func (s *Store) CreateStation(ctx context.Context, uid int64, st *Station) error {
	now := Now()
	if st.Health == "" {
		st.Health = "unknown"
	}
	if st.Source == "" {
		st.Source = "manual"
	}
	if st.Position == 0 {
		_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM stations WHERE user_id = ?`, uid).Scan(&st.Position)
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO stations (user_id, name, url, url_resolved, homepage, logo_url, tags, country_code, language,
		codec, bitrate, hls, directory_id, favorite, position, notes, source, play_count, last_played_at, health, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, st.Name, st.URL, st.URLResolved, st.Homepage, st.LogoURL, st.Tags, st.CountryCode, st.Language, st.Codec, st.Bitrate,
		boolInt(st.HLS), st.DirectoryID, boolInt(st.Favorite), st.Position, st.Notes, st.Source, st.PlayCount, nullS(st.LastPlayedAt),
		st.Health, now, now)
	if err != nil {
		return conflict(err)
	}
	st.ID, _ = res.LastInsertId()
	st.UserID, st.CreatedAt, st.UpdatedAt = uid, now, now
	return nil
}

// UpdateStation saves the editable fields of a station.
func (s *Store) UpdateStation(ctx context.Context, uid int64, st *Station) error {
	st.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, `UPDATE stations SET name = ?, url = ?, url_resolved = ?, homepage = ?, logo_url = ?, tags = ?,
		country_code = ?, language = ?, codec = ?, bitrate = ?, hls = ?, directory_id = ?, favorite = ?, position = ?, notes = ?, updated_at = ?
		WHERE user_id = ? AND id = ?`,
		st.Name, st.URL, st.URLResolved, st.Homepage, st.LogoURL, st.Tags, st.CountryCode, st.Language, st.Codec, st.Bitrate, boolInt(st.HLS),
		st.DirectoryID, boolInt(st.Favorite), st.Position, st.Notes, st.UpdatedAt, uid, st.ID)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteStation removes a station (its listens and heard songs stay, unlinked).
func (s *Store) DeleteStation(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM stations WHERE user_id = ? AND id = ?`, uid, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderStations puts the favourites in this order.
func (s *Store) ReorderStations(ctx context.Context, uid int64, ids []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE stations SET position = ? WHERE user_id = ? AND id = ?`, i+1, uid, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// MarkStationPlayed counts a play and moves it to the top of "recent".
func (s *Store) MarkStationPlayed(ctx context.Context, uid, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE stations SET play_count = play_count + 1, last_played_at = ? WHERE user_id = ? AND id = ?`, Now(), uid, id)
	return err
}

// SetStationResolved stores the real stream address and what it carries.
func (s *Store) SetStationResolved(ctx context.Context, uid, id int64, resolved, codec string, bitrate int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE stations SET url_resolved = ?, codec = CASE WHEN ? <> '' THEN ? ELSE codec END,
		bitrate = CASE WHEN ? > 0 THEN ? ELSE bitrate END WHERE user_id = ? AND id = ?`, resolved, codec, codec, bitrate, bitrate, uid, id)
	return err
}

// SetStationHealth records a check: ok resets the failures, a failure adds one.
func (s *Store) SetStationHealth(ctx context.Context, uid, id int64, ok bool, note string, failingAfter int) (*Station, error) {
	if ok {
		_, err := s.DB.ExecContext(ctx, `UPDATE stations SET health = 'ok', fail_count = 0, checked_at = ?, health_note = '' WHERE user_id = ? AND id = ?`, Now(), uid, id)
		if err != nil {
			return nil, err
		}
	} else {
		_, err := s.DB.ExecContext(ctx, `UPDATE stations SET fail_count = fail_count + 1, checked_at = ?, health_note = ?,
			health = CASE WHEN fail_count + 1 >= ? THEN 'failing' ELSE health END WHERE user_id = ? AND id = ?`, Now(), note, failingAfter, uid, id)
		if err != nil {
			return nil, err
		}
	}
	return s.GetStation(ctx, uid, id)
}

// StationsToCheck lists every person's favourites not checked since before.
func (s *Store) StationsToCheck(ctx context.Context, before string, limit int) ([]Station, error) {
	return s.queryStations(ctx, `SELECT `+stationCols+` FROM stations WHERE favorite = 1 AND (checked_at IS NULL OR checked_at < ?)
		ORDER BY checked_at IS NOT NULL, checked_at LIMIT ?`, before, limit)
}

// PruneStations forgets stations played once and never kept, after a while.
func (s *Store) PruneStations(ctx context.Context, before string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM stations WHERE favorite = 0 AND (last_played_at IS NULL OR last_played_at < ?)`, before)
	return err
}

func nullS(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}
