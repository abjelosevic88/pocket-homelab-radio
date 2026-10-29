// Bring stations in: a playlist file from another radio app (.m3u, .pls),
// pasted addresses, or the internet radio list kept in Navidrome. Addresses
// you already have are skipped, so importing twice adds nothing.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { Icon } from './icons'
import { Field, Modal, Spinner, Tabs, errMsg, useToast } from './ui'

type Tab = 'file' | 'navidrome'
interface Result { ok?: boolean; added: number; skipped: number; found?: number; error?: string; not_connected?: boolean }

export function ImportModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { toast } = useToast()
  const [tab, setTab] = useState<Tab>('file')
  const [text, setText] = useState('')
  const [fileName, setFileName] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [notConnected, setNotConnected] = useState(false)

  const report = (r: Result) => {
    if (r.added === 0) toast(r.skipped ? `Nothing new: you already have ${r.skipped === 1 ? 'that station' : `all ${r.skipped}`}` : 'No stations were added', 'ok')
    else toast(`${r.added === 1 ? 'One station' : `${r.added} stations`} added${r.skipped ? `, ${r.skipped} you already had` : ''}`, 'ok')
    onDone()
  }

  const pick = (f: File | undefined) => {
    if (!f) return
    if (f.size > 4 << 20) { setErr('That file is too big for a list of stations.'); return }
    setErr('')
    f.text().then(t => { setText(t); setFileName(f.name) }).catch(() => setErr('Could not read the file.'))
  }

  const importFile = async () => {
    if (!text.trim()) { setErr('Choose a file or paste the addresses first.'); return }
    setBusy(true)
    setErr('')
    try { report(await api.post<Result>(`${V1}/stations/import`, { text })) } catch (e) { setErr(errMsg(e)) } finally { setBusy(false) }
  }

  const importNavidrome = async () => {
    setBusy(true)
    setErr('')
    setNotConnected(false)
    try {
      const r = await api.post<Result>(`${V1}/stations/import/navidrome`)
      if (r.ok === false) { setErr(r.error || 'Navidrome did not answer.'); setNotConnected(!!r.not_connected); return }
      if (r.found === 0) { setErr('Navidrome has no internet radio stations saved.'); return }
      report(r)
    } catch (e) { setErr(errMsg(e)) } finally { setBusy(false) }
  }

  return (
    <Modal title="Import stations" onClose={onClose} footer={<>
      {busy && <span className="muted small sts-busy"><Spinner size={13} />Importing…</span>}
      <span className="spacer" />
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      {tab === 'file'
        ? <button type="button" className="btn primary" disabled={busy || !text.trim()} onClick={importFile}>Import</button>
        : <button type="button" className="btn primary" disabled={busy} onClick={importNavidrome}>Import from Navidrome</button>}
    </>}>
      <div className="stack">
        <Tabs<Tab> tabs={[{ id: 'file', label: 'A file', icon: 'upload' }, { id: 'navidrome', label: 'From Navidrome', icon: 'music' }]} value={tab} onChange={t => { setTab(t); setErr(''); setNotConnected(false) }} />
        {tab === 'file' ? (
          <>
            <p className="muted small im-lead">Most radio apps (VLC, foobar2000, phone radio apps) can save your stations as a .m3u or .pls file. Choose it here, or paste stream addresses, one per line.</p>
            <label className="im-drop">
              <input type="file" accept=".m3u,.m3u8,.pls,.txt,audio/x-mpegurl,audio/x-scpls,text/plain" onChange={e => pick(e.target.files?.[0])} />
              <Icon name="upload" size={18} />
              <span className="ellipsis">{fileName ? fileName : 'Choose a file…'}</span>
            </label>
            <Field label="Or paste">
              <textarea value={text} onChange={e => { setText(e.target.value); setFileName('') }} rows={6} spellCheck={false} placeholder={'#EXTM3U\n#EXTINF:-1,My station\nhttps://stream.example.com/live.mp3'} />
            </Field>
          </>
        ) : (
          <p className="muted small im-lead">Navidrome can keep a list of internet radio stations. They are copied into your stations here; Navidrome keeps its own list too.</p>
        )}
        {err && (
          <div className="callout danger"><Icon name="alert" size={15} /><div>
            {err}{notConnected && <> <Link to="/settings/apps" onClick={onClose}>Connect your music library</Link>.</>}
          </div></div>
        )}
      </div>
    </Modal>
  )
}
