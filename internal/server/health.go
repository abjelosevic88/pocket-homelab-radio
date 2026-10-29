package server

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// ---- are the favourites still on air? ----
//
// Stations move and close. Once a day every favourite is opened for a
// moment (a few kilobytes of sound are enough); three failed days in a row
// mark it "not working" and, if the person wants, send them a message.
// One good check brings it back.

const failingAfter = 3

// checkStation opens a station briefly: ok, or why not.
func (s *Server) checkStation(ctx context.Context, uid int64, st *store.Station) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if st.HLS {
		res, err := s.outGet(ctx, s.outClient(15*time.Second), st.StreamURL(), nil)
		if err != nil {
			return false, err.Error()
		}
		defer res.Body.Close()
		if res.StatusCode >= 400 {
			return false, fmt.Sprintf("HTTP %d", res.StatusCode)
		}
		return true, ""
	}
	res, err := s.openStation(ctx, uid, st)
	if err != nil {
		return false, err.Error()
	}
	defer res.Body.Close()
	n, err := io.CopyN(io.Discard, res.Body, 4096)
	if n < 1024 {
		if err != nil {
			return false, "no sound came: " + err.Error()
		}
		return false, "no sound came"
	}
	return true, ""
}

// checkFavourites checks a few favourites that were not checked today.
func (s *Server) checkFavourites(ctx context.Context) {
	if s.cfg.DemoData {
		return
	}
	list, err := s.store.StationsToCheck(ctx, time.Now().Add(-22*time.Hour).UTC().Format(time.RFC3339Nano), 12)
	if err != nil {
		return
	}
	for i := range list {
		st := &list[i]
		if ctx.Err() != nil {
			return
		}
		was := st.Health
		ok, note := s.checkStation(ctx, st.UserID, st)
		now, err := s.store.SetStationHealth(ctx, st.UserID, st.ID, ok, note, failingAfter)
		if err != nil || now.Health != "failing" || was == "failing" {
			continue
		}
		prefs, _ := s.store.GetSettings(ctx, st.UserID)
		if prefs.NotifyBroken && prefs.NotifyURL != "" {
			n := notify.New(prefs.NotifyURL, prefs.NotifyFormat)
			_ = n.Send(ctx, notify.Message{Title: "A station stopped working", Body: fmt.Sprintf("%s has not played for %d days (%s). Find it again or remove it in Pocket Radio.",
				now.Name, failingAfter, note), Priority: "warning", Tags: []string{"radio"}, Link: s.cfg.BaseURL + "/stations"})
		}
	}
}
