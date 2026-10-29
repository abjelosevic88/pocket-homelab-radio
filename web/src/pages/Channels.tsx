// Your channels: stations of your own, played from your music library like
// real radio. The list, and one channel with what it plays now, what it
// played lately and the address for other players.
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { usePlayer } from '../lib/player'
import { ago, fmtDateTime, fmtISOTime } from '../lib/format'
import type { Channel, ChannelNow, LibraryInfo } from '../lib/types'
import { Icon } from '../components/icons'
import { ChannelLogo } from '../components/StationLogo'
import { ChannelEditor, rulesSummary } from '../components/ChannelEditor'
import { announceChange, openCreate, useCrumb, useOnChange } from '../components/Layout'
import { Confirm, CopyField, Empty, Loading, MenuButton, PageHeader, Spinner, useToast } from '../components/ui'

function PlayButton({ channel, size = '' }: { channel: Channel; size?: '' | 'lg' }) {
  const p = usePlayer()
  const on = p.isCurrent('channel', channel.id)
  const busy = on && p.status === 'loading'
  return (
    <button type="button" className={`btn ${on ? '' : 'primary'} ch-play ${size}`} aria-label={on ? `Stop ${channel.name}` : `Play ${channel.name}`} title={on ? 'Stop' : 'Play'}
      onClick={e => { e.preventDefault(); e.stopPropagation(); if (on) p.stop(); else p.play({ kind: 'channel', channel }) }}>
      {busy ? <Spinner size={16} /> : <Icon name={on ? 'stop' : 'play'} size={size === 'lg' ? 18 : 16} strokeWidth={2} />}
      {size === 'lg' && (on ? 'Stop' : 'Play')}
    </button>
  )
}

function LibraryHint({ lib }: { lib: LibraryInfo | null }) {
  if (!lib || lib.ok) return null
  return (
    <div className="callout warn mb">
      <Icon name="alert" size={15} />
      <div>
        {lib.not_connected ? 'Channels play from your own music library, and it is not connected yet' : 'Your music library did not answer'}{lib.error ? `: ${lib.error}` : '.'}{' '}
        <Link to="/settings/apps">Connect it in Settings → Connected apps</Link>
      </div>
    </div>
  )
}

export default function Channels() {
  const p = usePlayer()
  const [list, setList] = useState<Channel[] | null>(null)
  const [lib, setLib] = useState<LibraryInfo | null>(null)
  const [error, setError] = useState('')
  const load = useCallback(() => { api.get<Channel[]>(`${V1}/channels`).then(l => { setList(l); setError('') }).catch(e => setError((e as Error).message)) }, [])
  useEffect(() => { load() }, [load])
  useEffect(() => { api.get<LibraryInfo>(`${V1}/library`).then(setLib).catch(() => {}) }, [])
  useOnChange(load)

  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!list) return <Loading />
  return (
    <div className="ch-page">
      <PageHeader title="Your channels" sub="Stations of your own, from your music library. They play like real radio: everyone listening hears the same song."
        actions={<button type="button" className="btn primary" onClick={() => openCreate('channel')}><Icon name="plus" size={15} />Make a channel</button>} />
      <LibraryHint lib={lib} />
      {list.length === 0 ? (
        <Empty title="No channels yet" icon="radio">
          <p>A channel picks songs from your own music by the rules you set — some genres, a few artists, a decade, the songs you starred — and plays them one after the other, like a radio station that only plays what you like.</p>
          <p>You can listen here, in the car or in any radio app.</p>
          <div className="row center mt"><button type="button" className="btn primary" onClick={() => openCreate('channel')}><Icon name="plus" size={15} />Make your first channel</button></div>
        </Empty>
      ) : (
        <div className="ch-grid">
          {list.map(c => {
            const mine = p.item?.kind === 'channel' && p.item.channel.id === c.id && (p.status === 'playing' || p.status === 'loading')
            const now = mine ? p.song?.title || 'Tuning in…' : c.on_air ? c.now || 'On air' : ''
            return (
              <Link key={c.id} to={`/channels/${c.id}`} className={`ch-card ${mine ? 'on' : ''}`}>
                <ChannelLogo channel={c} size={52} />
                <div className="ch-card-text">
                  <div className="ch-card-name ellipsis">{c.name}</div>
                  {c.description && <div className="muted small ch-card-desc">{c.description}</div>}
                  <div className="ch-card-rules small ellipsis" title={rulesSummary(c.rules)}>{rulesSummary(c.rules)}</div>
                  <div className={`ch-card-now small ellipsis ${now ? 'live' : 'muted'}`}>
                    {now ? <><span className="ch-dot" aria-hidden />{now}</> : c.last_played_at ? `Last played ${ago(c.last_played_at)}` : 'Not played yet'}
                  </div>
                </div>
                <PlayButton channel={c} />
              </Link>
            )
          })}
        </div>
      )}
    </div>
  )
}

export function ChannelPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const p = usePlayer()
  const { toast } = useToast()
  const [c, setC] = useState<Channel | null>(null)
  const [error, setError] = useState('')
  const [now, setNow] = useState<ChannelNow | null>(null)
  const [edit, setEdit] = useState(false)
  const [del, setDel] = useState(false)
  const [newAddr, setNewAddr] = useState(false)
  useCrumb(c?.name)

  const load = useCallback(() => {
    api.get<Channel>(`${V1}/channels/${id}`).then(x => { setC(x); setError('') }).catch(e => setError((e as Error).message))
  }, [id])
  useEffect(() => { load() }, [load])
  useOnChange(load)

  const playing = !!c && p.isCurrent('channel', c.id)
  const nowKey = useRef('')
  const fetchNow = useCallback(() => {
    api.get<ChannelNow>(`${V1}/channels/${id}/now`).then(n => {
      const key = `${n.on_air}|${n.song?.id || ''}|${n.error || ''}|${n.recent.map(r => r.id).join(',')}`
      if (key !== nowKey.current) { nowKey.current = key; setNow(n) }
    }).catch(() => {})
  }, [id])
  useEffect(() => { fetchNow() }, [fetchNow, playing])
  const live = playing || !!now?.on_air
  useEffect(() => {
    if (!live) return
    const t = window.setInterval(fetchNow, 10000)
    return () => window.clearInterval(t)
  }, [live, fetchNow])

  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!c) return <Loading />

  const remove = async () => {
    setDel(false)
    try {
      if (playing) p.stop()
      await api.del(`${V1}/channels/${c.id}`)
      toast(`${c.name} is gone`)
      announceChange()
      navigate('/channels')
    } catch (e) { toast((e as Error).message, 'err') }
  }
  const rotate = async () => {
    setNewAddr(false)
    try { setC(await api.post<Channel>(`${V1}/channels/${c.id}/address`)); toast('New address made; the old one no longer plays') } catch (e) { toast((e as Error).message, 'err') }
  }
  const song = now?.song

  return (
    <div className="ch-page">
      <div className="ch-head">
        <ChannelLogo channel={c} size={64} />
        <div className="grow">
          <h1 className="ch-title">{c.name}</h1>
          {c.description && <p className="muted ch-desc">{c.description}</p>}
          <p className="small ch-card-rules">{rulesSummary(c.rules)}</p>
        </div>
        <div className="ch-head-actions">
          <PlayButton channel={c} size="lg" />
          <button type="button" className="btn" onClick={() => setEdit(true)}><Icon name="edit" size={15} />Change</button>
          <MenuButton label="More" align="right" items={[
            { label: 'New listen address…', icon: 'refresh', onSelect: () => setNewAddr(true) },
            'sep',
            { label: 'Delete the channel…', icon: 'trash', danger: true, onSelect: () => setDel(true) },
          ]}><Icon name="more" size={18} /></MenuButton>
        </div>
      </div>

      <section className="card ch-now">
        <div className="ch-now-art">
          {song?.cover ? <img src={`${V1}/cover/${encodeURIComponent(song.cover)}?size=300`} alt="" /> : <ChannelLogo channel={c} size={120} />}
        </div>
        <div className="ch-now-text">
          <div className={`ch-now-label small ${now?.on_air ? 'live' : 'muted'}`}>
            {now?.on_air ? <><span className="ch-dot" aria-hidden />On air{now.listeners > 0 ? ` · ${now.listeners} listening` : ''}</> : 'Off air'}
          </div>
          {song ? <>
            <div className="ch-now-song ellipsis">{song.title}</div>
            <div className="ch-now-artist ellipsis">{song.artist}</div>
            <div className="muted small ellipsis">{[song.album, song.year || ''].filter(Boolean).join(' · ')}</div>
          </> : <div className="muted ch-now-off">{now?.error ? <span className="bad">{now.error}</span> : 'Press play to put it on air. It starts with a new song and keeps going while anyone listens.'}</div>}
        </div>
      </section>

      <div className="two-col ch-cols">
        <section className="card">
          <div className="card-head"><div className="grow"><h2>Played lately</h2></div></div>
          <div className="card-body flush">
            {!now ? <Loading /> : now.recent.length === 0 ? <p className="muted ch-pad">Nothing yet.</p> : (
              <ul className="ch-recent">
                {now.recent.map(h => (
                  <li key={h.id}>
                    <span className="grow"><span className="ellipsis block">{h.song || h.title}</span><span className="muted small ellipsis block">{h.artist}</span></span>
                    <span className="muted small tnum">{new Date(h.heard_at).toDateString() === new Date().toDateString() ? fmtISOTime(h.heard_at) : fmtDateTime(h.heard_at, false)}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </section>

        <section className="card">
          <div className="card-head"><div className="grow"><h2>Listen anywhere</h2><p className="muted small">In VLC, in the car, on a phone radio app — anything that plays internet radio.</p></div></div>
          <div className="card-body stack">
            <div className="field"><span className="label">Stream address</span><CopyField value={c.listen_url} label="Stream address" /></div>
            <div className="field"><span className="label">Playlist file (.m3u)</span><CopyField value={c.playlist_url} label="Playlist address" /><span className="help">Some apps want a playlist file instead; open this address in them.</span></div>
            <div className="callout"><Icon name="lock" size={15} /><div>Anyone who has this address can listen, without signing in. If it got out, make a new one: <button type="button" className="conn-advanced" onClick={() => setNewAddr(true)}>New address</button></div></div>
          </div>
        </section>
      </div>

      {edit && <ChannelEditor channel={c} onClose={() => setEdit(false)} onSaved={x => { setEdit(false); setC(x); announceChange() }} />}
      {del && <Confirm title={`Delete ${c.name}?`} message="The channel and its listen address go away. The songs stay in your library; your listening history stays." confirmLabel="Delete" onCancel={() => setDel(false)} onConfirm={remove} />}
      {newAddr && <Confirm title="Make a new listen address?" message="Players and radio apps using the old address stop and need the new one." confirmLabel="Make a new address" danger={false} onCancel={() => setNewAddr(false)} onConfirm={rotate} />}
    </div>
  )
}
