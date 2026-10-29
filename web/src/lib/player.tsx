// The one player of the app: a station (through the server's relay, or HLS
// straight in the browser) or a channel from the music library. Live radio
// has no "pause" — pausing stops the stream and playing again tunes in
// fresh. The song comes from /now (stations) or /channels/{id}/now, asked
// every 10 seconds while playing; the state only changes when the song does.
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { api, V1 } from './api'
import type { Channel, ChannelNow, NowPlaying, Station } from './types'

export type Playable = { kind: 'station'; station: Station } | { kind: 'channel'; channel: Channel }
export type PlayStatus = 'idle' | 'loading' | 'playing' | 'paused' | 'error'

export interface Song { title: string; artist: string; song: string; cover?: string; since?: string }

export interface PlayerState {
  item: Playable | null
  status: PlayStatus
  error: string
  song: Song | null
  volume: number
  muted: boolean
  sleepAt: number | null // epoch ms the sleep timer stops at
}

interface PlayerApi extends PlayerState {
  play: (p: Playable) => void
  toggle: () => void
  stop: () => void
  setVolume: (v: number) => void
  setMuted: (m: boolean) => void
  setSleep: (minutes: number | null) => void
  isCurrent: (kind: 'station' | 'channel', id: number) => boolean
  name: string
}

const Ctx = createContext<PlayerApi | null>(null)
export function usePlayer() {
  const c = useContext(Ctx)
  if (!c) throw new Error('usePlayer outside PlayerProvider')
  return c
}

export const itemName = (p: Playable | null) => (p ? (p.kind === 'station' ? p.station.name : p.channel.name) : '')
export const itemId = (p: Playable | null) => (p ? (p.kind === 'station' ? p.station.id : p.channel.id) : 0)

const VOLUME_KEY = 'pra-volume'
const LAST_KEY = 'pra-last'

function newSid() {
  const a = new Uint8Array(9)
  crypto.getRandomValues(a)
  return Array.from(a, b => b.toString(16).padStart(2, '0')).join('')
}

function canNativeHLS(a: HTMLAudioElement) {
  return a.canPlayType('application/vnd.apple.mpegurl') !== ''
}

type HlsLike = { destroy: () => void; loadSource: (u: string) => void; attachMedia: (m: HTMLMediaElement) => void; on: (ev: string, cb: (e: string, d: { fatal?: boolean }) => void) => void }

/** The logo address through the server (kept on disk there, always https). */
export const logoSrc = (url: string) => (url ? `${V1}/logo?u=${encodeURIComponent(url)}` : '')

export function PlayerProvider({ children }: { children: React.ReactNode }) {
  const audio = useRef<HTMLAudioElement | null>(null)
  if (!audio.current && typeof Audio !== 'undefined') {
    audio.current = new Audio()
    audio.current.preload = 'none'
  }
  const [st, setSt] = useState<PlayerState>(() => {
    let last: Playable | null = null
    try { last = JSON.parse(localStorage.getItem(LAST_KEY) || 'null') } catch { /* nothing kept */ }
    const v = Number(localStorage.getItem(VOLUME_KEY) ?? '0.9')
    return { item: last, status: last ? 'paused' : 'idle', error: '', song: null, volume: isFinite(v) ? Math.min(1, Math.max(0, v)) : 0.9, muted: false, sleepAt: null }
  })
  const stRef = useRef(st)
  stRef.current = st
  const sid = useRef('')
  const hls = useRef<HlsLike | null>(null)
  const beat = useRef<number | null>(null)
  const poll = useRef<number | null>(null)
  const sleepTimer = useRef<number | null>(null)
  const retries = useRef(0)
  const playToken = useRef(0)

  const set = useCallback((p: Partial<PlayerState>) => setSt(s => ({ ...s, ...p })), [])

  const clearTimers = () => {
    if (beat.current) { window.clearInterval(beat.current); beat.current = null }
    if (poll.current) { window.clearInterval(poll.current); poll.current = null }
  }

  const teardown = useCallback(() => {
    clearTimers()
    const a = audio.current
    const item = stRef.current.item
    if (item?.kind === 'station' && item.station.hls && sid.current) {
      api.post(`${V1}/listens/beat`, { sid: sid.current, station_id: item.station.id, stop: true }).catch(() => {})
    }
    if (hls.current) { hls.current.destroy(); hls.current = null }
    if (a) { a.pause(); a.removeAttribute('src'); a.load() }
    sid.current = ''
  }, [])

  // the song: asked every 10 s, the state changes only when it does
  const fetchNow = useCallback(async (token: number) => {
    const item = stRef.current.item
    if (!item || token !== playToken.current) return
    let song: Song | null = null
    try {
      if (item.kind === 'station') {
        const n = await api.get<NowPlaying>(`${V1}/now?sid=${sid.current}`)
        if (n.playing && n.title) song = { title: n.title, artist: n.artist || '', song: n.song || n.title, since: n.title_since }
      } else {
        const n = await api.get<ChannelNow>(`${V1}/channels/${item.channel.id}/now`)
        if (n.song) song = { title: (n.song.artist ? n.song.artist + ' - ' : '') + n.song.title, artist: n.song.artist, song: n.song.title, cover: n.song.cover, since: n.started_at }
      }
    } catch { return }
    if (token !== playToken.current) return
    const cur = stRef.current.song
    if ((cur?.title || '') !== (song?.title || '')) set({ song })
  }, [set])

  const start = useCallback(async (item: Playable) => {
    const a = audio.current
    if (!a) return
    teardown()
    const token = ++playToken.current
    sid.current = newSid()
    set({ item, status: 'loading', error: '', song: null })
    localStorage.setItem(LAST_KEY, JSON.stringify(item))
    a.volume = stRef.current.volume
    a.muted = stRef.current.muted
    try {
      if (item.kind === 'station' && item.station.hls) {
        const url = item.station.url_resolved || item.station.url
        if (canNativeHLS(a)) {
          a.src = url
        } else {
          const { default: Hls } = await import('hls.js')
          if (token !== playToken.current) return
          if (!Hls.isSupported()) throw new Error('This browser cannot play HLS stations.')
          const h = new Hls({ liveDurationInfinity: true }) as unknown as HlsLike
          hls.current = h
          h.on(Hls.Events.ERROR, (_e, d) => { if (d.fatal && token === playToken.current) set({ status: 'error', error: 'This station is not playing right now.' }) })
          h.loadSource(url)
          h.attachMedia(a)
        }
        const send = () => api.post(`${V1}/listens/beat`, { sid: sid.current, station_id: item.station.id }).catch(() => {})
        send()
        beat.current = window.setInterval(send, 30000)
      } else {
        const path = item.kind === 'station' ? `/stream/station/${item.station.id}` : `/stream/channel/${item.channel.id}`
        a.src = `${path}?sid=${sid.current}`
      }
      await a.play()
    } catch (e) {
      if (token !== playToken.current) return
      const msg = (e as Error)?.name === 'NotAllowedError' ? 'Press play to start.' : (e as Error)?.message || 'This station is not playing right now.'
      set({ status: (e as Error)?.name === 'NotAllowedError' ? 'paused' : 'error', error: msg })
      return
    }
    poll.current = window.setInterval(() => fetchNow(token), 10000)
    window.setTimeout(() => fetchNow(token), 2500)
  }, [fetchNow, set, teardown])

  // what the audio element says
  useEffect(() => {
    const a = audio.current
    if (!a) return
    const onPlaying = () => { retries.current = 0; set({ status: 'playing', error: '' }) }
    const onWaiting = () => { if (stRef.current.status === 'playing') set({ status: 'loading' }) }
    const onError = () => {
      const item = stRef.current.item
      if (!item || !a.getAttribute('src') && !hls.current) return
      // a live stream that drops is tuned in again (three times)
      if (stRef.current.status === 'playing' && retries.current < 3 && item) {
        retries.current++
        window.setTimeout(() => { if (stRef.current.item === item) start(item) }, 1500 * retries.current)
        return
      }
      teardown()
      set({ status: 'error', error: item.kind === 'channel' ? 'The channel could not play. Is your music library connected (Settings → Connected apps)?' : 'This station is not playing right now. Try again later, or check its address.' })
    }
    const onEnded = () => { const item = stRef.current.item; if (item && stRef.current.status === 'playing' && retries.current < 3) { retries.current++; start(item) } }
    a.addEventListener('playing', onPlaying)
    a.addEventListener('waiting', onWaiting)
    a.addEventListener('error', onError)
    a.addEventListener('ended', onEnded)
    return () => { a.removeEventListener('playing', onPlaying); a.removeEventListener('waiting', onWaiting); a.removeEventListener('error', onError); a.removeEventListener('ended', onEnded) }
  }, [set, start, teardown])

  const stop = useCallback(() => {
    playToken.current++
    teardown()
    set({ status: stRef.current.item ? 'paused' : 'idle', song: null, error: '' })
  }, [set, teardown])

  const play = useCallback((p: Playable) => { retries.current = 0; start(p) }, [start])
  const toggle = useCallback(() => {
    const s = stRef.current
    if (!s.item) return
    if (s.status === 'playing' || s.status === 'loading') stop()
    else play(s.item)
  }, [play, stop])

  const setVolume = useCallback((v: number) => {
    const x = Math.min(1, Math.max(0, v))
    if (audio.current) audio.current.volume = x
    localStorage.setItem(VOLUME_KEY, String(x))
    set({ volume: x, muted: x === 0 ? stRef.current.muted : false })
    if (audio.current && x > 0) audio.current.muted = false
  }, [set])
  const setMuted = useCallback((m: boolean) => { if (audio.current) audio.current.muted = m; set({ muted: m }) }, [set])

  const setSleep = useCallback((minutes: number | null) => {
    if (sleepTimer.current) { window.clearTimeout(sleepTimer.current); sleepTimer.current = null }
    if (!minutes) { set({ sleepAt: null }); return }
    const at = Date.now() + minutes * 60000
    sleepTimer.current = window.setTimeout(() => { sleepTimer.current = null; set({ sleepAt: null }); stop() }, minutes * 60000)
    set({ sleepAt: at })
  }, [set, stop])

  // lock screen and headphone buttons
  useEffect(() => {
    const ms = navigator.mediaSession
    if (!ms) return
    const name = itemName(st.item)
    if (!st.item) { ms.metadata = null; return }
    const art: MediaImage[] = []
    if (st.item.kind === 'station' && st.item.station.logo_url) art.push({ src: logoSrc(st.item.station.logo_url), sizes: '256x256' })
    else if (st.item.kind === 'channel' && st.song?.cover) art.push({ src: `${V1}/cover/${encodeURIComponent(st.song.cover)}?size=512`, sizes: '512x512' })
    else art.push({ src: '/icon-512.png', sizes: '512x512', type: 'image/png' })
    ms.metadata = new MediaMetadata({ title: st.song?.song || name, artist: st.song?.artist || name, album: st.song ? name : 'Pocket Radio', artwork: art })
    ms.playbackState = st.status === 'playing' ? 'playing' : st.status === 'paused' ? 'paused' : 'none'
  }, [st.item, st.song, st.status])
  useEffect(() => {
    const ms = navigator.mediaSession
    if (!ms) return
    try {
      ms.setActionHandler('play', () => toggle())
      ms.setActionHandler('pause', () => stop())
      ms.setActionHandler('stop', () => stop())
    } catch { /* not supported */ }
  }, [toggle, stop])

  useEffect(() => () => { teardown() }, [teardown])

  const isCurrent = useCallback((kind: 'station' | 'channel', id: number) => {
    const it = st.item
    return !!it && it.kind === kind && itemId(it) === id && (st.status === 'playing' || st.status === 'loading')
  }, [st.item, st.status])

  const value = useMemo<PlayerApi>(() => ({ ...st, play, toggle, stop, setVolume, setMuted, setSleep, isCurrent, name: itemName(st.item) }),
    [st, play, toggle, stop, setVolume, setMuted, setSleep, isCurrent])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}
