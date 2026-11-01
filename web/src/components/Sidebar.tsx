// Left column: the create button, sections with counts and the person's tags — the Pocket Calendar sidebar.
import { type ReactNode, useState } from 'react'
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import type { Dashboard, TagCount } from '../lib/types'
import { Icon } from './icons'
import { createItems } from './Layout'
import { type Anchor, Menu, rectOf } from './ui'

export function Sidebar({ onNavigate, dash, tags }: { onNavigate: () => void; dash: Dashboard | null; tags: TagCount[] }) {
  const { status, settings } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const title = settings?.title || status?.title || 'Pocket Lookout'
  const q = new URLSearchParams(loc.search)
  const here = (path: string, key?: string, val?: string) => loc.pathname === path && (key ? q.get(key) === val : !q.get('state') && !q.get('tag'))
  const nav: { to: string; label: string; icon: string; active: boolean; badge?: number; badgeKind?: string; badgeTitle?: string; count?: number }[] = [
    { to: '/', label: 'Watches', icon: 'eye', active: here('/'), count: dash?.watches },
    { to: '/changes', label: 'Changes', icon: 'activity', active: loc.pathname.startsWith('/changes'), badge: dash?.unseen, badgeKind: 'accent', badgeTitle: 'new changes you have not looked at' },
    { to: '/?state=failing', label: 'Failing', icon: 'alert', active: here('/', 'state', 'failing'), badge: dash?.failing, badgeKind: 'warn', badgeTitle: 'watches whose page cannot be read' },
    { to: '/?state=paused', label: 'Paused', icon: 'pause', active: here('/', 'state', 'paused'), count: dash?.paused },
  ]
  return (
    <aside className="sidebar" aria-label="Lookout">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Lookout' ? title : <>Pocket <b>Lookout</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
        <Icon name="plus" size={17} strokeWidth={2.2} />New<Icon name="chevron-down" size={15} className="create-caret" />
      </button>
      {menu && <Menu anchor={menu} items={createItems(to => { onNavigate(); navigate(to) })} onClose={() => setMenu(null)} width={248} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} onClick={onNavigate} className={() => `sb-nav-item ${n.active ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}
            {!!n.badge && <span className={`badge ${n.badgeKind || ''}`} title={`${n.badge} ${n.badgeTitle || ''}`}>{n.badge}</span>}
            {!n.badge && !!n.count && <span className="sb-count">{n.count}</span>}
          </NavLink>
        ))}
      </nav>

      <Group title="Tags">
        {tags.length === 0
          ? <p className="sb-empty">Tags you give your watches show up here.</p>
          : <ul className="cal-list">{tags.map(t => (
            <li key={t.tag} className="cal-row sb-row">
              <NavLink to={`/?tag=${encodeURIComponent(t.tag)}`} onClick={onNavigate} className={() => `cal-check ${here('/', 'tag', t.tag) ? 'is-active' : ''}`} title={t.tag}>
                <span className="sb-ico"><Icon name="hash" size={13} /></span>
                <span className="cal-name ellipsis">{t.tag}</span>
                <span className="sb-count">{t.count}</span>
              </NavLink>
            </li>
          ))}</ul>}
      </Group>

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('plk:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  const key = `plk-sb-${title}`
  const [open, setOpen] = useState(() => localStorage.getItem(key) !== '0')
  const toggle = () => setOpen(o => { localStorage.setItem(key, o ? '0' : '1'); return !o })
  return (
    <section className="sb-sec">
      <h4 className="sb-h">
        <button type="button" className="sb-h-btn" aria-expanded={open} onClick={toggle}><Icon name={open ? 'chevron-down' : 'chevron-right'} size={13} />{title}</button>
      </h4>
      {open && children}
    </section>
  )
}
