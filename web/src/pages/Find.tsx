// Find stations in the open station directory (radio-browser.info): by
// name, genre and country. A result plays without being kept (it shows up
// under "Played lately"); the heart keeps it in your stations.
import { useEffect, useMemo, useRef, useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { usePlayer } from '../lib/player'
import type { DirCountry, DirStation, DirTag, Station } from '../lib/types'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { StationLogo } from '../components/StationLogo'
import { announceChange, openCreate, useCrumb } from '../components/Layout'
import { Empty, Loading, Spinner, errMsg, useDebounce, useToast } from '../components/ui'

const PAGE = 40
const ORDERS = [
  { value: 'popular', label: 'Most played' },
  { value: 'votes', label: 'Most liked' },
  { value: 'new', label: 'Newest' },
  { value: 'name', label: 'A to Z' },
]

/** What POST /stations needs to keep (or just play) a directory station. */
export function dirPayload(d: DirStation, favorite: boolean) {
  return {
    name: d.name, url: d.url, url_resolved: d.url_resolved, homepage: d.homepage, logo_url: d.logo_url, tags: d.tags,
    country_code: d.country_code, language: d.language, codec: d.codec, bitrate: d.bitrate, hls: d.hls, directory_id: d.id, favorite,
  }
}

interface SearchResult { ok: boolean; error?: string; stations: DirStation[] }

export default function FindPage() {
  useCrumb(undefined)
  const { settings } = useApp()
  const p = usePlayer()
  const { toast } = useToast()
  const [q, setQ] = useState('')
  const dq = useDebounce(q.trim(), 350)
  const [tag, setTag] = useState('')
  const [country, setCountry] = useState<string | null>(null)
  const [order, setOrder] = useState('popular')
  const [tags, setTags] = useState<DirTag[]>([])
  const [countries, setCountries] = useState<DirCountry[]>([])
  const [list, setList] = useState<DirStation[] | null>(null)
  const [more, setMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<string | null>(null) // the row being saved or started
  const req = useRef(0)

  const on = settings?.directory !== false
  const where = country ?? settings?.country ?? ''

  useEffect(() => {
    if (!on) return
    api.get<{ tags: DirTag[] }>(`${V1}/directory/tags`).then(r => setTags(r.tags || [])).catch(() => {})
    api.get<{ countries: DirCountry[] }>(`${V1}/directory/countries`).then(r => setCountries(r.countries || [])).catch(() => {})
  }, [on])

  const query = (offset: number) => {
    const sp = new URLSearchParams({ limit: String(PAGE), offset: String(offset), order })
    if (dq) sp.set('q', dq)
    if (tag) sp.set('tag', tag)
    if (where) sp.set('country', where)
    return `${V1}/directory/search?${sp}`
  }

  useEffect(() => {
    if (!on || !settings) return
    const n = ++req.current
    setList(null)
    setError('')
    api.get<SearchResult>(query(0)).then(r => {
      if (n !== req.current) return
      setList(r.stations || [])
      setMore((r.stations || []).length >= PAGE)
      if (!r.ok) setError(r.error || 'The station directory did not answer.')
    }).catch(e => { if (n === req.current) { setList([]); setError(errMsg(e)) } })
  }, [dq, tag, where, order, on, settings ? 1 : 0]) // eslint-disable-line react-hooks/exhaustive-deps

  const loadMore = async () => {
    if (!list) return
    const n = req.current
    setLoadingMore(true)
    try {
      const r = await api.get<SearchResult>(query(list.length))
      if (n !== req.current) return
      const seen = new Set(list.map(x => x.id))
      setList([...list, ...(r.stations || []).filter(x => !seen.has(x.id))])
      setMore((r.stations || []).length >= PAGE)
    } catch (e) { toast(errMsg(e), 'err') } finally { setLoadingMore(false) }
  }

  const patch = (id: string, ch: Partial<DirStation>) => setList(l => l && l.map(x => (x.id === id ? { ...x, ...ch } : x)))

  const play = async (d: DirStation) => {
    if (d.saved_id && p.isCurrent('station', d.saved_id)) { p.stop(); return }
    setBusy(d.id)
    try {
      const st = await api.post<Station>(`${V1}/stations`, dirPayload(d, !!d.favorite))
      patch(d.id, { saved_id: st.id, favorite: st.favorite })
      p.play({ kind: 'station', station: st })
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(null) }
  }

  const keep = async (d: DirStation) => {
    setBusy(d.id)
    try {
      if (d.favorite && d.saved_id) {
        await api.put<Station>(`${V1}/stations/${d.saved_id}`, { favorite: false })
        patch(d.id, { favorite: false })
        toast(`${d.name} is no longer in your stations`, 'ok')
      } else {
        const st = await api.post<Station>(`${V1}/stations`, dirPayload(d, true))
        patch(d.id, { saved_id: st.id, favorite: true })
        toast(`${d.name} is in your stations`, 'ok')
      }
      announceChange()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(null) }
  }

  const topTags = tags.slice(0, 24)
  const tagOptions = useMemo(() => tags.map(t => ({ value: t.name, label: t.name, hint: t.count.toLocaleString() })), [tags])
  const countryOptions = useMemo(() => [{ value: '', label: 'Anywhere' }, ...countries.map(c => ({ value: c.code, label: c.name, hint: c.count.toLocaleString() }))], [countries])
  const countryName = countries.find(c => c.code === where)?.name || where

  if (!settings) return <Loading />
  if (!on) {
    return (
      <Empty title="The station directory is off on this box" icon="globe">
        Whoever runs this box switched the directory off. You can still add a station by its address.
      </Empty>
    )
  }

  const heading = dq ? `Stations called “${dq}”` : tag ? `${tag[0].toUpperCase() + tag.slice(1)} stations${where ? ' in ' + countryName : ''}` : where ? `Popular in ${countryName}` : 'Popular everywhere'

  return (
    <div className="fd">
      <div className="fd-bar">
        <div className="fd-search">
          <Icon name="search" size={16} />
          <input value={q} onChange={e => setQ(e.target.value)} placeholder="Station name, like “Jazz FM” or “BBC”" aria-label="Search stations" />
          {q && <button type="button" className="btn ghost icon-only sm" onClick={() => setQ('')} aria-label="Clear"><Icon name="x" size={14} /></button>}
        </div>
        <div className="fd-selects">
          <Select value={where} onChange={v => setCountry(v)} options={countryOptions} ariaLabel="Country" placeholder="Anywhere" />
          <Select value={order} onChange={setOrder} options={ORDERS} ariaLabel="Order" />
        </div>
      </div>

      {topTags.length > 0 && (
        <div className="fd-tags">
          <div className="chips">
            {topTags.map(t => (
              <button key={t.name} type="button" className={`chip ${tag === t.name ? 'active' : ''}`} onClick={() => setTag(tag === t.name ? '' : t.name)}>{t.name}</button>
            ))}
          </div>
          {tags.length > topTags.length && (
            <div className="fd-more-tags">
              <Select value={topTags.some(t => t.name === tag) ? '' : tag} onChange={setTag} options={tagOptions} placeholder="More genres…" ariaLabel="More genres" clearLabel="Any genre" />
            </div>
          )}
        </div>
      )}

      <div className="fd-head">
        <h3 className="ellipsis">{heading}</h3>
        {tag && <button type="button" className="btn ghost sm" onClick={() => setTag('')}><Icon name="x" size={13} />Any genre</button>}
      </div>

      {error && <div className="callout warn"><Icon name="alert" size={15} /><div>{error}</div></div>}

      {list === null ? <Loading label="Looking…" /> : list.length === 0 ? (
        !error && (
          <Empty title="No stations found" icon="search">
            Try fewer words, another genre or “Anywhere”. Know the address? <button type="button" className="link-btn" onClick={() => openCreate('station')}>Add it by its address</button>.
          </Empty>
        )
      ) : (
        <div className="card fd-list">
          {list.map(d => {
            const playing = !!d.saved_id && p.isCurrent('station', d.saved_id)
            const meta = [d.tags.split(',').slice(0, 3).map(s => s.trim()).filter(Boolean).join(', '), d.country || d.country_code, [d.codec, d.bitrate ? `${d.bitrate} kbit/s` : ''].filter(Boolean).join(' ')].filter(Boolean).join(' · ')
            return (
              <div key={d.id} className={`fd-row ${playing ? 'on' : ''}`}>
                <StationLogo station={{ name: d.name, logo_url: d.logo_url }} size={42} />
                <div className="grow">
                  <div className="fd-name ellipsis">{d.name}</div>
                  <div className="fd-meta ellipsis">{meta || 'Radio'}</div>
                </div>
                <button type="button" className={`btn ghost icon-only fd-heart ${d.favorite ? 'on' : ''}`} disabled={busy === d.id}
                  onClick={() => keep(d)} aria-label={d.favorite ? `Remove ${d.name} from your stations` : `Keep ${d.name}`} title={d.favorite ? 'In your stations (tap to remove)' : 'Keep it in your stations'}>
                  <Icon name="heart" size={17} />
                </button>
                <button type="button" className={`btn icon-only fd-play ${playing ? 'primary' : ''}`} disabled={busy === d.id} onClick={() => play(d)} aria-label={playing ? 'Stop' : `Play ${d.name}`} title={playing ? 'Stop' : 'Play'}>
                  {busy === d.id ? <Spinner size={14} /> : <Icon name={playing ? 'stop' : 'play'} size={15} strokeWidth={2} />}
                </button>
              </div>
            )
          })}
        </div>
      )}

      {list && list.length > 0 && more && (
        <div className="fd-foot">
          <button type="button" className="btn" onClick={loadMore} disabled={loadingMore}>{loadingMore ? <Spinner size={14} /> : <Icon name="chevron-down" size={15} />}Show more</button>
        </div>
      )}
      <p className="muted small fd-credit">Stations come from <a href="https://www.radio-browser.info" target="_blank" rel="noreferrer">radio-browser.info</a>, a free list kept by listeners.</p>
    </div>
  )
}
