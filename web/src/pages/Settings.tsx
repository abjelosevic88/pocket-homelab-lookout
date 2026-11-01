import { AskButton } from '../components/AskButton'
import React, { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, postFile, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, FetchMode, ImportResult, NotifyMode, Settings as SettingsT, User } from '../lib/types'
import { DATE_FORMATS, INTERVALS, TIME_FORMATS, everyLabel, fmtBytes, fmtDateTime, type DateFormat, type TimeFormat } from '../lib/format'
import { Avatar, Confirm, CopyField, CoreHint, Field, Loading, Modal, Segmented, SettingRow, Switch, Tabs, useAsync, useIsMobile, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { ShortcutsSheet } from '../components/ShortcutsSheet'
import { PocketModelSelect } from '../components/PocketModelSelect'
import { AIStatusLine, FETCH_LABEL, NOTIFY_LABEL, useAIStatus } from '../components/watch'

type TabId = 'general' | 'notifications' | 'ai' | 'profile' | 'people' | 'tokens' | 'data' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string }[] = [
  { id: 'general', label: 'General', icon: 'settings', sub: 'What a new watch starts with, how much history is kept, date formats and the app title.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', sub: 'Pushes when a page changes, the daily summary, failing pages, and your RSS feed.' },
  { id: 'ai', label: 'AI', icon: 'sparkles', sub: 'One-line summaries of every change, and conditions like “only if it drops under 50 €”.' },
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email and password.' },
  { id: 'people', label: 'People', icon: 'users', sub: 'Everyone who has an account on this box.' },
  { id: 'tokens', label: 'API & shortcuts', icon: 'key', sub: 'Personal tokens for Homepage widgets, Siri Shortcuts and Raycast, and a bookmarklet.' },
  { id: 'data', label: 'Backup & data', icon: 'database', sub: 'Import a list of pages or a changedetection.io backup, export your watches, download the database.' },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, numbers and the server.' },
]
// the old ?tab= names
const ALIAS: Record<string, TabId> = { users: 'people', api: 'tokens', integrations: 'ai', apps: 'ai' }

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const legacy = new URLSearchParams(window.location.search).get('tab') || ''
  const want = raw || legacy
  const tab: TabId = TABS.find(t => t.id === (ALIAS[want] || want))?.id || 'general'
  const current = TABS.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })

  // /settings?tab=users (old links) → /settings/people
  useEffect(() => { if (!raw && legacy) navigate(`/settings/${tab}`, { replace: true }) }, [raw, legacy, tab, navigate])
  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop, .menu, .sel-pop')) navigate('/') }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])

  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back to your watches" title="Back to your watches (esc)" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
        <span style={{ flex: 1 }} />
        <AskButton />
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={TABS.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {TABS.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner" key={tab}>
            <div className="set-title">
              <h2>{current.label}</h2>
              <p>{current.sub}</p>
            </div>
            {tab === 'general' && <General />}
            {tab === 'notifications' && <Notifications />}
            {tab === 'ai' && <AISettings />}
            {tab === 'profile' && <Profile />}
            {tab === 'people' && <Users />}
            {tab === 'tokens' && <Tokens />}
            {tab === 'data' && <Data />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
    </div>
  )
}

/** A settings card: title, explanation, actions on the right — Pocket Calendar's section. */
function Section({ title, sub, icon, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; icon?: string; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        {icon && <span className="set-app-ico"><Icon name={icon} size={17} /></span>}
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

function useSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<SettingsT | null>(settings)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!f && settings) setF(settings) }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = async (patch?: Partial<SettingsT>) => {
    if (!f) return
    setBusy(true)
    try { const s = await api.put<SettingsT>(`${V1}/settings`, { ...f, ...patch }); setSettings(s); setF(s); toast('Saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return { f, setF, save, busy, isAdmin: user?.role === 'admin' }
}

/** "Test connection" results per integration. */
function useTester() {
  const [test, setTest] = useState<Record<string, string>>({})
  const run = async (which: string, url: string, key: string, extra: Record<string, string> = {}) => {
    setTest(t => ({ ...t, [which]: 'testing…' }))
    try { const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/integrations/test`, { which, url, key, ...extra }); setTest(t => ({ ...t, [which]: r.ok ? `✓ ${r.message}` : `✕ ${r.error}` })) } catch (e) { setTest(t => ({ ...t, [which]: `✕ ${(e as Error).message}` })) }
  }
  const status = (which: string) => <span className={`small ${test[which]?.startsWith('✓') ? 'ok' : test[which]?.startsWith('✕') ? 'bad' : 'muted'}`}>{test[which]}</span>
  return { run, status }
}

function General() {
  const { f, setF, save, busy, isAdmin } = useSettingsForm()
  if (!f) return <Loading />
  const intervals = INTERVALS.map(m => ({ value: String(m), label: everyLabel(m), disabled: m < f.min_interval, hint: m < f.min_interval ? 'too often for this server' : undefined }))
  return (
    <div className="stack loose">
      <Section title="New watches start with" icon="eye" sub="Yours alone — everyone on the box picks their own. Each watch can change these.">
        <SettingRow title="Check every"><Select ariaLabel="Check every" value={String(f.default_interval)} onChange={v => setF({ ...f, default_interval: Number(v) })} options={intervals} /></SettingRow>
        <SettingRow title="Tell me" help="Right away, in one push a day, or not at all (the changes are still kept)."><Select ariaLabel="Tell me" value={f.default_notify_mode} onChange={v => setF({ ...f, default_notify_mode: v as NotifyMode })} options={(['now', 'digest', 'off'] as const).map(m => ({ value: m, label: NOTIFY_LABEL[m] }))} /></SettingRow>
        <SettingRow title="Read pages" help={f.browser ? 'Automatic uses the fast way and switches to the browser for pages that need JavaScript.' : 'There is no browser on this server, so pages that need JavaScript cannot be read (an admin can set BROWSER_URL).'}>
          <Select ariaLabel="Read pages" value={f.default_fetch_mode} onChange={v => setF({ ...f, default_fetch_mode: v as FetchMode })} options={(['auto', 'fast', 'browser'] as const).map(m => ({ value: m, label: FETCH_LABEL[m], disabled: m === 'browser' && !f.browser }))} />
        </SettingRow>
        <SettingRow title="Ignore dates, times and counters" help="“Updated 10:42”, “3 minutes ago”, “1,204 views” moving on their own do not count as a change. Switch it off on a watch whose dates are the news (appointments, timetables)."><Switch checked={f.default_noise} onChange={v => setF({ ...f, default_noise: v })} label="Ignore dates, times and counters" /></SettingRow>
      </Section>
      <Section title="History" icon="clock">
        <SettingRow title="Versions kept per page" help="Older versions of a page go; the changes between them stay."><input type="number" min={2} max={1000} value={f.keep_snapshots} onChange={e => setF({ ...f, keep_snapshots: Number(e.target.value) })} className="set-time" /></SettingRow>
        <SettingRow title="Keep changes for" help="0 keeps them forever."><span className="row"><input type="number" min={0} max={3650} value={f.keep_days} onChange={e => setF({ ...f, keep_days: Number(e.target.value) })} className="set-time" /><span className="muted small">days</span></span></SettingRow>
      </Section>
      <Section title="Date & time">
        <SettingRow title="Date format"><Select value={f.date_format} onChange={v => setF({ ...f, date_format: v as DateFormat })} options={DATE_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
        <SettingRow title="Time format"><Select value={f.time_format} onChange={v => setF({ ...f, time_format: v as TimeFormat })} options={TIME_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
      </Section>
      <Section title="This box" sub={isAdmin ? 'Applies to everyone on this box.' : 'Set by an admin for everyone.'}>
        <SettingRow title="App title" help="Shown in the sidebar and the browser tab."><input value={f.title} onChange={e => setF({ ...f, title: e.target.value })} maxLength={40} disabled={!isAdmin} title={isAdmin ? undefined : 'Box-wide — only an admin can change it'} /></SettingRow>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
      <Section title="How it fits together">
        <dl className="set-dl">
          <div><dt>Watches</dt><dd className="muted">A page, one part of it (picked by tapping it, or a CSS selector), its price and stock, or a value in a JSON API. Pages that need JavaScript are read with a headless browser, which can also keep a picture.</dd></div>
          <div><dt>Changes</dt><dd className="muted">Each check compares the text with the last version. Dates, counters and lines you ignore never count; “only when” lines and an AI condition decide what is worth a push. Every change gets a one-line summary.</dd></div>
          <div><dt>Telling you</dt><dd className="muted">A push right away or in the daily summary, an RSS feed for Pocket Reader or any reader, and events Pocket Flows can act on (“lookout.changed”).</dd></div>
        </dl>
      </Section>
    </div>
  )
}

function Notifications() {
  const { f, setF, save, busy } = useSettingsForm()
  const { toast } = useToast()
  const { setSettings } = useApp()
  const { run, status } = useTester()
  const [rotate, setRotate] = useState(false)
  if (!f) return <Loading />
  const digest = async () => { try { const r = await api.post<{ ok: boolean; sent: number; error?: string }>(`${V1}/notify/digest`); toast(r.ok ? (r.sent ? `Sent (${r.sent} change${r.sent === 1 ? '' : 's'})` : 'Nothing is waiting for the summary') : r.error || 'failed', r.ok ? 'ok' : 'err') } catch (e) { toast((e as Error).message, 'err') } }
  const newFeed = async () => { setRotate(false); try { const r = await api.post<{ feed_url: string }>(`${V1}/feed/rotate`); const s = { ...f, feed_url: r.feed_url }; setF(s); setSettings(s); toast('New feed address made; the old one stopped working') } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <div className="stack loose">
      <Section title="Push" icon="bell" actions={<span className={`badge ${f.notify_configured ? 'ok' : ''}`}>{f.notify_configured ? 'on' : 'not set'}</span>}
        sub={<>Apprise gateway (<code>…/notify/&lt;key&gt;</code>), an ntfy topic URL, or any webhook that takes JSON. On a box with Pocket Alerts use its Apprise address.</>}>
        <SettingRow title="URL" stack><input value={f.notify_url} onChange={e => setF({ ...f, notify_url: e.target.value })} placeholder="http://apprise:8000/notify/lookout" /></SettingRow>
        <SettingRow title="Format"><Segmented value={f.notify_format} onChange={v => setF({ ...f, notify_format: v })} options={[{ value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'json', label: 'JSON' }]} size="sm" /></SettingRow>
      </Section>
      <Section title="Daily summary" icon="inbox" sub="Watches set to “in the daily summary” wait here and go out as one push.">
        <SettingRow title="Send it at" help="Hour of the day, server time."><input type="number" min={0} max={23} value={f.digest_hour} onChange={e => setF({ ...f, digest_hour: Number(e.target.value) })} className="set-time" /></SettingRow>
      </Section>
      <Section title="Pages that stop working" icon="alert">
        <SettingRow title="Tell me when a page keeps failing" help="One push when it fails, and one when it works again."><Switch checked={f.notify_errors} onChange={v => setF({ ...f, notify_errors: v })} label="Tell me when a page keeps failing" /></SettingRow>
        <SettingRow title="After" help="Failed checks in a row."><input type="number" min={1} max={50} value={f.errors_after} onChange={e => setF({ ...f, errors_after: Number(e.target.value) })} className="set-time" disabled={!f.notify_errors} /></SettingRow>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        <button type="button" className="btn" onClick={() => run('notify', f.notify_url, '', { format: f.notify_format })} disabled={!f.notify_url}><Icon name="mail" size={15} />Send a test</button>
        {f.notify_configured && <button type="button" className="btn" onClick={digest}><Icon name="inbox" size={15} />Send the summary now</button>}
        {status('notify')}
      </div>
      <Section title="RSS feed" icon="rss" sub="Every change that counted, newest first, for Pocket Reader or any feed reader. The address is secret: anyone who has it can read your changes. Each watch has its own feed too (its menu → Copy RSS feed).">
        <CopyField value={f.feed_url} label="Feed address" />
        <div className="row mt-sm"><button type="button" className="btn sm ghost" onClick={() => setRotate(true)}><Icon name="refresh" size={14} />Make a new address</button></div>
        {rotate && <Confirm title="Make a new feed address?" message="The old address stops working; readers subscribed to it need the new one." confirmLabel="Make a new one" onCancel={() => setRotate(false)} onConfirm={newFeed} />}
      </Section>
    </div>
  )
}

function AISettings() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  const ai = useAIStatus()
  if (!f) return <Loading />
  const coreOn = f.core_configured && f.core_registered !== false
  // Pocket AI only comes through Pocket Core: without one a saved "pocket" shows as what is in effect.
  const shown = !coreOn && f.ai_provider === 'pocket' ? 'openai' : (f.ai_provider || 'auto')
  return (
    <div className="stack loose">
      <Section title="What the AI does" icon="sparkles" sub="For every change that counts, the AI reads the lines that came and went and writes one plain sentence (“Price dropped from 169 to 149 KM”). A watch with an AI condition asks it whether the change matches; only then you hear about it. Only the changed lines, the page's name and address go to the AI you pick.">
        <AIStatusLine st={ai} />
        <SettingRow title="Summaries" help="Off: summaries are made without AI (“New: …”, “3 lines gone”). Conditions still use the AI."><Switch checked={f.ai_summaries} onChange={v => setF({ ...f, ai_summaries: v })} label="Write summaries with AI" /></SettingRow>
      </Section>
      <Section title="AI" icon="sparkles">
        <div className="grid" style={{ gap: 12 }}>
          <Field group label="Which AI" help="Automatic prefers your enabled Pocket AI connection; without one it uses your OpenAI settings; with neither, there are no AI summaries and conditions let every change through.">
            <Select ariaLabel="AI provider" value={shown} onChange={v => setF({ ...f, ai_provider: v as SettingsT['ai_provider'] })} options={[{ value: 'auto', label: coreOn ? 'Automatic — prefer Pocket AI' : 'Automatic' }, { value: 'pocket', label: 'Pocket AI', disabled: !coreOn, hint: coreOn ? undefined : 'needs Pocket Core' }, { value: 'openai', label: 'OpenAI / compatible API' }]} />
          </Field>
          {!coreOn && <CoreHint />}
          {coreOn && shown !== 'openai' && <PocketModelSelect value={f.ai_pocket_model || ''} onChange={v => setF({ ...f, ai_pocket_model: v })} />}
          {(shown === 'openai' || (!coreOn && shown === 'auto')) && <>
            <Field label="OpenAI API key" help="platform.openai.com → API keys. Stored on the server; shown masked."><input value={f.openai_api_key} onChange={e => setF({ ...f, openai_api_key: e.target.value })} autoComplete="off" placeholder="sk-…" /></Field>
            <div className="two-col tight">
              <Field label="Model" help="Default gpt-4o-mini."><input value={f.openai_model} onChange={e => setF({ ...f, openai_model: e.target.value })} placeholder="gpt-4o-mini" /></Field>
              <Field label="Base URL (optional)" help="Ollama, LM Studio, OpenRouter…"><input value={f.openai_base_url} onChange={e => setF({ ...f, openai_base_url: e.target.value })} placeholder="https://api.openai.com/v1" /></Field>
            </div>
          </>}
        </div>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        {(shown === 'openai' || (!coreOn && shown === 'auto')) && <><button type="button" className="btn" onClick={() => run('openai', f.openai_base_url, f.openai_api_key)} disabled={!f.openai_api_key}>Test the OpenAI key</button>{status('openai')}</>}
      </div>
    </div>
  )
}

function Users() {
  const { user, users, reloadMeta, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); await reloadMeta(); toast(msg) } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Section title="People on this box" actions={isAdmin && <button type="button" className="btn primary sm" onClick={() => setEdit({ role: 'member' })}><Icon name="plus" size={14} />Add person</button>}
      sub="Everyone signs in with their own account and has their own watches, changes and settings. Admins manage accounts; they do not see anyone else’s watches.">
      <div className="set-people">
        {users.map(u => (
          <div key={u.id} className="set-person">
            <Avatar name={u.name} email={u.email} size={34} />
            <div className="grow">
              <div className="set-person-name">{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge">you</span>}{u.role === 'admin' && <span className="badge accent">admin</span>}</div>
              <div className="muted small ellipsis">{u.email}</div>
            </div>
            {isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button type="button" className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}
          </div>
        ))}
      </div>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit; passwords are not used here.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadMeta() }} />}
      {del && <Confirm confirmLabel="Remove" title={`Remove ${del.name || del.email}?`} message="Their account is deleted together with their watches and changes." onCancel={() => setDel(null)} onConfirm={() => { const d = del; setDel(null); run(() => api.del(`${V1}/users/${d.id}`), 'User removed') }} />}
    </Section>
  )
}

function UserModal({ user, local, onClose, onSaved }: { user: Partial<User>; local: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: user.name || '', email: user.email || '', password: '', role: user.role || 'member' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { if (user.id) await api.put(`${V1}/users/${user.id}`, f); else await api.post(`${V1}/users`, f); toast(user.id ? 'User saved' : 'User added'); onSaved() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={user.id ? 'Edit person' : 'Add a person'} onClose={onClose} size="sm">
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add person'}</button></div>
      </form>
    </Modal>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: user?.name || '', email: user?.email || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setUser(await api.put(`${V1}/auth/me`, f)); setF(x => ({ ...x, current_password: '', new_password: '' })); toast('Profile saved') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <form onSubmit={save} className="stack loose">
      <div className="set-profile">
        <Avatar name={user?.name} email={user?.email} size={56} />
        <div className="grow"><h3>{user?.name || user?.email}</h3><p className="muted small">{user?.email} · {user?.role}</p></div>
      </div>
      <Section title="You">
        <div className="stack">
          <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
          <Field label="Email"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
        </div>
      </Section>
      {status?.auth_mode === 'local' && <Section title="Password" sub="Leave empty to keep the current one."><div className="stack">
        <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
        <Field label="New password" help="At least 8 characters."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
      </div></Section>}
      <div className="row"><button className="btn primary" disabled={busy}>Save changes</button></div>
    </form>
  )
}

function Tokens() {
  const { toast } = useToast()
  const { settings } = useApp()
  const { data, reload } = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'token' }); setFresh(r.token); setName(''); reload() } catch (e) { toast((e as Error).message, 'err') }
  }
  const base = settings?.base_url || window.location.origin
  return (
    <div className="stack loose">
      <Section title="Personal tokens" icon="key" sub={<>Use as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you: it can read and change everything, feed a Homepage widget (<code>GET /api/v1/dashboard</code>) or drive a phone shortcut. See <code>docs/api.md</code>.</>}>
        <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. Homepage widget)" /><button className="btn primary"><Icon name="plus" size={15} />Create</button></form>
        {fresh && <div className="set-fresh mb"><div className="row"><Icon name="check-circle" size={16} /><b>Copy it now — it is shown once.</b></div><pre className="mono">{fresh}</pre></div>}
        {data && data.length > 0 ? (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Prefix</th><th>Last used</th><th>Created</th><th /></tr></thead>
            <tbody>{data.map(t => <tr key={t.id}><td>{t.name}</td><td className="mono">{t.prefix}…</td><td className="muted">{t.last_used_at ? fmtDateTime(t.last_used_at) : 'never'}</td><td className="muted">{fmtDateTime(t.created_at)}</td><td className="right"><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button></td></tr>)}</tbody>
          </table>
        ) : <p className="muted">No tokens yet.</p>}
        {del && <Confirm confirmLabel="Revoke" title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
      </Section>
      <Section title="Add from anywhere" icon="smartphone" sub={<>Handy endpoints at <code>{base}/api/v1</code>, and a bookmarklet.</>}>
        <dl className="set-dl">
          <div><dt>Bookmarklet</dt><dd className="muted">Drag this to the bookmarks bar; on any page it opens “Watch a page” with that page: <a className="badge" href={`javascript:location.href='${base}/add?url='+encodeURIComponent(location.href)+'&title='+encodeURIComponent(document.title)`} onClick={e => e.preventDefault()}>+ Lookout</a></dd></div>
          <div><dt><code>{base}/add?url=…</code></dt><dd className="muted">The same from an iOS Shortcut’s “Open URLs” or a share sheet.</dd></div>
          <div><dt><code>POST /watches</code></dt><dd className="muted"><code>{'{"url":"https://…","selector":"#price","ai_condition":"…"}'}</code> — starts watching right away.</dd></div>
          <div><dt><code>GET /changes?unseen=1</code></dt><dd className="muted">New changes with their summaries.</dd></div>
          <div><dt><code>GET /dashboard</code></dt><dd className="muted">Watches, new changes, failing pages — for a Homepage widget.</dd></div>
        </dl>
      </Section>
    </div>
  )
}

function Data() {
  const { toast } = useToast()
  const { user } = useApp()
  const [busy, setBusy] = useState(false)
  const [urls, setUrls] = useState('')
  const [tags, setTags] = useState('')
  const [res, setRes] = useState<ImportResult | null>(null)
  const done = (r: ImportResult) => { setRes(r); toast(r.added ? `Added ${r.added} watch${r.added === 1 ? '' : 'es'}` : 'Nothing new to add', r.added ? 'ok' : undefined) }
  const send = async (body: File | string) => {
    setBusy(true); setRes(null)
    try { done(await postFile<ImportResult>(`${V1}/import${tags.trim() ? `?tags=${encodeURIComponent(tags)}` : ''}`, body)) } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <div className="stack loose">
      <Section title="Import a list of pages" icon="list" sub="One address per line; words after the address become tags (“https://example.com/news tech, news”). Pages you already watch are skipped.">
        <textarea rows={6} value={urls} onChange={e => setUrls(e.target.value)} placeholder={'https://example.com/releases\nhttps://shop.example.com/item shopping'} />
        <div className="row wrap mt-sm">
          <input value={tags} onChange={e => setTags(e.target.value)} placeholder="Tags for all of them (optional)" style={{ maxWidth: 280 }} />
          <button type="button" className="btn primary" disabled={busy || !urls.trim()} onClick={() => send(urls)}><Icon name="upload" size={15} />{busy ? 'Importing…' : 'Watch these pages'}</button>
        </div>
      </Section>
      <Section title="From changedetection.io or another Lookout" icon="archive" sub={<>A changedetection.io backup (the <b>.zip</b> from its Backups page, or the <code>url-watches.json</code> inside), or a Pocket Lookout export. Addresses, titles, tags, CSS filters, ignore and trigger text, intervals, paused state and browser use come along; XPath filters do not (pick the part again).</>}>
        <label className="btn"><Icon name="upload" size={15} />{busy ? 'Importing…' : 'Choose a file…'}<input type="file" accept=".zip,.json,application/json,application/zip" hidden onChange={e => { const file = e.target.files?.[0]; e.target.value = ''; if (file) send(file) }} /></label>
      </Section>
      {res && <div className={`callout ${res.added ? 'info' : ''}`}><Icon name="check-circle" size={15} /><div>
        From {res.from}: <b>{res.added}</b> added, {res.skipped} skipped.
        {res.notes.length > 0 && <ul className="set-list">{res.notes.map((n, i) => <li key={i}>{n}</li>)}</ul>}
      </div></div>}
      <Section title="Export" icon="download" sub="Your watches and their recorded changes as one JSON file. Importing it here (or on another box) adds the watches; the history starts again with the first check.">
        <a className="btn" href={`${V1}/export.json`}><Icon name="download" size={15} />Export JSON</a>
      </Section>
      {user?.role === 'admin' && <Section title="Database backup" icon="database" sub="The whole box, every account — admins only.">
        <p className="muted">A consistent copy of the SQLite database (<code>VACUUM INTO</code>). Page pictures live next to it under <code>files/</code>. Restore by stopping the app and replacing <code>pocket-lookout.db</code> in the data directory.</p>
        <a className="btn primary" href={`${V1}/backup`}><Icon name="download" size={15} />Download database</a>
      </Section>}
    </div>
  )
}

function About() {
  const { data, error } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const [keys, setKeys] = useState(false)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!data) return <Loading />
  const counts = (data.counts || {}) as Record<string, number>
  const tiles: [string, number | undefined, string][] = [['Watches', counts.watches, 'eye'], ['Changes', counts.changes, 'activity'], ['Checks since start', Number(data.checks_run), 'refresh'], ['With the browser', Number(data.browser_checks), 'globe']]
  const rows: [string, React.ReactNode][] = [
    ['Version', <code key="v">{String(data.version)}</code>],
    ['Uptime', String(data.uptime)],
    ['Runtime', `${String(data.go)} · ${String(data.os)}/${String(data.arch)}`],
    ['Time zone', String(data.timezone)],
    ['Sign-in', String(data.auth_mode)],
    ['Database', <span key="db"><code>{String(data.db_path)}</code> · {fmtBytes(Number(data.db_bytes))} · files {fmtBytes(Number(data.files_bytes))}</span>],
    ['Checking', `${String(data.workers)} pages at a time · at most every ${String(data.min_interval)} min${data.fetch_private ? ' · private addresses allowed' : ''}`],
    ['Browser', data.browser ? 'set up (pages that need JavaScript and pictures work)' : 'not set up (BROWSER_URL)'],
    ['People', `${counts.users ?? 0} accounts on this box`],
  ]
  return (
    <div className="stack loose">
      <div className="set-about-hero">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div>
          <h3>Pocket Lookout</h3>
          <p className="muted small">Watches web pages for you: a whole page, one part of it, a price or a JSON value. Noise is ignored, every change gets a one-line summary, AI conditions decide what matters, and changes reach you as pushes, a daily summary, RSS or events for Pocket Flows. One binary, one SQLite file, one volume.</p>
        </div>
      </div>
      <div className="set-stats">
        {tiles.map(([label, n, icon]) => (
          <div key={label} className="set-stat"><Icon name={icon} size={16} /><span className="set-stat-n tnum">{n ?? 0}</span><span className="set-stat-l">{label}</span></div>
        ))}
      </div>
      <Section title="This server">
        <dl className="set-dl">
          {rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}
        </dl>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn" onClick={() => setKeys(true)}><Icon name="keyboard" size={15} />Keyboard shortcuts</button>
        <a className="btn" href="https://github.com/abjelosevic88/pocket-homelab-lookout" target="_blank" rel="noreferrer"><Icon name="external" size={15} />Project on GitHub</a>
      </div>
      <p className="muted small">Open source (MIT). Part of the Pocket apps: Pocket Core connects them — Alerts, Flows, Reader, Day and more.</p>
      {keys && <ShortcutsSheet onClose={() => setKeys(false)} />}
    </div>
  )
}
