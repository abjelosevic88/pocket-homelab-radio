// Make or change a channel: a station of your own from your music library.
// The rules on the left, what they would play on the right (asked again
// ~0.6 s after the last change; the panel keeps its size while it loads).
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Channel, ChannelPreview, ChannelRules, LibraryInfo } from '../lib/types'
import { Icon } from './icons'
import { Select } from './Select'
import { ChannelLogo, toneOf } from './StationLogo'
import { Field, Modal, Spinner, Switch, useDebounce, useToast } from './ui'

const COLORS = ['#0b7285', '#c2410c', '#7c3aed', '#be123c', '#0369a1', '#4d7c0f', '#a21caf', '#b45309', '#0f766e', '#4338ca']
const AVOID = [2, 6, 12, 24, 48, 168].map(h => ({ value: String(h), label: h === 168 ? 'a week' : h === 48 ? '2 days' : h === 24 ? 'a day' : `${h} hours` }))
const GAP = [0, 2, 3, 5, 8].map(n => ({ value: String(n), label: n === 0 ? 'any time' : `${n} other songs` }))
const QUALITY = [96, 128, 160, 192, 256, 320].map(b => ({ value: String(b), label: `${b} kbit/s`, hint: b <= 128 ? 'saves mobile data' : b >= 256 ? 'best sound' : undefined }))

export const emptyRules = (): ChannelRules => ({ genres: [], exclude_genres: [], artists: [], exclude_artists: [], playlists: [], year_from: 0, year_to: 0, starred_only: false, avoid_hours: 24, artist_gap: 3 })

function listText(l: string[], n = 3) {
  if (l.length <= n) return l.join(', ')
  return `${l.slice(0, n).join(', ')} +${l.length - n}`
}

/** "Jazz, Soul · 1960–1979 · only starred · no repeats for 24 h" */
export function rulesSummary(r: ChannelRules, playlistNames?: Record<string, string>): string {
  const parts: string[] = []
  if (r.playlists.length) parts.push(r.playlists.length === 1 && playlistNames?.[r.playlists[0]] ? `playlist ${playlistNames[r.playlists[0]]}` : `${r.playlists.length} playlist${r.playlists.length === 1 ? '' : 's'}`)
  if (r.artists.length) parts.push(listText(r.artists, 2))
  if (r.genres.length) parts.push(listText(r.genres))
  if (!r.playlists.length && !r.artists.length && !r.genres.length) parts.push(r.starred_only ? 'your starred songs' : 'your whole library')
  if (r.year_from && r.year_to) parts.push(`${r.year_from}–${r.year_to}`)
  else if (r.year_from) parts.push(`from ${r.year_from}`)
  else if (r.year_to) parts.push(`up to ${r.year_to}`)
  if (r.starred_only && (r.playlists.length || r.artists.length || r.genres.length)) parts.push('only starred')
  if (r.exclude_genres.length) parts.push(`no ${listText(r.exclude_genres, 2)}`)
  if (r.avoid_hours) parts.push(`no repeats for ${r.avoid_hours >= 48 ? `${Math.round(r.avoid_hours / 24)} days` : `${r.avoid_hours} h`}`)
  return parts.join(' · ')
}

/** Chips of what is picked, a search box and the matching choices below it. */
function Picker({ picked, onChange, choices, placeholder, empty, onSearch, loading }: {
  picked: string[]; onChange: (v: string[]) => void; choices: { value: string; label: string; hint?: string }[]
  placeholder: string; empty?: string; onSearch?: (q: string) => void; loading?: boolean
}) {
  const [q, setQ] = useState('')
  useEffect(() => { onSearch?.(q) }, [q]) // eslint-disable-line react-hooks/exhaustive-deps
  const shown = useMemo(() => {
    const s = q.trim().toLowerCase()
    const rest = choices.filter(c => !picked.includes(c.value))
    return (onSearch || !s ? rest : rest.filter(c => c.label.toLowerCase().includes(s))).slice(0, s ? 12 : 10)
  }, [q, choices, picked, onSearch])
  const label = (v: string) => choices.find(c => c.value === v)?.label || v
  return (
    <div className="ch-picker">
      {picked.length > 0 && (
        <div className="chips">
          {picked.map(v => (
            <button key={v} type="button" className="chip active" title={`Remove ${label(v)}`} onClick={() => onChange(picked.filter(x => x !== v))}>
              {label(v)}<Icon name="x" size={12} />
            </button>
          ))}
        </div>
      )}
      <div className="ch-picker-search">
        <Icon name="search" size={14} />
        <input value={q} onChange={e => setQ(e.target.value)} placeholder={placeholder} aria-label={placeholder}
          onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); if (shown[0]) { onChange([...picked, shown[0].value]); setQ('') } } }} />
        {loading && <Spinner size={12} />}
      </div>
      <div className="chips ch-picker-choices">
        {shown.map(c => (
          <button key={c.value} type="button" className="chip" onClick={() => { onChange([...picked, c.value]); setQ('') }}>
            <Icon name="plus" size={12} />{c.label}{c.hint && <span className="chip-n">{c.hint}</span>}
          </button>
        ))}
        {shown.length === 0 && <span className="muted small">{q.trim() ? 'Nothing matches.' : empty || ''}</span>}
      </div>
    </div>
  )
}

export function ChannelEditor({ channel, onClose, onSaved }: { channel?: Channel; onClose: () => void; onSaved: (c: Channel) => void }) {
  const { settings } = useApp()
  const { toast } = useToast()
  const [name, setName] = useState(channel?.name || '')
  const [description, setDescription] = useState(channel?.description || '')
  const [color, setColor] = useState(channel?.color || '')
  const [bitrate, setBitrate] = useState(channel?.bitrate || settings?.channel_bitrate || 192)
  const [rules, setRules] = useState<ChannelRules>(channel?.rules ? { ...emptyRules(), ...channel.rules } : emptyRules())
  const [lib, setLib] = useState<LibraryInfo | null>(null)
  const [text, setText] = useState('')
  const [describing, setDescribing] = useState(false)
  const [describeErr, setDescribeErr] = useState<{ msg: string; ai?: boolean } | null>(null)
  const [preview, setPreview] = useState<ChannelPreview | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const [artistQ, setArtistQ] = useState('')
  const [artistHits, setArtistHits] = useState<string[]>([])
  const [artistBusy, setArtistBusy] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => { api.get<LibraryInfo>(`${V1}/library`).then(setLib).catch(e => setLib({ ok: false, error: (e as Error).message, genres: [], playlists: [] })) }, [])

  const dq = useDebounce(artistQ, 300)
  useEffect(() => {
    if (!dq.trim()) { setArtistHits([]); return }
    let live = true
    setArtistBusy(true)
    api.get<{ ok: boolean; artists: { name: string }[] }>(`${V1}/library/artists?q=${encodeURIComponent(dq.trim())}`)
      .then(r => { if (live) setArtistHits((r.artists || []).map(a => a.name)) }).catch(() => {}).finally(() => { if (live) setArtistBusy(false) })
    return () => { live = false }
  }, [dq])

  const rulesKey = useDebounce(JSON.stringify(rules), 600)
  useEffect(() => {
    if (!lib || !lib.ok) return
    let live = true
    setPreviewing(true)
    api.post<ChannelPreview>(`${V1}/channels/preview`, { rules: JSON.parse(rulesKey) })
      .then(r => { if (live) setPreview(r) }).catch(e => { if (live) setPreview({ ok: false, error: (e as Error).message, songs: [], count: 0, estimate: false }) })
      .finally(() => { if (live) setPreviewing(false) })
    return () => { live = false }
  }, [rulesKey, lib])

  const set = (p: Partial<ChannelRules>) => setRules(r => ({ ...r, ...p }))
  const genreChoices = (lib?.genres || []).map(g => ({ value: g.value, label: g.value, hint: g.songCount.toLocaleString() }))
  const playlistNames = Object.fromEntries((lib?.playlists || []).map(p => [p.id, p.name]))

  const describe = async () => {
    if (!text.trim()) return
    setDescribing(true); setDescribeErr(null)
    try {
      const r = await api.post<{ ok: boolean; error?: string; not_configured?: boolean; name?: string; description?: string; rules?: ChannelRules }>(`${V1}/channels/describe`, { text })
      if (!r.ok || !r.rules) { setDescribeErr({ msg: r.error || 'That did not work.', ai: r.not_configured }); return }
      setRules({ ...emptyRules(), ...r.rules })
      if (r.name && (!channel || !name.trim())) setName(r.name)
      if (r.description && (!channel || !description.trim())) setDescription(r.description)
    } catch (e) { setDescribeErr({ msg: (e as Error).message }) } finally { setDescribing(false) }
  }

  const save = async () => {
    if (!name.trim()) { toast('Give the channel a name', 'err'); return }
    setSaving(true)
    const body = { name: name.trim(), description: description.trim(), color, bitrate, rules }
    try {
      const c = channel ? await api.put<Channel>(`${V1}/channels/${channel.id}`, body) : await api.post<Channel>(`${V1}/channels`, body)
      toast(channel ? 'Channel saved' : `${c.name} is in your channels`)
      onSaved(c)
    } catch (e) { toast((e as Error).message, 'err') } finally { setSaving(false) }
  }

  const notConnected = !!lib && !lib.ok
  const year = (v: string) => { const n = parseInt(v.replace(/\D/g, ''), 10); return Number.isFinite(n) && n > 0 ? Math.min(n, 2999) : 0 }

  return (
    <Modal title={channel ? `Change ${channel.name}` : 'Make a channel'} onClose={onClose} size="lg"
      footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" onClick={save} disabled={saving || !name.trim()}>{channel ? 'Save' : 'Make the channel'}</button></>}>
      {notConnected && (
        <div className="callout warn mb"><Icon name="alert" size={15} /><div>{lib?.not_connected ? 'Your music library is not connected yet' : 'Your music library did not answer'}: {lib?.error} <Link to="/settings/apps">Connected apps</Link></div></div>
      )}
      <div className="ch-editor">
        <div className="stack">
          <div className="ch-describe">
            <label className="label" htmlFor="ch-describe">Describe it in your own words</label>
            <div className="ch-describe-row">
              <input id="ch-describe" value={text} onChange={e => setText(e.target.value)} placeholder="Calm jazz and soul from the sixties for Sunday mornings"
                onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); describe() } }} disabled={notConnected} />
              <button type="button" className="btn" onClick={describe} disabled={describing || !text.trim() || notConnected}>
                {describing ? <Spinner size={14} /> : <Icon name="sparkles" size={15} />}Fill in
              </button>
            </div>
            {describeErr
              ? <p className="small bad">{describeErr.msg}{describeErr.ai && <> — <Link to="/settings/ai">Settings → AI</Link></>}</p>
              : <p className="muted small">AI picks genres and years from what your library has. You can change everything after.</p>}
          </div>

          <div className="ch-name-row">
            <ChannelLogo channel={{ name: name || 'Channel', color }} size={44} />
            <Field label="Name" className="grow"><input value={name} onChange={e => setName(e.target.value)} placeholder="Sunday Morning" maxLength={80} /></Field>
          </div>
          <Field label="What it plays (optional)"><input value={description} onChange={e => setDescription(e.target.value)} placeholder="Slow jazz and soul for a late breakfast." maxLength={500} /></Field>
          <Field group label="Colour">
            <div className="ch-swatches" role="radiogroup" aria-label="Colour">
              <button type="button" role="radio" aria-checked={!color} className={`ch-swatch ${!color ? 'on' : ''}`} style={{ background: toneOf(name || 'Channel') }} title="Picked from the name" onClick={() => setColor('')} />
              {COLORS.map(c => <button key={c} type="button" role="radio" aria-checked={color === c} className={`ch-swatch ${color === c ? 'on' : ''}`} style={{ background: c }} title={c} onClick={() => setColor(c)} />)}
            </div>
          </Field>

          <h4 className="ch-h">Songs from</h4>
          <Field group label="Genres" help="Any of these. Leave empty for every genre.">
            <Picker picked={rules.genres} onChange={v => set({ genres: v })} choices={genreChoices} placeholder="Find a genre" empty={lib ? (lib.ok ? 'Your library has no genres.' : '') : 'Loading genres…'} />
          </Field>
          <Field group label="Artists" help="Only these artists. Leave empty for anyone.">
            <Picker picked={rules.artists} onChange={v => set({ artists: v })} choices={artistHits.map(a => ({ value: a, label: a }))} placeholder="Find an artist" empty="Type a name to find artists." onSearch={setArtistQ} loading={artistBusy} />
          </Field>
          {(lib?.playlists.length || 0) > 0 && (
            <Field group label="Playlists" help="Songs of these playlists, together with the artists above.">
              <Picker picked={rules.playlists} onChange={v => set({ playlists: v })} choices={(lib?.playlists || []).map(p => ({ value: p.id, label: p.name, hint: String(p.songCount) }))} placeholder="Find a playlist" />
            </Field>
          )}
          <div className="two-col tight">
            <Field label="From the year"><input inputMode="numeric" value={rules.year_from || ''} onChange={e => set({ year_from: year(e.target.value) })} placeholder="any" /></Field>
            <Field label="To the year"><input inputMode="numeric" value={rules.year_to || ''} onChange={e => set({ year_to: year(e.target.value) })} placeholder="any" /></Field>
          </div>
          <div className="ch-switch-row">
            <div className="grow"><div className="setting-title">Only songs you starred</div><div className="setting-help">Songs with a heart in your library.</div></div>
            <Switch checked={rules.starred_only} onChange={v => set({ starred_only: v })} label="Only songs you starred" />
          </div>
          <Field group label="Leave out genres">
            <Picker picked={rules.exclude_genres} onChange={v => set({ exclude_genres: v })} choices={genreChoices} placeholder="Find a genre to leave out" />
          </Field>

          <h4 className="ch-h">How it plays</h4>
          <div className="two-col tight">
            <Field group label="No song again within"><Select ariaLabel="No song again within" value={String(rules.avoid_hours)} onChange={v => set({ avoid_hours: Number(v) })} options={AVOID.some(a => a.value === String(rules.avoid_hours)) ? AVOID : [...AVOID, { value: String(rules.avoid_hours), label: `${rules.avoid_hours} hours` }]} /></Field>
            <Field group label="Same artist again after"><Select ariaLabel="Same artist again after" value={String(rules.artist_gap)} onChange={v => set({ artist_gap: Number(v) })} options={GAP.some(a => a.value === String(rules.artist_gap)) ? GAP : [...GAP, { value: String(rules.artist_gap), label: `${rules.artist_gap} other songs` }]} /></Field>
          </div>
          <Field group label="Sound quality" help="Higher sounds better and uses more data on the phone.">
            <Select ariaLabel="Sound quality" value={String(bitrate)} onChange={v => setBitrate(Number(v))} options={QUALITY} />
          </Field>
        </div>

        <aside className="ch-preview" aria-label="What it would play">
          <div className="ch-preview-head">
            <h4>What it would play</h4>
            {previewing && <Spinner size={13} />}
          </div>
          <p className="ch-preview-count">
            {notConnected ? 'Connect your music library to see the songs.'
              : !preview ? 'Looking…'
              : !preview.ok ? <span className="bad">{preview.error}</span>
              : preview.count === 0 ? 'No songs match these rules.'
              : <>{preview.estimate ? 'About ' : ''}<b>{preview.count.toLocaleString()}</b> {preview.count === 1 ? 'song' : 'songs'}</>}
          </p>
          <p className="muted small">{rulesSummary(rules, playlistNames)}</p>
          <ul className="ch-preview-list">
            {(preview?.ok ? preview.songs : []).map(s => (
              <li key={s.id}>
                <img src={`${V1}/cover/${encodeURIComponent(s.cover || s.id)}?size=80`} alt="" loading="lazy" onError={e => { (e.target as HTMLImageElement).style.visibility = 'hidden' }} />
                <span className="grow"><span className="ellipsis block">{s.title}</span><span className="muted small ellipsis block">{[s.artist, s.year || ''].filter(Boolean).join(' · ')}</span></span>
              </li>
            ))}
          </ul>
          {preview?.ok && preview.songs.length > 0 && <p className="muted xs">A few at random; they change with every look.</p>}
        </aside>
      </div>
    </Modal>
  )
}
