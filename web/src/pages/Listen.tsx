// Listen: what plays now, your favourites and channels one tap away, what you
// played lately and the songs you just heard. Everything comes from the
// dashboard the shell already loads (and reloads after every change).
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { ago, fmtDuration } from '../lib/format'
import { usePlayer } from '../lib/player'
import type { Channel, Station } from '../lib/types'
import { Icon } from '../components/icons'
import { ChannelLogo, StationLogo } from '../components/StationLogo'
import { announceChange, openCreate, useCounts, useCrumb } from '../components/Layout'
import { Loading, errMsg, useToast } from '../components/ui'

function minutes(secs: number) {
  const m = Math.round(secs / 60)
  return m ? fmtDuration(m) : 'nothing yet'
}

export default function ListenPage() {
  useCrumb(undefined)
  const d = useCounts()
  const p = usePlayer()
  const { toast } = useToast()

  if (!d) return <Loading />

  const empty = d.favorites.length === 0 && d.channels.length === 0 && d.recent.length === 0
  const playStation = (s: Station) => (p.isCurrent('station', s.id) ? p.stop() : p.play({ kind: 'station', station: s }))
  const playChannel = (c: Channel) => (p.isCurrent('channel', c.id) ? p.stop() : p.play({ kind: 'channel', channel: c }))
  const keep = async (s: Station) => {
    try {
      await api.post<Station>(`${V1}/stations`, { ...s, favorite: true })
      toast(`${s.name} is in your stations`, 'ok')
      announceChange()
    } catch (e) { toast(errMsg(e), 'err') }
  }

  return (
    <div className="ls">
      <NowHero />

      {empty && (
        <section className="card ls-start">
          <div className="ls-start-ico"><Icon name="radio" size={26} /></div>
          <h2>Start listening</h2>
          <p className="muted">Find stations from around the world, add one you know by its address, or bring your list from another radio app.</p>
          <div className="row wrap ls-start-actions">
            <button type="button" className="btn primary" onClick={() => openCreate('find')}><Icon name="search" size={15} />Find a station</button>
            <button type="button" className="btn" onClick={() => openCreate('station')}><Icon name="link" size={15} />Add by address</button>
            <button type="button" className="btn" onClick={() => openCreate('import')}><Icon name="upload" size={15} />Import a list</button>
          </div>
        </section>
      )}

      {!empty && (
        <div className="ls-stats">
          <span><Icon name="headphones" size={15} />Today: <b>{minutes(d.today_seconds)}</b></span>
          <span><Icon name="chart" size={15} />This week: <b>{minutes(d.week_seconds)}</b></span>
          {d.week_top[0] && <span className="hide-phone"><Icon name="star" size={15} />Most this week: <b>{d.week_top[0].name}</b></span>}
        </div>
      )}

      {d.failing > 0 && (
        <Link to="/stations" className="ls-warn">
          <Icon name="alert" size={15} />
          <span className="grow">{d.failing === 1 ? 'One of your stations has not played for a few days.' : `${d.failing} of your stations have not played for a few days.`}</span>
          <span className="ls-warn-more">See which</span>
        </Link>
      )}

      {d.favorites.length > 0 && (
        <section>
          <div className="ls-head"><h3>Your stations</h3><Link to="/stations" className="muted small">All {d.favorites.length}</Link></div>
          <div className="ls-tiles">
            {d.favorites.slice(0, 12).map(s => {
              const on = p.isCurrent('station', s.id)
              return (
                <button key={s.id} type="button" className={`ls-tile ${on ? 'on' : ''}`} onClick={() => playStation(s)} title={on ? `Stop ${s.name}` : `Play ${s.name}`}>
                  <StationLogo station={s} size={48} />
                  <span className="ls-tile-text">
                    <span className="ls-tile-name ellipsis">{s.name}</span>
                    <span className="ls-tile-sub ellipsis">{on ? 'Playing' : s.health === 'failing' ? 'Not working lately' : s.tags || s.codec || 'Radio'}</span>
                  </span>
                  <span className="ls-tile-play" aria-hidden><Icon name={on ? 'stop' : 'play'} size={15} strokeWidth={2} /></span>
                </button>
              )
            })}
          </div>
        </section>
      )}

      {d.channels.length > 0 && (
        <section>
          <div className="ls-head"><h3>Your channels</h3><Link to="/channels" className="muted small">All channels</Link></div>
          <div className="ls-tiles">
            {d.channels.map(c => {
              const on = p.isCurrent('channel', c.id)
              return (
                <button key={c.id} type="button" className={`ls-tile ${on ? 'on' : ''}`} onClick={() => playChannel(c)} title={on ? `Stop ${c.name}` : `Play ${c.name}`}>
                  <ChannelLogo channel={c} size={48} />
                  <span className="ls-tile-text">
                    <span className="ls-tile-name ellipsis">{c.name}</span>
                    <span className="ls-tile-sub ellipsis">{on ? 'Playing' : c.description || c.rules.genres.slice(0, 3).join(', ') || 'From your music'}</span>
                  </span>
                  <span className="ls-tile-play" aria-hidden><Icon name={on ? 'stop' : 'play'} size={15} strokeWidth={2} /></span>
                </button>
              )
            })}
          </div>
        </section>
      )}

      {(d.recent.length > 0 || d.heard.length > 0) && (
        <div className="ls-two">
          {d.recent.length > 0 && (
            <section className="card">
              <div className="card-head"><div className="grow"><h2>Played lately</h2></div></div>
              <div className="ls-list">
                {d.recent.map(s => {
                  const on = p.isCurrent('station', s.id)
                  return (
                    <div key={s.id} className="ls-row">
                      <StationLogo station={s} size={34} />
                      <div className="grow">
                        <div className="ellipsis ls-row-title">{s.name}</div>
                        <div className="ellipsis muted small">{s.last_played_at ? ago(s.last_played_at) : ''}{s.favorite ? '' : ' · not kept'}</div>
                      </div>
                      {!s.favorite && <button type="button" className="btn ghost icon-only sm" onClick={() => keep(s)} aria-label={`Keep ${s.name}`} title="Keep it in your stations"><Icon name="heart" size={15} /></button>}
                      <button type="button" className={`btn icon-only sm ${on ? 'primary' : ''}`} onClick={() => playStation(s)} aria-label={on ? 'Stop' : 'Play'} title={on ? 'Stop' : 'Play'}>
                        <Icon name={on ? 'stop' : 'play'} size={14} strokeWidth={2} />
                      </button>
                    </div>
                  )
                })}
              </div>
            </section>
          )}
          {d.heard.length > 0 && (
            <section className="card">
              <div className="card-head"><div className="grow"><h2>Heard just now</h2></div><Link to="/heard" className="muted small">All songs</Link></div>
              <div className="ls-list">
                {d.heard.map(h => (
                  <div key={h.id} className="ls-row">
                    <span className={`ls-note ${h.saved ? 'saved' : ''}`} aria-hidden><Icon name={h.saved ? 'heart' : 'music'} size={15} /></span>
                    <div className="grow">
                      <div className="ellipsis ls-row-title">{h.song || h.title}</div>
                      <div className="ellipsis muted small">{[h.artist, h.source, ago(h.heard_at)].filter(Boolean).join(' · ')}</div>
                    </div>
                  </div>
                ))}
              </div>
            </section>
          )}
        </div>
      )}
    </div>
  )
}

/** What plays now (or played last), big. Nothing played yet: nothing shown. */
function NowHero() {
  const p = usePlayer()
  if (!p.item) return null
  const on = p.status === 'playing' || p.status === 'loading'
  const station = p.item.kind === 'station' ? p.item.station : null
  const channel = p.item.kind === 'channel' ? p.item.channel : null
  const line = p.status === 'error' ? p.error
    : p.song ? p.song.song : on ? (p.status === 'loading' ? 'Tuning in…' : 'On air') : 'Press play to listen again'
  return (
    <section className={`card ls-hero ${on ? 'on' : ''}`}>
      <div className="ls-hero-art">
        {station ? <StationLogo station={station} size={84} /> : channel && (
          p.song?.cover ? <img className="st-logo" style={{ width: 84, height: 84 }} src={`${V1}/cover/${encodeURIComponent(p.song.cover)}?size=200`} alt="" />
            : <ChannelLogo channel={channel} size={84} />
        )}
      </div>
      <div className="ls-hero-text">
        <div className="ls-hero-kicker">{on ? 'Now playing' : 'Last played'}{channel ? ' · your channel' : ''}</div>
        <h2 className="ellipsis">{p.name}</h2>
        <div className={`ls-hero-song ellipsis ${p.status === 'error' ? 'err' : ''}`} title={line}>{line}</div>
        <div className="ls-hero-artist ellipsis">{p.status !== 'error' && p.song?.artist ? p.song.artist : ' '}</div>
      </div>
      <button type="button" className="btn primary ls-hero-play" onClick={p.toggle} aria-label={on ? 'Stop' : 'Play'} title={on ? 'Stop' : 'Play'}>
        <Icon name={on ? 'stop' : 'play'} size={26} strokeWidth={2} />
      </button>
    </section>
  )
}
