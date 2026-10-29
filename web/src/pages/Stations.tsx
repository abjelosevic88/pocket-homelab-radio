// Your stations: the favourites in your own order, with whether each still
// plays (checked once a day), edit, move, remove and export.
import { useCallback, useEffect, useState } from 'react'
import { ApiError, api, V1 } from '../lib/api'
import { ago } from '../lib/format'
import { usePlayer } from '../lib/player'
import type { Station } from '../lib/types'
import { Icon } from '../components/icons'
import { StationLogo } from '../components/StationLogo'
import { announceChange, openCreate, useCrumb, useOnChange } from '../components/Layout'
import { Confirm, Empty, Field, Loading, MenuButton, type MenuItem, Modal, Spinner, download, errMsg, useIsMobile, useToast } from '../components/ui'

export default function StationsPage() {
  useCrumb(undefined)
  const p = usePlayer()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const [list, setList] = useState<Station[] | null>(null)
  const [q, setQ] = useState('')
  const [edit, setEdit] = useState<Station | null>(null)
  const [del, setDel] = useState<Station | null>(null)
  const [checking, setChecking] = useState<number | null>(null)

  const load = useCallback(() => { api.get<Station[]>(`${V1}/stations`).then(setList).catch(e => { setList([]); toast(errMsg(e), 'err') }) }, [toast])
  useEffect(() => { load() }, [load])
  useOnChange(load)

  const changed = () => { load(); announceChange() }

  const move = async (s: Station, dir: -1 | 1) => {
    if (!list) return
    const i = list.findIndex(x => x.id === s.id)
    const j = i + dir
    if (i < 0 || j < 0 || j >= list.length) return
    const next = [...list]
    ;[next[i], next[j]] = [next[j], next[i]]
    setList(next)
    try { await api.post(`${V1}/stations/reorder`, { ids: next.map(x => x.id) }); announceChange() } catch (e) { toast(errMsg(e), 'err'); load() }
  }
  const unfavourite = async (s: Station) => {
    try {
      await api.put(`${V1}/stations/${s.id}`, { favorite: false })
      toast(`${s.name} is no longer in your stations`, 'ok', { label: 'Undo', run: async () => { await api.put(`${V1}/stations/${s.id}`, { favorite: true }).catch(() => {}); changed() } })
      changed()
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const remove = async (s: Station) => {
    setDel(null)
    try { await api.del(`${V1}/stations/${s.id}`); if (p.isCurrent('station', s.id)) p.stop(); toast(`${s.name} deleted`, 'ok'); changed() } catch (e) { toast(errMsg(e), 'err') }
  }
  const check = async (s: Station) => {
    setChecking(s.id)
    try {
      const r = await api.post<{ ok: boolean; note: string; station: Station }>(`${V1}/stations/${s.id}/check`)
      setList(l => l && l.map(x => (x.id === s.id ? r.station : x)))
      toast(r.ok ? `${s.name} plays again` : `${s.name} still does not play${r.note ? ': ' + r.note : ''}`, r.ok ? 'ok' : 'err')
      if (r.ok) announceChange()
    } catch (e) { toast(errMsg(e), 'err') } finally { setChecking(null) }
  }

  if (!list) return <Loading />

  const t = q.trim().toLowerCase()
  const shown = t ? list.filter(s => `${s.name} ${s.tags} ${s.notes}`.toLowerCase().includes(t)) : list
  const failing = list.filter(s => s.health === 'failing').length

  return (
    <div className="sts">
      <div className="sts-bar">
        <div className="fd-search sts-filter">
          <Icon name="search" size={16} />
          <input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter your stations" aria-label="Filter your stations" />
        </div>
        <div className="row sts-actions">
          <button type="button" className="btn" onClick={() => openCreate('station')}><Icon name="link" size={15} />{mobile ? 'Add' : 'Add by address'}</button>
          <button type="button" className="btn" onClick={() => openCreate('import')}><Icon name="upload" size={15} />Import</button>
          <button type="button" className="btn" disabled={list.length === 0} onClick={() => download(`${V1}/stations/export.m3u`)} title="A playlist file for VLC and other radio apps"><Icon name="download" size={15} />{mobile ? '.m3u' : 'Export .m3u'}</button>
        </div>
      </div>

      {failing > 0 && (
        <div className="callout warn"><Icon name="alert" size={15} /><div>
          {failing === 1 ? 'One station' : `${failing} stations`} did not play for a few days. Stations sometimes move to a new address: try again, find it again under <button type="button" className="link-btn" onClick={() => openCreate('find')}>Find stations</button>, or remove it.
        </div></div>
      )}

      {list.length === 0 ? (
        <Empty title="No stations yet" icon="heart">
          Tap the heart next to a station to keep it here. <button type="button" className="link-btn" onClick={() => openCreate('find')}>Find a station</button>
        </Empty>
      ) : shown.length === 0 ? (
        <Empty title="Nothing matches" icon="search">No station has “{q}” in its name, genres or notes.</Empty>
      ) : (
        <div className="card sts-list">
          {shown.map(s => {
            const on = p.isCurrent('station', s.id)
            const idx = list.findIndex(x => x.id === s.id)
            const items: MenuItem[] = [
              { label: 'Edit…', icon: 'edit', onSelect: () => setEdit(s) },
              ...(t ? [] : [
                { label: 'Move up', icon: 'chevron-up', disabled: idx === 0, onSelect: () => move(s, -1) },
                { label: 'Move down', icon: 'chevron-down', disabled: idx === list.length - 1, onSelect: () => move(s, 1) },
              ]),
              ...(s.homepage ? [{ label: 'Open its website', icon: 'external', onSelect: () => window.open(s.homepage, '_blank', 'noopener') }] : []),
              { label: 'Check that it plays', icon: 'refresh', onSelect: () => check(s) },
              'sep',
              { label: 'Remove from your stations', icon: 'heart', onSelect: () => unfavourite(s) },
              { label: 'Delete', icon: 'trash', danger: true, onSelect: () => setDel(s) },
            ]
            const meta = [s.tags, [s.codec, s.bitrate ? `${s.bitrate} kbit/s` : ''].filter(Boolean).join(' '), s.play_count ? `played ${s.play_count}×` : ''].filter(Boolean).join(' · ')
            return (
              <div key={s.id} className={`sts-row ${on ? 'on' : ''}`}>
                <StationLogo station={s} size={42} />
                <div className="grow">
                  <div className="sts-name ellipsis">{s.name}</div>
                  {s.health === 'failing' ? (
                    <div className="sts-fail ellipsis" title={s.health_note}>
                      <Icon name="alert" size={13} />Not working{s.checked_at ? ` (last tried ${ago(s.checked_at)})` : ''}{s.health_note ? `: ${s.health_note}` : ''}
                    </div>
                  ) : <div className="sts-meta ellipsis">{meta || 'Radio'}</div>}
                </div>
                {s.health === 'failing' && (
                  <button type="button" className="btn sm hide-phone" disabled={checking === s.id} onClick={() => check(s)}>
                    {checking === s.id ? <Spinner size={13} /> : <Icon name="refresh" size={13} />}Try again
                  </button>
                )}
                <MenuButton label={`More for ${s.name}`} align="right" width={240} items={items}><Icon name="more" size={17} /></MenuButton>
                <button type="button" className={`btn icon-only ${on ? 'primary' : ''}`} onClick={() => (on ? p.stop() : p.play({ kind: 'station', station: s }))} aria-label={on ? 'Stop' : `Play ${s.name}`} title={on ? 'Stop' : 'Play'}>
                  <Icon name={on ? 'stop' : 'play'} size={15} strokeWidth={2} />
                </button>
              </div>
            )
          })}
        </div>
      )}

      {edit && <EditStationModal station={edit} onClose={() => setEdit(null)} onSaved={s => { setEdit(null); setList(l => l && l.map(x => (x.id === s.id ? s : x))); announceChange(); toast('Saved', 'ok') }} />}
      {del && <Confirm title={`Delete ${del.name}?`} message="It goes from your stations. Your listening history and the songs you heard on it stay." confirmLabel="Delete" onConfirm={() => remove(del)} onCancel={() => setDel(null)} />}
    </div>
  )
}

/** Name, address, logo, website, genres and notes of a station. */
function EditStationModal({ station, onClose, onSaved }: { station: Station; onClose: () => void; onSaved: (s: Station) => void }) {
  const [f, setF] = useState({ name: station.name, url: station.url, logo_url: station.logo_url, homepage: station.homepage, tags: station.tags, notes: station.notes })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [probeFailed, setProbeFailed] = useState(false)
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => { setF({ ...f, [k]: e.target.value }); if (k === 'url') setProbeFailed(false) }
  const save = async (force = false) => {
    if (!f.name.trim()) { setErr('The station needs a name.'); return }
    setBusy(true)
    setErr('')
    try {
      onSaved(await api.put<Station>(`${V1}/stations/${station.id}`, { ...f, force }))
    } catch (e) {
      if (e instanceof ApiError && e.data.probe_failed) setProbeFailed(true)
      setErr(errMsg(e))
    } finally { setBusy(false) }
  }
  const urlChanged = f.url.trim() !== station.url
  return (
    <Modal title="Edit station" onClose={onClose} footer={<>
      {busy && <span className="muted small sts-busy"><Spinner size={13} />{urlChanged ? 'Checking that it plays…' : 'Saving…'}</span>}
      <span className="spacer" />
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      {probeFailed && <button type="button" className="btn" disabled={busy} onClick={() => save(true)}>Save anyway</button>}
      <button type="button" className="btn primary" disabled={busy} onClick={() => save(false)}>Save</button>
    </>}>
      <form className="stack" onSubmit={e => { e.preventDefault(); save(false) }}>
        <Field label="Name"><input value={f.name} onChange={set('name')} autoFocus /></Field>
        <Field label="Stream address" help="The address the sound comes from. A .pls or .m3u address works too."><input value={f.url} onChange={set('url')} inputMode="url" spellCheck={false} /></Field>
        <Field label="Logo address" help="A picture on the web; leave empty for the initials."><input value={f.logo_url} onChange={set('logo_url')} inputMode="url" spellCheck={false} placeholder="https://…" /></Field>
        <Field label="Website"><input value={f.homepage} onChange={set('homepage')} inputMode="url" spellCheck={false} placeholder="https://…" /></Field>
        <Field label="Genres" help="Separated by commas, like “jazz, soul”."><input value={f.tags} onChange={set('tags')} /></Field>
        <Field label="Notes"><textarea value={f.notes} onChange={set('notes')} rows={3} /></Field>
        {err && <div className="callout danger"><Icon name="alert" size={15} /><div>{err}{probeFailed && ' You can save it anyway and try again later.'}</div></div>}
        <button type="submit" hidden />
      </form>
    </Modal>
  )
}
