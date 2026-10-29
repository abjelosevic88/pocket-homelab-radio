// Add a station you know by its address. The server opens it first, so a
// typo or a dead station shows at once; it can be kept anyway.
import { useState } from 'react'
import { ApiError, api, V1 } from '../lib/api'
import type { Station } from '../lib/types'
import { Icon } from './icons'
import { Field, Modal, Spinner, errMsg } from './ui'

export function AddStationModal({ onClose, onSaved }: { onClose: () => void; onSaved: (s: Station) => void }) {
  const [url, setUrl] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [probeFailed, setProbeFailed] = useState(false)

  const save = async (force = false) => {
    const u = url.trim()
    if (!/^https?:\/\/\S+/i.test(u)) { setErr('The address starts with http:// or https://'); return }
    setBusy(true)
    setErr('')
    try {
      onSaved(await api.post<Station>(`${V1}/stations`, { url: u, name: name.trim(), favorite: true, force }))
    } catch (e) {
      if (e instanceof ApiError && e.data.probe_failed) setProbeFailed(true)
      setErr(errMsg(e))
    } finally { setBusy(false) }
  }

  return (
    <Modal title="Add a station" onClose={onClose} footer={<>
      {busy && <span className="muted small sts-busy"><Spinner size={13} />Checking that it plays…</span>}
      <span className="spacer" />
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      {probeFailed && <button type="button" className="btn" disabled={busy} onClick={() => save(true)}>Keep it anyway</button>}
      <button type="button" className="btn primary" disabled={busy || !url.trim()} onClick={() => save(false)}>Add</button>
    </>}>
      <form className="stack" onSubmit={e => { e.preventDefault(); save(false) }}>
        <Field label="Stream address" help="Most stations list it on their website as “listen”, “stream” or a .pls / .m3u link.">
          <input value={url} onChange={e => { setUrl(e.target.value); setProbeFailed(false); setErr('') }} placeholder="https://stream.example.com/live.mp3" inputMode="url" spellCheck={false} autoFocus />
        </Field>
        <Field label="Name" help="Leave it empty and the station's own name is used.">
          <input value={name} onChange={e => setName(e.target.value)} placeholder="Optional" />
        </Field>
        {err && <div className="callout danger"><Icon name="alert" size={15} /><div>{err}{probeFailed && ' If it plays at other times, keep it anyway.'}</div></div>}
        <button type="submit" hidden />
      </form>
    </Modal>
  )
}
