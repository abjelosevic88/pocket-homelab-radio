// ⌘K / "/" — play one of your stations or channels, jump to a page, or run a command from the keyboard.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { api, V1 } from '../lib/api'
import { usePlayer } from '../lib/player'
import type { Channel, Station } from '../lib/types'
import { Icon } from './icons'
import { ChannelLogo, StationLogo } from './StationLogo'

export interface PaletteCommand { id: string; label: string; icon: string; hint?: string; keywords?: string; run: () => void }

export function CommandPalette({ onClose, commands }: { onClose: () => void; commands: PaletteCommand[] }) {
  const player = usePlayer()
  const [q, setQ] = useState('')
  const [hi, setHi] = useState(0)
  const [stations, setStations] = useState<Station[]>([])
  const [channels, setChannels] = useState<Channel[]>([])
  const list = useRef<HTMLDivElement>(null)
  useEffect(() => {
    api.get<Station[]>(`${V1}/stations?view=all`).then(setStations).catch(() => {})
    api.get<Channel[]>(`${V1}/channels`).then(setChannels).catch(() => {})
  }, [])

  type Row = { key: string; group: string; icon: React.ReactNode; label: React.ReactNode; sub?: React.ReactNode; hint?: string; run: () => void }
  const rows = useMemo<Row[]>(() => {
    const n = q.trim().toLowerCase()
    const words = n.split(/\s+/).filter(Boolean)
    const hit = (text: string) => words.every(w => text.toLowerCase().includes(w))
    const cmds = commands.filter(c => !n || hit(`${c.label} ${c.keywords || ''}`)).map(c => ({ key: c.id, group: 'Commands', icon: <Icon name={c.icon} size={16} />, label: c.label, hint: c.hint, run: c.run }))
    const sts = stations.filter(s => (n ? hit(`${s.name} ${s.tags}`) : s.favorite)).slice(0, n ? 12 : 6).map(s => ({ key: `s${s.id}`, group: 'Play a station',
      icon: <StationLogo station={s} size={22} />, label: s.name, sub: [s.tags, !s.favorite && 'played before'].filter(Boolean).join(' · '), run: () => player.play({ kind: 'station', station: s }) }))
    const chs = channels.filter(c => !n || hit(`${c.name} ${c.description} ${c.rules.genres.join(' ')}`)).slice(0, n ? 8 : 3).map(c => ({ key: `c${c.id}`, group: 'Play your channel',
      icon: <ChannelLogo channel={c} size={22} />, label: c.name, sub: c.description, run: () => player.play({ kind: 'channel', channel: c }) }))
    return [...sts, ...chs, ...cmds]
  }, [q, stations, channels, commands, player])
  useEffect(() => setHi(0), [q])
  useEffect(() => { list.current?.querySelector(`[data-i="${hi}"]`)?.scrollIntoView({ block: 'nearest' }) }, [hi])

  const run = (r: Row | undefined) => { if (!r) return; onClose(); r.run() }
  let last = ''
  return createPortal(
    <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="palette-box" role="dialog" aria-label="Search and commands">
        <div className="pal-input">
          <Icon name="search" size={18} />
          <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Play a station, or run a command…" aria-label="Search"
            onKeyDown={e => {
              if (e.key === 'ArrowDown') { e.preventDefault(); setHi(h => Math.min(rows.length - 1, h + 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setHi(h => Math.max(0, h - 1)) }
              else if (e.key === 'Enter') { e.preventDefault(); run(rows[hi]) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
            }} />
          <kbd>esc</kbd>
        </div>
        <div className="pal-list" ref={list} role="listbox">
          {rows.length === 0 && <div className="pal-empty">Nothing matches.</div>}
          {rows.map((r, i) => {
            const head = r.group !== last ? <div className="pal-group" key={'g' + r.group}>{r.group}</div> : null
            last = r.group
            return (
              <React.Fragment key={r.key}>
                {head}
                <div data-i={i} role="option" aria-selected={i === hi} className={`pal-row ${i === hi ? 'hi' : ''}`} onMouseEnter={() => setHi(i)} onClick={() => run(r)}>
                  <span className="pal-ico">{r.icon}</span>
                  <span className="grow"><span className="pal-label">{r.label}</span>{r.sub && <span className="pal-sub">{r.sub}</span>}</span>
                  {r.hint && <kbd>{r.hint}</kbd>}
                  {i === hi && <Icon name="chevron-right" size={14} className="muted" />}
                </div>
              </React.Fragment>
            )
          })}
        </div>
      </div>
    </div>, document.body)
}
