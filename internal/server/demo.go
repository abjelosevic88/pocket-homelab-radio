package server

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-radio/internal/store"
)

// demoStations are real, long-running public stations.
var demoStations = []store.Station{
	{Name: "Radio Paradise", URL: "https://stream.radioparadise.com/mp3-192", Homepage: "https://radioparadise.com", Tags: "eclectic, rock, indie", CountryCode: "US", Language: "english", Codec: "MP3", Bitrate: 192},
	{Name: "SomaFM Groove Salad", URL: "https://ice2.somafm.com/groovesalad-128-mp3", Homepage: "https://somafm.com/groovesalad/", Tags: "ambient, downtempo, chillout", CountryCode: "US", Language: "english", Codec: "MP3", Bitrate: 128},
	{Name: "FIP", URL: "https://icecast.radiofrance.fr/fip-midfi.mp3", Homepage: "https://www.radiofrance.fr/fip", Tags: "eclectic, jazz, world", CountryCode: "FR", Language: "french", Codec: "MP3", Bitrate: 128},
	{Name: "KEXP 90.3 Seattle", URL: "https://kexp-mp3-128.streamguys1.com/kexp128.mp3", Homepage: "https://www.kexp.org", Tags: "indie, alternative", CountryCode: "US", Language: "english", Codec: "MP3", Bitrate: 128},
	{Name: "Radio Swiss Jazz", URL: "https://stream.srg-ssr.ch/m/rsj/mp3_128", Homepage: "https://www.radioswissjazz.ch", Tags: "jazz, soul", CountryCode: "CH", Language: "german", Codec: "MP3", Bitrate: 128},
	{Name: "SomaFM Secret Agent", URL: "https://ice2.somafm.com/secretagent-128-mp3", Homepage: "https://somafm.com/secretagent/", Tags: "lounge, soundtrack", CountryCode: "US", Language: "english", Codec: "MP3", Bitrate: 128},
	{Name: "Jazz24", URL: "https://live.wostreaming.net/direct/ppm-jazz24mp3-ibc1", Homepage: "https://www.jazz24.org", Tags: "jazz", CountryCode: "US", Language: "english", Codec: "MP3", Bitrate: 128},
	{Name: "BBC Radio 6 Music", URL: "https://example.org/bbc6music.m3u8", Homepage: "https://www.bbc.co.uk/6music", Tags: "alternative, indie", CountryCode: "GB", Language: "english", Codec: "HLS", HLS: true},
	{Name: "Radio Swiss Classic", URL: "https://stream.srg-ssr.ch/m/rsc_de/mp3_128", Homepage: "https://www.radioswissclassic.ch", Tags: "classical", CountryCode: "CH", Language: "german", Codec: "MP3", Bitrate: 128},
	{Name: "NTS Radio 1", URL: "https://stream-relay-geo.ntslive.net/stream", Homepage: "https://www.nts.live", Tags: "eclectic, electronic", CountryCode: "GB", Language: "english", Codec: "MP3", Bitrate: 256},
}

var demoRecentOnly = []store.Station{
	{Name: "SomaFM Drone Zone", URL: "https://ice2.somafm.com/dronezone-128-mp3", Tags: "ambient, drone", CountryCode: "US", Codec: "MP3", Bitrate: 128},
	{Name: "Radio Swiss Pop", URL: "https://stream.srg-ssr.ch/m/rsp/mp3_128", Tags: "pop", CountryCode: "CH", Codec: "MP3", Bitrate: 128},
}

var demoChannels = []store.Channel{
	{Name: "Sunday Morning", Description: "Slow jazz and soul for a late breakfast.", Color: "#c2410c",
		Rules: store.ChannelRules{Genres: []string{"Jazz", "Soul", "Bossa Nova"}, AvoidHours: 24, ArtistGap: 3}},
	{Name: "Seventies Rock", Description: "Guitars from 1968 to 1979.", Color: "#7c3aed",
		Rules: store.ChannelRules{Genres: []string{"Rock", "Hard Rock", "Blues Rock"}, YearFrom: 1968, YearTo: 1979, AvoidHours: 48, ArtistGap: 4}},
	{Name: "Focus", Description: "Ambient and instrumental music, nothing with words.", Color: "#0f766e",
		Rules: store.ChannelRules{Genres: []string{"Ambient", "Electronic", "Classical"}, ExcludeGenres: []string{"Vocal"}, AvoidHours: 12, ArtistGap: 2}},
}

// demoSongs are what each kind of station plays.
var demoSongs = map[string][]string{
	"eclectic":  {"Talking Heads - This Must Be the Place", "Khruangbin - Maria También", "Portishead - Roads", "Nick Drake - Pink Moon", "Air - La Femme d'Argent", "Fleetwood Mac - Dreams", "Massive Attack - Teardrop", "Beck - Lost Cause"},
	"ambient":   {"Boards of Canada - Dayvan Cowboy", "Tycho - Awake", "Bonobo - Kerala", "Thievery Corporation - Lebanese Blonde", "Nightmares on Wax - You Wish", "Zero 7 - In the Waiting Line"},
	"jazz":      {"Miles Davis - So What", "Bill Evans - Waltz for Debby", "Nina Simone - Feeling Good", "Chet Baker - Almost Blue", "John Coltrane - Naima", "Ella Fitzgerald - Summertime", "Stan Getz & João Gilberto - Corcovado"},
	"indie":     {"Arcade Fire - Wake Up", "The National - Bloodbuzz Ohio", "Wilco - Jesus, Etc.", "Big Thief - Not", "Fontaines D.C. - Boys in the Better Land", "Alvvays - Archie, Marry Me"},
	"lounge":    {"Henry Mancini - Lujon", "Nicola Conte - Bossa Per Due", "Dzihan & Kamien - Homebass", "Tosca - Suzuki"},
	"classical": {"Erik Satie - Gymnopédie No. 1", "J. S. Bach - Air on the G String", "Arvo Pärt - Spiegel im Spiegel", "Claude Debussy - Clair de Lune", "Max Richter - On the Nature of Daylight"},
	"rock":      {"Led Zeppelin - Ramble On", "Deep Purple - Highway Star", "The Rolling Stones - Gimme Shelter", "Pink Floyd - Wish You Were Here", "Thin Lizzy - The Boys Are Back in Town", "Free - All Right Now"},
}

func demoKind(tags string) string {
	for _, k := range []string{"ambient", "jazz", "indie", "lounge", "classical", "rock", "eclectic"} {
		if strings.Contains(tags, k) {
			return k
		}
	}
	return "eclectic"
}

// seedDemo fills the owner's account with stations, channels and four
// months of listening (DEMO_DATA=true, for screenshots and trying the app).
// Runs once.
func (s *Server) seedDemo(ctx context.Context, owner int64) error {
	key := fmt.Sprintf("seeded:%d:demo", owner)
	if _, _, done := s.store.CacheGet(ctx, key); done {
		return nil
	}
	if have, _ := s.store.ListStations(ctx, owner, store.StationFilter{}); len(have) > 0 {
		_ = s.store.CachePut(ctx, key, "1")
		return nil
	}
	st, _ := s.store.GetSettings(ctx, owner)
	if st.Country == "" {
		st.Country = "BA"
		_ = s.store.SaveUserSettings(ctx, owner, st)
	}
	rng := rand.New(rand.NewSource(42))
	type src struct {
		name, kind string
		station    *int64
		channel    *int64
		weight     int
	}
	var sources []src
	for i, x := range demoStations {
		x.Favorite, x.Source, x.Position, x.Health = true, "directory", i+1, "ok"
		if err := s.store.CreateStation(ctx, owner, &x); err != nil {
			return err
		}
		if x.Name == "NTS Radio 1" {
			_, _ = s.store.SetStationHealth(ctx, owner, x.ID, false, "the station answered HTTP 404", 1)
		}
		id := x.ID
		sources = append(sources, src{x.Name, demoKind(x.Tags), &id, nil, 10 - i})
	}
	for _, x := range demoRecentOnly {
		x.Favorite, x.Source = false, "directory"
		if err := s.store.CreateStation(ctx, owner, &x); err != nil {
			return err
		}
		id := x.ID
		sources = append(sources, src{x.Name, demoKind(x.Tags), &id, nil, 1})
	}
	for _, c := range demoChannels {
		c.Bitrate = 192
		if err := s.store.CreateChannel(ctx, owner, &c); err != nil {
			return err
		}
		id := c.ID
		kind := "jazz"
		switch c.Name {
		case "Seventies Rock":
			kind = "rock"
		case "Focus":
			kind = "ambient"
		}
		sources = append(sources, src{c.Name, kind, nil, &id, 4})
	}
	total := 0
	for _, x := range sources {
		total += x.weight
	}
	pick := func() src {
		n := rng.Intn(total)
		for _, x := range sources {
			if n -= x.weight; n < 0 {
				return x
			}
		}
		return sources[0]
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	lastPlayed := map[int64]string{}
	plays := map[int64]int{}
	type slot struct{ hour, minute, minLen, maxLen int }
	for d := 120; d >= 0; d-- {
		day := today.AddDate(0, 0, -d)
		weekend := day.Weekday() == time.Saturday || day.Weekday() == time.Sunday
		var slots []slot
		if !weekend && rng.Float64() < 0.75 {
			slots = append(slots, slot{7, 20 + rng.Intn(25), 18, 45}) // the way to work
		}
		if !weekend && rng.Float64() < 0.45 {
			slots = append(slots, slot{10 + rng.Intn(5), rng.Intn(60), 40, 150}) // working
		}
		if weekend && rng.Float64() < 0.7 {
			slots = append(slots, slot{9 + rng.Intn(3), rng.Intn(60), 50, 160}) // a slow morning
		}
		if rng.Float64() < 0.4 {
			slots = append(slots, slot{19 + rng.Intn(3), rng.Intn(60), 20, 90}) // evening
		}
		for _, sl := range slots {
			start := day.Add(time.Duration(sl.hour)*time.Hour + time.Duration(sl.minute)*time.Minute)
			length := time.Duration(sl.minLen+rng.Intn(sl.maxLen-sl.minLen+1)) * time.Minute
			if start.Add(length).After(now) {
				continue
			}
			x := pick()
			l := store.Listen{StationID: x.station, ChannelID: x.channel, Name: x.name, Client: "web",
				StartedAt: start.UTC().Format(time.RFC3339Nano), EndedAt: start.Add(length).UTC().Format(time.RFC3339Nano), Seconds: int(length.Seconds())}
			if rng.Float64() < 0.2 {
				l.Client = "app"
			}
			if err := s.store.AddListen(ctx, owner, &l); err != nil {
				return err
			}
			if x.station != nil {
				lastPlayed[*x.station] = l.EndedAt
				plays[*x.station]++
			}
			songs := demoSongs[x.kind]
			for t := start.Add(time.Duration(rng.Intn(4)) * time.Minute); t.Before(start.Add(length)); t = t.Add(time.Duration(3+rng.Intn(3)) * time.Minute) {
				if rng.Float64() < 0.55 {
					continue
				}
				title := songs[rng.Intn(len(songs))]
				artist, song := splitTitle(title)
				h := store.Heard{StationID: x.station, ChannelID: x.channel, Source: x.name, Title: title, Artist: artist, Song: song,
					Saved: rng.Float64() < 0.06, HeardAt: t.UTC().Format(time.RFC3339Nano)}
				if _, err := s.store.AddHeard(ctx, owner, &h); err != nil {
					return err
				}
			}
		}
	}
	for id, at := range lastPlayed {
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE stations SET last_played_at = ?, play_count = ? WHERE user_id = ? AND id = ?`, at, plays[id], owner, id)
	}
	return s.store.CachePut(ctx, key, "1")
}
