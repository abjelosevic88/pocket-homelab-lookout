// "/" — every watch, newest change first, with filters, tags and bulk actions.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { ago, plural } from '../lib/format'
import type { Outcome, Watch } from '../lib/types'
import { Icon } from '../components/icons'
import { announceChange, useOnChange, useShell } from '../components/Layout'
import { Confirm, Empty, Loading, MenuButton, Modal, PageHeader, Segmented, Spinner, errMsg, useDebounce, useIsMobile, useToast } from '../components/ui'
import type { MenuItem } from '../components/ui'
import { SiteIcon, StatusDot, checkLine, stateText, watchState } from '../components/watch'

type StateFilter = '' | 'changed' | 'failing' | 'paused'

export default function Watches() {
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const { toast } = useToast()
  const { tags, dash } = useShell()
  const [sp, setSp] = useSearchParams()
  const state = (sp.get('state') || '') as StateFilter
  const tag = sp.get('tag') || ''
  const [q, setQ] = useState(sp.get('q') || '')
  const dq = useDebounce(q, 200)
  const [watches, setWatches] = useState<Watch[] | null>(null)
  const [error, setError] = useState('')
  const [checkingAll, setCheckingAll] = useState(false)
  const [selecting, setSelecting] = useState(false)
  const [picked, setPicked] = useState<Set<number>>(new Set())
  const [confirm, setConfirm] = useState<{ ids: number[]; name: string } | null>(null)
  const [tagModal, setTagModal] = useState(false)
  const [bulkBusy, setBulkBusy] = useState(false)

  // the whole list is loaded once; state and tag are filtered here so switching is instant
  const load = useCallback(() => {
    api.get<Watch[]>(`${V1}/watches`).then(ws => { setWatches(ws); setError('') }).catch(e => setError(errMsg(e)))
  }, [])
  useEffect(() => { load() }, [load])
  useOnChange(load)
  useEffect(() => { const t = window.setInterval(load, 20_000); return () => window.clearInterval(t) }, [load])

  const setParam = (k: string, v: string) => {
    const n = new URLSearchParams(sp)
    if (v) n.set(k, v); else n.delete(k)
    setSp(n, { replace: true })
  }

  const shown = useMemo(() => {
    if (!watches) return []
    const words = dq.trim().toLowerCase().split(/\s+/).filter(Boolean)
    return watches.filter(w => {
      if (state === 'changed' && !(w.unseen > 0)) return false
      if (state === 'failing' && !(w.status === 'error' && !w.paused)) return false
      if (state === 'paused' && !w.paused) return false
      if (tag && !w.tags.split(',').map(t => t.trim().toLowerCase()).includes(tag.toLowerCase())) return false
      if (words.length) {
        const hay = `${w.name} ${w.url} ${w.tags} ${w.notes} ${w.last_summary}`.toLowerCase()
        if (!words.every(x => hay.includes(x))) return false
      }
      return true
    })
  }, [watches, state, tag, dq])

  const title = tag ? `#${tag}` : state === 'failing' ? 'Failing' : state === 'paused' ? 'Paused' : state === 'changed' ? 'New changes' : 'Watches'
  const unfiltered = !state && !tag && !dq
  const sub = watches == null ? '' : unfiltered
    ? `${plural(watches.length, 'page')} watched${dash && dash.unseen ? ` · ${plural(dash.unseen, 'new change')}` : ''}`
    : plural(shown.length, 'watch', 'watches')

  const checkAll = async () => {
    setCheckingAll(true)
    try {
      const r = await api.post<{ count: number }>(`${V1}/watches/bulk`, { action: 'check' })
      toast(`Checking ${plural(r.count, 'page')}`)
      window.setTimeout(() => { load(); announceChange() }, 4000)
    } catch (e) { toast(errMsg(e), 'err') } finally { setCheckingAll(false) }
  }

  const bulk = async (action: string, ids: number[], extra: Record<string, unknown> = {}) => {
    setBulkBusy(true)
    try {
      const r = await api.post<{ count: number }>(`${V1}/watches/bulk`, { action, ids, ...extra })
      const verb: Record<string, string> = { check: 'Checking', pause: 'Paused', resume: 'Resumed', delete: 'Deleted', tag: 'Tagged' }
      toast(`${verb[action] || 'Done'}: ${plural(r.count, 'watch', 'watches')}`)
      setPicked(new Set())
      if (action === 'delete') setSelecting(false)
      load(); announceChange()
      if (action === 'check') window.setTimeout(load, 4000)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBulkBusy(false) }
  }

  const one = async (w: Watch, path: string, done: string) => {
    try { await api.post(`${V1}/watches/${w.id}/${path}`); toast(done); load(); announceChange() } catch (e) { toast(errMsg(e), 'err') }
  }
  const checkOne = async (w: Watch) => {
    toast(`Checking ${w.name}…`)
    try {
      const r = await api.post<{ outcome: Outcome }>(`${V1}/watches/${w.id}/check`)
      const o = r.outcome
      toast(!o.ok ? `Could not read it: ${o.error}` : o.changed && !o.ignored ? `${w.name} changed` : `${w.name}: nothing new`, o.ok ? 'ok' : 'err')
      load(); announceChange()
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const duplicate = async (w: Watch) => {
    try { const c = await api.post<Watch>(`${V1}/watches/${w.id}/duplicate`); announceChange(); navigate(`/watches/${c.id}/edit`) } catch (e) { toast(errMsg(e), 'err') }
  }
  const remove = async (ids: number[]) => {
    if (ids.length === 1) {
      try { await api.del(`${V1}/watches/${ids[0]}`); toast('Deleted'); load(); announceChange() } catch (e) { toast(errMsg(e), 'err') }
    } else await bulk('delete', ids)
  }

  const menu = (w: Watch): MenuItem[] => [
    { label: 'Check now', icon: 'refresh', onSelect: () => checkOne(w) },
    w.paused ? { label: 'Resume', icon: 'play', onSelect: () => one(w, 'resume', 'Resumed') } : { label: 'Pause', icon: 'pause', onSelect: () => one(w, 'pause', 'Paused') },
    { label: 'Edit', icon: 'edit', onSelect: () => navigate(`/watches/${w.id}/edit`) },
    { label: 'Duplicate', icon: 'copy', onSelect: () => duplicate(w) },
    { label: 'Open the page', icon: 'external', onSelect: () => window.open(w.url, '_blank', 'noopener') },
    'sep',
    { label: 'Delete', icon: 'trash', danger: true, onSelect: () => setConfirm({ ids: [w.id], name: w.name }) },
  ]

  const toggle = (id: number) => setPicked(p => { const n = new Set(p); if (n.has(id)) n.delete(id); else n.add(id); return n })
  const ids = [...picked]
  const allPicked = shown.length > 0 && shown.every(w => picked.has(w.id))

  if (watches == null && !error) return <Loading />

  if (watches && watches.length === 0) {
    return (
      <>
        <PageHeader title="Watches" />
        <Empty title="Nothing is watched yet" icon="eye">
          <p className="wl-empty-text">Lookout reads the web pages you pick — a whole page, one part of it, a price or a JSON API — and tells you when something there changes. Dates, counters and ads are ignored, and each change comes with a one-line summary.</p>
          <div className="row wl-empty-actions">
            <Link to="/add" className="btn primary"><Icon name="plus" size={16} />Watch a page</Link>
            <Link to="/settings/data" className="btn"><Icon name="upload" size={16} />Import a list</Link>
          </div>
        </Empty>
      </>
    )
  }

  return (
    <>
      <PageHeader title={title} sub={sub} actions={<>
        <button type="button" className="btn" onClick={checkAll} disabled={checkingAll || !watches?.length} title="Check every active page now">
          {checkingAll ? <Spinner /> : <Icon name="refresh" size={15} />}Check all
        </button>
        {!mobile && <Link to="/add" className="btn primary"><Icon name="plus" size={16} />Watch a page</Link>}
      </>} />

      {unfiltered && dash && (
        <div className="stats wl-stats">
          <div className="stat"><Icon name="eye" size={16} /><span className="n">{dash.watches}</span><span className="l">Watches</span></div>
          <Link to="/changes" className="stat"><Icon name="activity" size={16} /><span className="n">{dash.unseen}</span><span className="l">New changes</span></Link>
          <div className="stat"><Icon name="clock" size={16} /><span className="n">{dash.changes_week}</span><span className="l">Changes this week</span></div>
          <Link to="/?state=failing" className="stat"><Icon name="alert" size={16} /><span className={`n ${dash.failing ? 'wl-bad' : ''}`}>{dash.failing}</span><span className="l">Failing</span></Link>
        </div>
      )}

      <div className="toolbar">
        <div className="search">
          <Icon name="search" size={15} />
          <input type="search" value={q} onChange={e => { setQ(e.target.value); setParam('q', e.target.value) }} placeholder="Find a watch, a site, a tag…" aria-label="Find a watch" />
        </div>
        <Segmented<StateFilter> size="sm" label="Show" value={state} onChange={v => setParam('state', v)}
          options={[{ value: '', label: 'All' }, { value: 'changed', label: mobile ? 'New' : 'New changes' }, { value: 'failing', label: 'Failing' }, { value: 'paused', label: 'Paused' }]} />
        <button type="button" className={`btn sm ${selecting ? '' : 'ghost'}`} onClick={() => { setSelecting(s => !s); setPicked(new Set()) }}>
          <Icon name={selecting ? 'x' : 'check'} size={14} />{selecting ? 'Done' : 'Select'}
        </button>
      </div>

      {tags.length > 0 && (
        <div className="chips wl-tags">
          {tag && <button type="button" className="chip sm ghost" onClick={() => setParam('tag', '')}><Icon name="x" size={12} />All tags</button>}
          {tags.map(t => (
            <button key={t.tag} type="button" className={`chip sm ${t.tag === tag ? 'active' : ''}`} onClick={() => setParam('tag', t.tag === tag ? '' : t.tag)}>
              <Icon name="hash" size={12} />{t.tag}<span className="n">{t.count}</span>
            </button>
          ))}
        </div>
      )}

      {selecting && (
        <div className="wl-bulk">
          <label className="check-row"><input type="checkbox" checked={allPicked} onChange={() => setPicked(allPicked ? new Set() : new Set(shown.map(w => w.id)))} />{picked.size ? `${picked.size} picked` : 'Pick all'}</label>
          <span className="spacer" />
          <button type="button" className="btn sm" disabled={!picked.size || bulkBusy} onClick={() => bulk('check', ids)}><Icon name="refresh" size={14} />Check</button>
          <button type="button" className="btn sm" disabled={!picked.size || bulkBusy} onClick={() => bulk('pause', ids)}><Icon name="pause" size={14} />Pause</button>
          <button type="button" className="btn sm" disabled={!picked.size || bulkBusy} onClick={() => bulk('resume', ids)}><Icon name="play" size={14} />Resume</button>
          <button type="button" className="btn sm" disabled={!picked.size || bulkBusy} onClick={() => setTagModal(true)}><Icon name="hash" size={14} />Tag</button>
          <button type="button" className="btn sm danger-ghost" disabled={!picked.size || bulkBusy} onClick={() => setConfirm({ ids, name: plural(ids.length, 'watch', 'watches') })}><Icon name="trash" size={14} />Delete</button>
        </div>
      )}

      {error && <div className="callout bad mb"><Icon name="alert" size={15} /><span>{error}</span></div>}

      {shown.length === 0
        ? <Empty title="Nothing here" icon="filter">{dq ? 'No watch matches what you typed.' : state === 'failing' ? 'Every page can be read.' : state === 'paused' ? 'No watch is paused.' : state === 'changed' ? 'You have seen every change.' : 'No watch has this tag.'}</Empty>
        : (
          <ul className="wl-list" aria-label="Watches">
            {shown.map(w => (
              <WatchRow key={w.id} w={w} selecting={selecting} picked={picked.has(w.id)} onPick={() => toggle(w.id)}
                onOpen={() => navigate(`/watches/${w.id}`)} menu={() => menu(w)} />
            ))}
          </ul>
        )}

      {confirm && <Confirm title={`Delete ${confirm.name}?`} message="The watch and every change it recorded are deleted. This cannot be undone." confirmLabel="Delete"
        onCancel={() => setConfirm(null)} onConfirm={() => { const c = confirm; setConfirm(null); remove(c.ids) }} />}
      {tagModal && <TagModal count={ids.length} onClose={() => setTagModal(false)} onSave={t => { setTagModal(false); bulk('tag', ids, { tag: t }) }} />}
    </>
  )
}

function WatchRow({ w, selecting, picked, onPick, onOpen, menu }: { w: Watch; selecting: boolean; picked: boolean; onPick: () => void; onOpen: () => void; menu: () => MenuItem[] }) {
  const st = watchState(w)
  const line = st === 'error' ? w.last_error || 'The last check failed' : w.last_summary || stateText(w)
  return (
    <li className={`wl-row ${w.unseen > 0 ? 'unseen' : ''} ${w.paused ? 'paused' : ''} ${picked ? 'picked' : ''}`}
      onClick={() => (selecting ? onPick() : onOpen())} onKeyDown={e => { if (e.key === 'Enter' && e.target === e.currentTarget) onOpen() }} tabIndex={0} aria-label={w.name}>
      {selecting && <input type="checkbox" className="wl-pick" checked={picked} onChange={onPick} onClick={e => e.stopPropagation()} aria-label={`Pick ${w.name}`} />}
      <SiteIcon host={w.host} />
      <div className="wl-main">
        <div className="wl-name">
          <span className="ellipsis">{w.name}</span>
          {w.unseen > 0 && <span className="badge accent" title={stateText(w)}>{w.unseen}</span>}
        </div>
        <div className={`wl-line ellipsis ${st === 'error' ? 'wl-bad' : ''}`}>{line}</div>
        <div className="wl-meta ellipsis">{w.host} · {checkLine(w)}</div>
      </div>
      <div className="wl-side">
        <span className="wl-when"><StatusDot w={w} />{st === 'checking' ? 'checking' : w.paused ? 'paused' : w.last_changed_at ? ago(w.last_changed_at) : '—'}</span>
        {w.tags && <span className="wl-tagline ellipsis">{w.tags.split(',').map(t => `#${t.trim()}`).join(' ')}</span>}
      </div>
      <div className="wl-more" onClick={e => e.stopPropagation()}>
        <MenuButton label={`More for ${w.name}`} align="right" items={menu} width={200}><Icon name="more" size={16} /></MenuButton>
      </div>
    </li>
  )
}

function TagModal({ count, onClose, onSave }: { count: number; onClose: () => void; onSave: (tag: string) => void }) {
  const [t, setT] = useState('')
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => { ref.current?.focus() }, [])
  const ok = t.trim().length > 0
  return (
    <Modal title={`Tag ${plural(count, 'watch', 'watches')}`} onClose={onClose} size="sm"
      footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" disabled={!ok} onClick={() => onSave(t.trim())}>Add tag</button></>}>
      <form onSubmit={e => { e.preventDefault(); if (ok) onSave(t.trim()) }}>
        <label className="field"><span className="label">Tag</span>
          <input ref={ref} value={t} onChange={e => setT(e.target.value)} placeholder="e.g. shopping" />
          <span className="help">Added to every picked watch; the tags they already have stay.</span>
        </label>
      </form>
    </Modal>
  )
}
