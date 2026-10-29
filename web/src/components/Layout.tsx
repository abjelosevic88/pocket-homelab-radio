// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Dashboard } from '../lib/types'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { AddStationModal } from './AddStationModal'
import { ImportModal } from './ImportModal'
import { ChannelEditor } from './ChannelEditor'
import { PlayerBar } from './PlayerBar'
import { usePlayer } from '../lib/player'
import { Avatar, type Anchor, type MenuItem, Menu, MenuButton, modalOpen, rectOf, useIsMobile, useMediaQuery } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'pra-sidebar'

/** Something was added or changed from the shell (or a page): the counts and the pages reload. */
export const announceChange = () => window.dispatchEvent(new CustomEvent('pra:changed'))
export function useOnChange(cb: () => void) {
  const ref = useRef(cb)
  ref.current = cb
  useEffect(() => {
    const h = () => ref.current()
    window.addEventListener('pra:changed', h)
    return () => window.removeEventListener('pra:changed', h)
  }, [])
}

/** The page's own name for the top bar breadcrumb. */
const CrumbCtx = createContext<(t: string) => void>(() => {})
export function useCrumb(title: string | undefined) {
  const set = useContext(CrumbCtx)
  useEffect(() => { set(title || ''); return () => set('') }, [title, set])
}

/** The dashboard (favourites, channels, listening today) for every page that wants it. */
const CountsCtx = createContext<Dashboard | null>(null)
export const useCounts = () => useContext(CountsCtx)

export type CreateKind = 'station' | 'channel' | 'import' | 'find'

/** Open a create action from anywhere (a page's button). */
export const openCreate = (k: CreateKind) => window.dispatchEvent(new CustomEvent('pra:create', { detail: { kind: k } }))

/** The add menu: the sidebar's "Add" button and the phone's + button. */
export function createItems(onCreate: (k: CreateKind) => void): MenuItem[] {
  return [
    { label: 'Find a station…', icon: 'search', hint: 'F', onSelect: () => onCreate('find') },
    { label: 'Add a station by its address…', icon: 'link', hint: 'N', onSelect: () => onCreate('station') },
    { label: 'Make a channel from your music…', icon: 'radio', hint: 'M', onSelect: () => onCreate('channel') },
    'sep',
    { label: 'Import stations (.m3u, .pls)…', icon: 'upload', onSelect: () => onCreate('import') },
  ]
}

export default function Layout() {
  const navigate = useNavigate()
  const loc = useLocation()

  const [sp, setSp] = useSearchParams()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])

  const [crumb, setCrumbRaw] = useState('')
  const setCrumb = useCallback((t: string) => setCrumbRaw(t), [])

  const [counts, setCounts] = useState<Dashboard | null>(null)
  const load = useCallback(() => { api.get<Dashboard>(`${V1}/dashboard`).then(setCounts).catch(() => {}) }, [])
  useEffect(() => { load() }, [loc.pathname, load])
  useOnChange(load)

  const player = usePlayer()
  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [modal, setModal] = useState<{ kind: 'station' } | { kind: 'channel' } | { kind: 'import' } | null>(null)
  const [fabMenu, setFabMenu] = useState<Anchor | null>(null)

  // /?add=1 (the PWA shortcut) opens "add a station"
  useEffect(() => {
    if (sp.get('add')) { setModal({ kind: 'station' }); sp.delete('add'); setSp(sp, { replace: true }) }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const create = (k: CreateKind) => {
    closeDrawer()
    switch (k) {
      case 'station': setModal({ kind: 'station' }); break
      case 'channel': setModal({ kind: 'channel' }); break
      case 'import': setModal({ kind: 'import' }); break
      case 'find': navigate('/find'); break
    }
  }
  const createRef = useRef(create)
  createRef.current = create

  useEffect(() => {
    const open = () => setShortcuts(true)
    const make = (e: Event) => { const d = (e as CustomEvent<{ kind: CreateKind }>).detail; createRef.current(d.kind) }
    window.addEventListener('pra:shortcuts', open)
    window.addEventListener('pra:create', make)
    return () => { window.removeEventListener('pra:shortcuts', open); window.removeEventListener('pra:create', make) }
  }, [])

  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      const mk = (k: CreateKind) => { e.preventDefault(); createRef.current(k) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case ' ': if (!t.closest('button, a')) { e.preventDefault(); player.toggle() } break
        case 'n': mk('station'); break
        case 'm': mk('channel'); break
        case 'f': go('/find'); break
        case 'l': go('/'); break
        case 's': go('/stations'); break
        case 'c': go('/channels'); break
        case 'h': go('/heard'); break
        case 'y': go('/history'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'find', label: 'Find a station', icon: 'search', hint: 'F', keywords: 'search directory world genre country', run: () => create('find') },
    { id: 'station', label: 'Add a station by its address', icon: 'link', hint: 'N', keywords: 'new url stream m3u pls icecast', run: () => create('station') },
    { id: 'channel', label: 'Make a channel from your music', icon: 'radio', hint: 'M', keywords: 'new own library navidrome genres', run: () => create('channel') },
    { id: 'listen', label: 'Listen', icon: 'headphones', hint: 'L', keywords: 'home now playing', run: () => navigate('/') },
    { id: 'stations', label: 'Your stations', icon: 'heart', hint: 'S', keywords: 'favourites favorites saved', run: () => navigate('/stations') },
    { id: 'channels', label: 'Your channels', icon: 'radio', hint: 'C', keywords: 'own library', run: () => navigate('/channels') },
    { id: 'heard', label: 'Songs you heard', icon: 'music', hint: 'H', keywords: 'titles saved songs played', run: () => navigate('/heard') },
    { id: 'history', label: 'Listening history', icon: 'chart', hint: 'Y', keywords: 'stats minutes time', run: () => navigate('/history') },
    { id: 'import', label: 'Import stations', icon: 'upload', keywords: 'm3u pls file navidrome', run: () => create('import') },
    { id: 'export', label: 'Export your stations (.m3u)', icon: 'download', keywords: 'playlist vlc backup', run: () => { window.location.href = `${V1}/stations/export.m3u` } },
    { id: 'play', label: player.status === 'playing' ? 'Stop playing' : 'Play', icon: player.status === 'playing' ? 'stop' : 'play', hint: 'space', run: () => player.toggle() },
    { id: 'apps', label: 'Settings: connected apps', icon: 'layers', keywords: 'navidrome music library core integrations', run: () => navigate('/settings/apps') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  const closeModal = () => setModal(null)
  return (
    <CrumbCtx.Provider value={setCrumb}>
      <CountsCtx.Provider value={counts}>
        <div className={`app ${player.item ? 'with-player' : ''} ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
          {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
          <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
            <Sidebar onCreate={create} onNavigate={closeDrawer} counts={counts} />
          </div>
          <div className="main">
            <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />
            <main className="content"><div className="content-inner"><Outlet /></div></main>
            <PlayerBar />
          </div>
          {mobile && <button type="button" className="fab" aria-label="Add" title="Add" aria-haspopup="menu" aria-expanded={!!fabMenu} onClick={e => setFabMenu(fabMenu ? null : rectOf(e.currentTarget))}><Icon name="plus" size={24} strokeWidth={2.2} /></button>}
          {fabMenu && <Menu anchor={fabMenu} align="right" items={createItems(create)} onClose={() => setFabMenu(null)} width={250} />}
          {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} />}
          {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
          {modal?.kind === 'station' && <AddStationModal onClose={closeModal} onSaved={st => { closeModal(); announceChange(); player.play({ kind: 'station', station: st }); navigate('/stations') }} />}
          {modal?.kind === 'channel' && <ChannelEditor onClose={closeModal} onSaved={c => { closeModal(); announceChange(); navigate(`/channels/${c.id}`) }} />}
          {modal?.kind === 'import' && <ImportModal onClose={closeModal} onDone={() => { closeModal(); announceChange(); navigate('/stations') }} />}
        </div>
      </CountsCtx.Provider>
    </CrumbCtx.Provider>
  )
}

const SECTION: Record<string, [string, string]> = { '': ['Listen', '/'], find: ['Find stations', '/find'], stations: ['Your stations', '/stations'], channels: ['Your channels', '/channels'], heard: ['Songs you heard', '/heard'], history: ['History', '/history'] }

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: string }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [section, sectionTo] = SECTION[loc.pathname.split('/')[1] || ''] || ['', '/']
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pra:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {crumb ? <><button type="button" className="crumb" onClick={() => navigate(sectionTo)}>{section}</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{crumb}</span></> : section}
      </h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
