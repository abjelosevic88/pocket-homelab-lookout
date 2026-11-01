// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Dashboard, TagCount } from '../lib/types'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { Avatar, type Anchor, type MenuItem, Menu, MenuButton, modalOpen, rectOf, useIsMobile, useMediaQuery } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'plk-sidebar'

/** Something was added or changed from the shell (or a page): the sidebar counts and the pages reload. */
export const announceChange = () => window.dispatchEvent(new CustomEvent('plk:changed'))
export function useOnChange(cb: () => void) {
  const ref = useRef(cb)
  ref.current = cb
  useEffect(() => {
    const h = () => ref.current()
    window.addEventListener('plk:changed', h)
    return () => window.removeEventListener('plk:changed', h)
  }, [])
}

/** The page's own name for the top bar breadcrumb (a watch's name). */
const CrumbCtx = createContext<(t: string) => void>(() => {})
export function useCrumb(title: string | undefined) {
  const set = useContext(CrumbCtx)
  useEffect(() => { set(title || ''); return () => set('') }, [title, set])
}

/** The person's tags, for every page that needs them (filters, pickers, the sidebar). */
const TagsCtx = createContext<{ tags: TagCount[]; dash: Dashboard | null; reload: () => void }>({ tags: [], dash: null, reload: () => {} })
export const useShell = () => useContext(TagsCtx)

/** The create menu: the sidebar's "New" button and the phone's + button. */
export function createItems(navigate: (to: string) => void): MenuItem[] {
  return [
    { label: 'Watch a page', icon: 'plus', hint: 'N', onSelect: () => navigate('/add') },
    { label: 'Import a list of pages', icon: 'upload', onSelect: () => navigate('/settings/data') },
  ]
}

export default function Layout() {
  const navigate = useNavigate()
  const loc = useLocation()
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

  const [dash, setDash] = useState<Dashboard | null>(null)
  const [tags, setTags] = useState<TagCount[]>([])
  const load = useCallback(() => {
    api.get<Dashboard>(`${V1}/dashboard`).then(setDash).catch(() => {})
    api.get<TagCount[]>(`${V1}/tags`).then(setTags).catch(() => {})
  }, [])
  useEffect(() => { load() }, [loc.pathname, load])
  useOnChange(load)
  // the counts follow checks that finish in the background
  useEffect(() => { const t = window.setInterval(load, 60_000); return () => window.clearInterval(t) }, [load])

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [fabMenu, setFabMenu] = useState<Anchor | null>(null)

  useEffect(() => {
    const open = () => setShortcuts(true)
    window.addEventListener('plk:shortcuts', open)
    return () => window.removeEventListener('plk:shortcuts', open)
  }, [])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'n': go('/add'); break
        case 'h': case 'w': go('/'); break
        case 'c': go('/changes'); break
        case 'f': go('/?state=failing'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'add', label: 'Watch a page', icon: 'plus', hint: 'N', keywords: 'new add url monitor', run: () => navigate('/add') },
    { id: 'home', label: 'Watches', icon: 'eye', hint: 'H', keywords: 'pages list', run: () => navigate('/') },
    { id: 'changes', label: 'Changes', icon: 'activity', hint: 'C', keywords: 'diff updates new inbox', run: () => navigate('/changes') },
    { id: 'failing', label: 'Failing watches', icon: 'alert', hint: 'F', keywords: 'error broken', run: () => navigate('/?state=failing') },
    { id: 'paused', label: 'Paused watches', icon: 'pause', run: () => navigate('/?state=paused') },
    { id: 'checkall', label: 'Check all pages now', icon: 'refresh', keywords: 'recheck refresh', run: () => { api.post(`${V1}/watches/bulk`, { action: 'check' }).then(() => setTimeout(announceChange, 1500)).catch(() => {}) } },
    { id: 'import', label: 'Import a list of pages', icon: 'upload', keywords: 'changedetection urls backup', run: () => navigate('/settings/data') },
    { id: 'feed', label: 'Settings: RSS feed', icon: 'rss', keywords: 'reader feed', run: () => navigate('/settings/notifications') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'ai', label: 'Settings: AI', icon: 'sparkles', keywords: 'pocket ai openai summaries', run: () => navigate('/settings/ai') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  return (
    <CrumbCtx.Provider value={setCrumb}>
      <TagsCtx.Provider value={{ tags, dash, reload: load }}>
        <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
          {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
          <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
            <Sidebar onNavigate={closeDrawer} dash={dash} tags={tags} />
          </div>
          <div className="main">
            <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />
            <main className="content"><div className="content-inner"><Outlet /></div></main>
          </div>
          {mobile && !/^\/add$|\/edit$/.test(loc.pathname) && <button type="button" className="fab" aria-label="New" title="New" aria-haspopup="menu" aria-expanded={!!fabMenu} onClick={e => setFabMenu(fabMenu ? null : rectOf(e.currentTarget))}><Icon name="plus" size={26} strokeWidth={2.2} /></button>}
          {fabMenu && <Menu anchor={fabMenu} align="right" items={createItems(navigate)} onClose={() => setFabMenu(null)} width={250} />}
          {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} />}
          {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
        </div>
      </TagsCtx.Provider>
    </CrumbCtx.Provider>
  )
}

const SECTION: Record<string, [string, string]> = { '': ['Watches', '/'], watches: ['Watches', '/'], changes: ['Changes', '/changes'], add: ['Watches', '/'] }

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
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('plk:shortcuts')), hint: '?' },
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
