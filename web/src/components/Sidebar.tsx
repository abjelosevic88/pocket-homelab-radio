// Left column: the add button, sections, your first favourites to play at once — the Pocket Calendar sidebar.
import { useState } from 'react'
import { NavLink } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { usePlayer } from '../lib/player'
import type { Dashboard } from '../lib/types'
import { Icon } from './icons'
import { createItems, type CreateKind } from './Layout'
import { StationLogo } from './StationLogo'
import { type Anchor, Menu, rectOf } from './ui'

export function Sidebar({ onCreate, onNavigate, counts }: { onCreate: (k: CreateKind) => void; onNavigate: () => void; counts: Dashboard | null }) {
  const { status, settings } = useApp()
  const player = usePlayer()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const title = settings?.title || status?.title || 'Pocket Radio'
  const nav: { to: string; label: string; icon: string; end?: boolean; count?: string }[] = [
    { to: '/', label: 'Listen', icon: 'headphones', end: true },
    { to: '/find', label: 'Find stations', icon: 'globe' },
    { to: '/stations', label: 'Your stations', icon: 'heart', count: counts?.favorites.length ? String(counts.favorites.length) : undefined },
    { to: '/channels', label: 'Your channels', icon: 'radio', count: counts?.channels.length ? String(counts.channels.length) : undefined },
    { to: '/heard', label: 'Songs you heard', icon: 'music' },
    { to: '/history', label: 'History', icon: 'chart' },
  ]
  const quick = (counts?.favorites || []).slice(0, 6)
  return (
    <aside className="sidebar" aria-label="Pocket Radio">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Radio' ? title : <>Pocket <b>Radio</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
        <Icon name="plus" size={15} strokeWidth={2.2} />Add<Icon name="chevron-down" size={15} className="create-caret" />
      </button>
      {menu && <Menu anchor={menu} items={createItems(onCreate)} onClose={() => setMenu(null)} width={270} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}
            {!!n.count && <span className="sb-count">{n.count}</span>}
          </NavLink>
        ))}
      </nav>

      {quick.length > 0 && (
        <section className="sb-sec" aria-label="Play a favourite">
          <h4 className="sb-h"><span className="sb-h-btn">Play</span></h4>
          <div className="sb-quick">
            {quick.map(s => {
              const on = player.isCurrent('station', s.id)
              return (
                <button key={s.id} type="button" className={`sb-quick-item ${on ? 'on' : ''}`} title={on ? `Stop ${s.name}` : `Play ${s.name}`}
                  onClick={() => (on ? player.stop() : player.play({ kind: 'station', station: s }))}>
                  <StationLogo station={s} size={26} />
                  <span className="ellipsis">{s.name}</span>
                  <Icon name={on ? 'stop' : 'play'} size={13} className="sb-quick-ico" />
                </button>
              )
            })}
          </div>
        </section>
      )}

      <div className="sb-foot">
        <NavLink to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</NavLink>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pra:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}
