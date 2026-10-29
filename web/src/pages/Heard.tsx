// Songs you heard: every title a station sent while you listened, and every
// song your channels played. Star the ones you like; check whether they are
// in your music library and keep them in a playlist there.
import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, V1 } from '../lib/api'
import { fmtDate, fmtISOTime, todayISO, addDays } from '../lib/format'
import type { Heard, LibraryMatch } from '../lib/types'
import { Icon } from '../components/icons'
import { useOnChange } from '../components/Layout'
import { copyText, Empty, Loading, MenuButton, PageHeader, Segmented, Spinner, useDebounce, useToast } from '../components/ui'

const PAGE = 100

function localDay(iso: string) {
  const d = new Date(iso)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function dayTitle(day: string) {
  const today = todayISO()
  if (day === today) return 'Today'
  if (day === addDays(today, -1)) return 'Yesterday'
  return fmtDate(day, day.slice(0, 4) !== today.slice(0, 4))
}

export default function HeardPage() {
  const { toast } = useToast()
  const [view, setView] = useState<'all' | 'saved'>('all')
  const [q, setQ] = useState('')
  const dq = useDebounce(q.trim(), 300)
  const [list, setList] = useState<Heard[] | null>(null)
  const [more, setMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [busyId, setBusyId] = useState<number | null>(null)

  const url = useCallback((before = '') => {
    const p = new URLSearchParams({ limit: String(PAGE) })
    if (view === 'saved') p.set('saved', '1')
    if (dq) p.set('q', dq)
    if (before) p.set('before', before)
    return `${V1}/heard?${p}`
  }, [view, dq])

  const load = useCallback(() => {
    api.get<Heard[]>(url()).then(l => { setList(l); setMore(l.length === PAGE) }).catch(e => toast((e as Error).message, 'err'))
  }, [url, toast])
  useEffect(() => { load() }, [load])
  useOnChange(load)

  const showMore = async () => {
    if (!list?.length) return
    setLoadingMore(true)
    try { const l = await api.get<Heard[]>(url(list[list.length - 1].heard_at)); setList([...list, ...l]); setMore(l.length === PAGE) } catch (e) { toast((e as Error).message, 'err') } finally { setLoadingMore(false) }
  }

  const star = async (h: Heard) => {
    const saved = !h.saved
    setList(l => l?.map(x => (x.id === h.id ? { ...x, saved } : x)) || l)
    try { await api.put(`${V1}/heard/${h.id}`, { saved }) } catch (e) {
      setList(l => l?.map(x => (x.id === h.id ? { ...x, saved: h.saved } : x)) || l)
      toast((e as Error).message, 'err')
    }
  }

  const inLibrary = async (h: Heard) => {
    setBusyId(h.id)
    try {
      const r = await api.get<LibraryMatch>(`${V1}/heard/${h.id}/library`)
      if (!r.ok) toast(r.not_connected ? `Pocket Music is not connected: ${r.error}` : r.error || 'Pocket Music did not answer', 'err')
      else if (r.in_library) toast(`“${r.song?.title}” is in your music`, 'ok', r.open_url ? { label: 'Open', run: () => window.open(r.open_url, '_blank', 'noopener') } : undefined)
      else toast('Not in your music yet', 'ok', r.search_url ? { label: 'Search', run: () => window.open(r.search_url, '_blank', 'noopener') } : undefined)
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusyId(null) }
  }

  const toPlaylist = async (h: Heard) => {
    setBusyId(h.id)
    try {
      const r = await api.post<{ ok: boolean; error?: string; not_connected?: boolean; playlist?: string; url?: string }>(`${V1}/heard/${h.id}/playlist`)
      if (!r.ok) toast(r.error || 'That did not work', 'err')
      else toast(`Added to “${r.playlist}”`, 'ok', r.url ? { label: 'Open', run: () => window.open(r.url, '_blank', 'noopener') } : undefined)
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusyId(null) }
  }

  const remove = async (h: Heard) => {
    try { await api.del(`${V1}/heard/${h.id}`); setList(l => l?.filter(x => x.id !== h.id) || l) } catch (e) { toast((e as Error).message, 'err') }
  }

  const groups = useMemo(() => {
    const out: { day: string; items: Heard[] }[] = []
    for (const h of list || []) {
      const d = localDay(h.heard_at)
      if (out.length && out[out.length - 1].day === d) out[out.length - 1].items.push(h)
      else out.push({ day: d, items: [h] })
    }
    return out
  }, [list])

  return (
    <div className="hd-page">
      <PageHeader title="Songs you heard" sub="What the stations played while you listened, and the songs of your channels. Star the ones you like." />
      <div className="hd-toolbar">
        <Segmented value={view} onChange={setView} options={[{ value: 'all', label: 'All' }, { value: 'saved', label: 'Starred' }]} label="Show" />
        <div className="hd-search">
          <Icon name="search" size={15} />
          <input value={q} onChange={e => setQ(e.target.value)} placeholder="Find a song, an artist or a station" aria-label="Find" />
        </div>
      </div>
      {!list ? <Loading /> : list.length === 0 ? (
        dq ? <Empty title="Nothing matches" icon="search">Try fewer words.</Empty>
          : view === 'saved' ? <Empty title="No starred songs yet" icon="star">Tap the star next to a song you like. Starred songs are kept for good.</Empty>
          : <Empty title="Nothing heard yet" icon="music">Play a station: most of them send the name of each song, and it lands here.</Empty>
      ) : (
        <div className="stack">
          {groups.map(g => (
            <section key={g.day} className="card hd-day">
              <h3 className="hd-day-title">{dayTitle(g.day)}</h3>
              <ul className="hd-list">
                {g.items.map(h => (
                  <li key={h.id} className="hd-row">
                    <button type="button" className={`btn ghost icon-only sm fav ${h.saved ? 'on' : ''}`} aria-pressed={h.saved} aria-label={h.saved ? 'Unstar' : 'Star'} title={h.saved ? 'Starred — tap to unstar' : 'Star this song'} onClick={() => star(h)}>
                      <Icon name="star" size={16} />
                    </button>
                    <div className="hd-text">
                      <div className="hd-title ellipsis">{h.artist ? <><b>{h.song || h.title}</b><span className="muted"> · {h.artist}</span></> : <b>{h.title}</b>}</div>
                      <div className="muted small ellipsis">{h.source}{h.channel_id ? ' · your channel' : ''}</div>
                    </div>
                    <span className="muted small tnum hd-time">{fmtISOTime(h.heard_at)}</span>
                    <span className="hd-menu">
                      {busyId === h.id ? <span className="btn ghost icon-only sm" aria-hidden><Spinner size={13} /></span> : (
                        <MenuButton label="More" align="right" width={240} className="btn ghost icon-only sm" items={[
                          { label: 'Is it in my music?', icon: 'music', onSelect: () => inLibrary(h) },
                          { label: 'Add to my playlist', icon: 'plus', onSelect: () => toPlaylist(h) },
                          { label: 'Search the web', icon: 'globe', onSelect: () => window.open(`https://duckduckgo.com/?q=${encodeURIComponent(h.title)}`, '_blank', 'noopener') },
                          { label: 'Copy the title', icon: 'copy', onSelect: async () => toast(await copyText(h.title) ? 'Copied' : 'Could not copy') },
                          'sep',
                          { label: 'Remove', icon: 'trash', danger: true, onSelect: () => remove(h) },
                        ]}><Icon name="more" size={16} /></MenuButton>
                      )}
                    </span>
                  </li>
                ))}
              </ul>
            </section>
          ))}
          {more && <div className="row center"><button type="button" className="btn" onClick={showMore} disabled={loadingMore}>{loadingMore ? <Spinner size={14} /> : null}Show more</button></div>}
        </div>
      )}
    </div>
  )
}
