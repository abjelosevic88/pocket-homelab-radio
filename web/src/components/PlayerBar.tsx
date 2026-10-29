// The bar at the bottom of every page: what plays, the song, play/stop,
// volume, the sleep timer and "keep this station". Nothing in it moves
// while the song changes: the title is clipped in a fixed space.
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { usePlayer } from '../lib/player'
import type { Station } from '../lib/types'
import { Icon } from './icons'
import { ChannelLogo, StationLogo } from './StationLogo'
import { type Anchor, Menu, type MenuItem, rectOf, Spinner, useIsMobile, useToast } from './ui'
import { announceChange } from './Layout'

function left(at: number) {
  const m = Math.max(0, Math.ceil((at - Date.now()) / 60000))
  return m >= 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${m} min`
}

export function PlayerBar() {
  const p = usePlayer()
  const { settings } = useApp()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const [sleepMenu, setSleepMenu] = useState<Anchor | null>(null)
  const [, tick] = useState(0)
  useEffect(() => {
    if (!p.sleepAt) return
    const t = window.setInterval(() => tick(x => x + 1), 30000)
    return () => window.clearInterval(t)
  }, [p.sleepAt])
  if (!p.item) return null

  const station = p.item.kind === 'station' ? p.item.station : null
  const channel = p.item.kind === 'channel' ? p.item.channel : null
  const busy = p.status === 'loading'
  const on = p.status === 'playing' || busy
  const line = p.status === 'error' ? p.error : p.song?.title || (on ? (busy ? 'Tuning in…' : 'On air') : p.error || 'Stopped')

  const keep = async (s: Station) => {
    try {
      const saved = await api.post<Station>(`${V1}/stations`, { ...s, favorite: true })
      p.play({ kind: 'station', station: saved })
      toast(`${s.name} is in your stations`)
      announceChange()
    } catch (e) { toast((e as Error).message, 'err') }
  }

  const def = settings?.sleep_minutes || 30
  const sleepItems: MenuItem[] = [
    { heading: p.sleepAt ? `Stops in ${left(p.sleepAt)}` : 'Stop playing after' },
    ...[15, 30, 45, 60, 90].map(m => ({ label: `${m} minutes${m === def ? ' (usual)' : ''}`, checked: false, onSelect: () => p.setSleep(m) })),
    ...(p.sleepAt ? ['sep' as const, { label: 'Turn the sleep timer off', icon: 'x', onSelect: () => p.setSleep(null) }] : []),
  ]

  return (
    <div className={`player-bar ${p.status}`} role="region" aria-label="Player">
      <Link to={station ? (station.favorite ? '/stations' : '/find') : '/channels'} className="pb-art" title={p.name}>
        {station ? <StationLogo station={station} size={mobile ? 40 : 48} /> : channel && (
          p.song?.cover ? <img className="st-logo" style={{ width: mobile ? 40 : 48, height: mobile ? 40 : 48 }} src={`${V1}/cover/${encodeURIComponent(p.song.cover)}?size=120`} alt="" />
            : <ChannelLogo channel={channel} size={mobile ? 40 : 48} />
        )}
      </Link>
      <div className="pb-text">
        <div className="pb-name ellipsis">{p.name}{channel && <span className="pb-tag">your channel</span>}</div>
        <div className={`pb-song ellipsis ${p.status === 'error' ? 'err' : ''}`} title={line}>{line}</div>
      </div>
      <div className="pb-controls">
        {station && !station.favorite && !mobile && (
          <button type="button" className="btn ghost sm" onClick={() => keep(station)} title="Keep it in your stations"><Icon name="heart" size={15} />Keep</button>
        )}
        <button type="button" className={`btn ghost icon-only pb-sleep ${p.sleepAt ? 'on' : ''}`} aria-haspopup="menu" aria-label="Sleep timer"
          title={p.sleepAt ? `Sleep timer: stops in ${left(p.sleepAt)}` : 'Sleep timer'} onClick={e => setSleepMenu(sleepMenu ? null : rectOf(e.currentTarget))}>
          <Icon name="moon" size={17} />
        </button>
        {!mobile && (
          <div className="pb-volume">
            <button type="button" className="btn ghost icon-only" aria-label={p.muted ? 'Sound on' : 'Mute'} title={p.muted ? 'Sound on' : 'Mute'} onClick={() => p.setMuted(!p.muted)}>
              <Icon name={p.muted || p.volume === 0 ? 'volume-off' : 'volume'} size={17} />
            </button>
            <input type="range" min={0} max={1} step={0.02} value={p.muted ? 0 : p.volume} onChange={e => p.setVolume(Number(e.target.value))} aria-label="Volume" />
          </div>
        )}
        <button type="button" className="btn primary pb-play" onClick={p.toggle} aria-label={on ? 'Stop' : 'Play'} title={on ? 'Stop' : 'Play'}>
          {busy ? <Spinner size={18} /> : <Icon name={on ? 'stop' : 'play'} size={20} strokeWidth={2} />}
        </button>
      </div>
      {sleepMenu && <Menu anchor={sleepMenu} align="right" items={sleepItems} onClose={() => setSleepMenu(null)} width={240} />}
    </div>
  )
}
