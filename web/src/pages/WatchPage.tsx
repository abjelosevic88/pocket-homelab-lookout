// "/watches/:id" — one watch: its changes, what the page says now, its checks and its settings in words.
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, everyLabel, fmtBytes, fmtDateTime, plural, until } from '../lib/format'
import type { Change, Check, Outcome, Snapshot, Watch } from '../lib/types'
import { Icon } from '../components/icons'
import { announceChange, useCrumb, useOnChange } from '../components/Layout'
import { Card, Confirm, Empty, Loading, MenuButton, Segmented, Spinner, Switch, Tabs, copyText, errMsg, useToast } from '../components/ui'
import type { MenuItem } from '../components/ui'
import { Counts, FETCH_LABEL, NOTIFY_LABEL, NOTIFY_ON_LABEL, SiteIcon, StatusDot, Summary, checkLine, stateText, watchState } from '../components/watch'

type Tab = 'changes' | 'now' | 'checks' | 'about'
const TABS: { id: Tab; label: string; icon: string }[] = [
  { id: 'changes', label: 'Changes', icon: 'activity' },
  { id: 'now', label: 'Now', icon: 'eye' },
  { id: 'checks', label: 'Checks', icon: 'clock' },
  { id: 'about', label: 'About this watch', icon: 'info' },
]

export default function WatchPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const { settings } = useApp()
  const [sp, setSp] = useSearchParams()
  const tab = (TABS.find(t => t.id === sp.get('tab'))?.id || 'changes') as Tab
  const [w, setW] = useState<Watch | null>(null)
  const [missing, setMissing] = useState(false)
  const [checking, setChecking] = useState(false)
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const [rev, setRev] = useState(0) // bumps after a check so the tabs reload
  useCrumb(w?.name)

  const load = useCallback(() => {
    api.get<Watch>(`${V1}/watches/${id}`).then(setW).catch(e => { if (e instanceof ApiError && e.status === 404) setMissing(true) })
  }, [id])
  useEffect(() => { setW(null); setMissing(false); load() }, [load])
  useOnChange(load)
  useEffect(() => { const t = window.setInterval(load, 20_000); return () => window.clearInterval(t) }, [load])

  const checkNow = useCallback(async () => {
    if (!w || checking) return
    setChecking(true)
    try {
      const r = await api.post<{ outcome: Outcome; watch: Watch }>(`${V1}/watches/${w.id}/check`)
      const o = r.outcome
      if (r.watch) setW(r.watch)
      if (!o.ok) toast(`Could not read the page: ${o.error}`, 'err')
      else if (o.first) toast('First look taken: changes count from now on')
      else if (o.changed && o.change && !o.ignored) toast(<>Changed: {o.change.summary}</>, 'ok', { label: 'Open', run: () => navigate(`/changes/${o.change!.id}`) })
      else if (o.changed) toast('The page changed, but not in a way this watch counts')
      else toast(`No change (${o.via === 'browser' ? 'browser' : 'fast'}, ${o.ms} ms)`)
      setRev(x => x + 1)
      announceChange()
    } catch (e) { toast(errMsg(e), 'err') } finally { setChecking(false) }
  }, [w, checking, toast, navigate])

  // r = check now (only while this page is open)
  const checkRef = useRef(checkNow)
  checkRef.current = checkNow
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (e.key !== 'r' || e.metaKey || e.ctrlKey || e.altKey) return
      const t = e.target as HTMLElement
      if (t.closest('input, textarea, select, [contenteditable="true"]') || document.querySelector('.modal-backdrop, .palette-backdrop, .menu, .sel-pop')) return
      e.preventDefault()
      checkRef.current()
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [])

  const setPaused = async (paused: boolean) => {
    if (!w) return
    setBusy(true)
    try { setW(await api.post<Watch>(`${V1}/watches/${w.id}/${paused ? 'pause' : 'resume'}`)); toast(paused ? 'Paused' : 'Resumed: it is checked right away'); announceChange() }
    catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const duplicate = async () => {
    if (!w) return
    try { const c = await api.post<Watch>(`${V1}/watches/${w.id}/duplicate`); announceChange(); navigate(`/watches/${c.id}/edit`) } catch (e) { toast(errMsg(e), 'err') }
  }
  const markSeen = async () => {
    if (!w) return
    try { await api.post(`${V1}/changes/seen`, { watch_id: w.id }); load(); setRev(x => x + 1); announceChange() } catch (e) { toast(errMsg(e), 'err') }
  }
  const remove = async () => {
    if (!w) return
    try { await api.del(`${V1}/watches/${w.id}`); toast('Deleted'); announceChange(); navigate('/') } catch (e) { toast(errMsg(e), 'err') }
  }
  const feedURL = settings?.feed_url && w ? settings.feed_url.replace(/\.xml$/, `/${w.id}.xml`) : ''

  if (missing) return <Empty title="This watch is not here" icon="eye-off">It may have been deleted. <Link to="/">Back to your watches</Link></Empty>
  if (!w) return <Loading />

  const st = watchState(w)
  const more: MenuItem[] = [
    { label: 'Duplicate', icon: 'copy', onSelect: duplicate },
    ...(feedURL ? [{ label: 'Copy the RSS feed of this watch', icon: 'rss', onSelect: async () => toast(await copyText(feedURL) ? 'Feed address copied' : 'Could not copy', 'ok') }] : []),
    { label: 'Mark all as seen', icon: 'check', disabled: !w.unseen, onSelect: markSeen },
    { label: 'Open the page', icon: 'external', onSelect: () => window.open(w.url, '_blank', 'noopener') },
    'sep',
    { label: 'Delete', icon: 'trash', danger: true, onSelect: () => setConfirm(true) },
  ]
  const tags = w.tags ? w.tags.split(',').map(t => t.trim()).filter(Boolean) : []

  return (
    <>
      <header className="wp-head">
        <SiteIcon host={w.host} size="lg" />
        <div className="wp-title">
          <h1>{w.name}</h1>
          <a className="wp-url ellipsis" href={w.url} target="_blank" rel="noreferrer noopener" title={w.url}>{w.url.replace(/^https?:\/\//, '')}<Icon name="external" size={12} /></a>
          <div className="wp-status">
            <StatusDot w={w} /><span>{stateText(w)}</span>
            <span className="wp-sep">·</span><span>{checkLine(w)}</span>
            {!w.paused && w.next_check_at && st !== 'checking' && <><span className="wp-sep">·</span><span>next {until(w.next_check_at)}</span></>}
          </div>
          {tags.length > 0 && <div className="wp-tags">{tags.map(t => <Link key={t} to={`/?tag=${encodeURIComponent(t)}`} className="badge tag">{t}</Link>)}</div>}
        </div>
        <div className="wp-actions">
          <button type="button" className="btn primary" onClick={checkNow} disabled={checking || st === 'checking'} title="Check now (r)">
            {checking || st === 'checking' ? <Spinner /> : <Icon name="refresh" size={15} />}Check now
          </button>
          <button type="button" className="btn" onClick={() => setPaused(!w.paused)} disabled={busy}>
            <Icon name={w.paused ? 'play' : 'pause'} size={15} />{w.paused ? 'Resume' : 'Pause'}
          </button>
          <Link to={`/watches/${w.id}/edit`} className="btn"><Icon name="edit" size={15} />Edit</Link>
          <MenuButton label="More" className="btn icon-only" align="right" items={more} width={250}><Icon name="more" size={16} /></MenuButton>
        </div>
      </header>

      {st === 'error' && (
        <div className="callout bad mb"><Icon name="alert" size={15} /><div>
          <b>The page could not be read{w.error_count > 1 ? ` (${plural(w.error_count, 'check')} in a row)` : ''}.</b> {w.last_error}
          {/HTTP 40[13]|refused|robot|429|JavaScript/i.test(w.last_error) && w.fetch_mode !== 'browser' && <> Sites that block robots often work with the browser: <Link to={`/watches/${w.id}/edit`}>change how it is read</Link>.</>}
          {/not there any more/i.test(w.last_error) && <> The page may have been redesigned: <Link to={`/watches/${w.id}/edit`}>pick the part again</Link>.</>}
        </div></div>
      )}
      {w.paused && (
        <div className="callout info mb"><Icon name="pause" size={15} /><div className="grow">This watch is paused: the page is not checked until you resume it.</div>
          <button type="button" className="btn sm" onClick={() => setPaused(false)} disabled={busy}><Icon name="play" size={13} />Resume</button></div>
      )}

      <div className="wp-tabs"><Tabs tabs={TABS} value={tab} onChange={t => { const n = new URLSearchParams(sp); if (t === 'changes') n.delete('tab'); else n.set('tab', t); setSp(n, { replace: true }) }} /></div>

      {tab === 'changes' && <ChangesTab w={w} rev={rev} onSeen={load} />}
      {tab === 'now' && <NowTab w={w} rev={rev} />}
      {tab === 'checks' && <ChecksTab w={w} rev={rev} />}
      {tab === 'about' && <AboutTab w={w} />}

      {confirm && <Confirm title={`Delete ${w.name}?`} message="The watch and every change it recorded are deleted. This cannot be undone." confirmLabel="Delete" onCancel={() => setConfirm(false)} onConfirm={() => { setConfirm(false); remove() }} />}
    </>
  )
}

// ---------- changes ----------

function ChangesTab({ w, rev, onSeen }: { w: Watch; rev: number; onSeen: () => void }) {
  const navigate = useNavigate()
  const [all, setAll] = useState(false)
  const [items, setItems] = useState<Change[] | null>(null)
  const [more, setMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const PAGE = 30
  const base = `${V1}/watches/${w.id}/changes?limit=${PAGE}${all ? '&all=1' : ''}`
  useEffect(() => {
    api.get<Change[]>(base).then(cs => { setItems(cs); setMore(cs.length === PAGE) }).catch(() => setItems([]))
  }, [base, rev])
  // looking at the list counts as seeing the changes
  const unseen = w.unseen
  useEffect(() => {
    if (!unseen) return
    const t = window.setTimeout(() => { api.post(`${V1}/changes/seen`, { watch_id: w.id }).then(() => { onSeen(); announceChange() }).catch(() => {}) }, 2500)
    return () => window.clearTimeout(t)
  }, [w.id, unseen, onSeen])
  const older = async () => {
    if (!items?.length) return
    setLoadingMore(true)
    try { const cs = await api.get<Change[]>(`${base}&before=${items[items.length - 1].id}`); setItems([...items, ...cs]); setMore(cs.length === PAGE) } finally { setLoadingMore(false) }
  }
  return (
    <Card title="Changes" sub={w.changes ? plural(w.changes, 'change') + ' recorded' : undefined}
      actions={<label className="row small muted wp-switch">Show changes that did not count<Switch size="sm" checked={all} onChange={setAll} label="Show changes that did not count" /></label>} flush>
      {items == null ? <Loading /> : items.length === 0
        ? <Empty title="No changes yet" icon="activity">{w.last_checked_at ? 'The page has said the same since the first look.' : 'The first check sets what the page says now; changes count from then on.'}</Empty>
        : (
          <>
            <ul className="wp-changes">
              {items.map(c => (
                <li key={c.id} className={`wp-change ${c.matched ? '' : 'nocount'} ${!c.seen && c.matched ? 'unseen' : ''} ${c.dismissed ? 'dismissed' : ''}`}
                  onClick={() => navigate(`/changes/${c.id}`)} onKeyDown={e => { if (e.key === 'Enter') navigate(`/changes/${c.id}`) }} tabIndex={0}>
                  <span className="wp-cdot" aria-hidden />
                  <div className="grow">
                    <div className="wp-csum"><Summary c={c} /></div>
                    {c.reason && <div className="wp-creason">{c.matched ? '' : 'Did not count: '}{c.reason}</div>}
                    <div className="wp-cmeta"><span title={fmtDateTime(c.at)}>{fmtDateTime(c.at)} · {ago(c.at)}</span>{c.dismissed && <span className="badge">not important</span>}</div>
                  </div>
                  <Counts added={c.added} removed={c.removed} />
                  <Icon name="chevron-right" size={15} className="faint" />
                </li>
              ))}
            </ul>
            {more && <div className="wp-more"><button type="button" className="btn sm" onClick={older} disabled={loadingMore}>{loadingMore ? <Spinner /> : null}Load older changes</button></div>}
          </>
        )}
    </Card>
  )
}

// ---------- now ----------

function NowTab({ w, rev }: { w: Watch; rev: number }) {
  const [data, setData] = useState<{ snapshot: Snapshot | null; history: Snapshot[] } | null>(null)
  const [view, setView] = useState<'text' | 'picture'>('text')
  useEffect(() => { api.get<{ snapshot: Snapshot | null; history: Snapshot[] }>(`${V1}/watches/${w.id}/snapshot`).then(setData).catch(() => setData({ snapshot: null, history: [] })) }, [w.id, rev])
  if (!data) return <Loading />
  const sn = data.snapshot
  if (!sn) return <Card><Empty title="Not read yet" icon="eye">{w.paused ? 'Resume the watch to read the page.' : 'The page is read at the first check.'}</Empty></Card>
  const lines = sn.body ? sn.body.split('\n').length : 0
  return (
    <Card title="What the page says" sub={`As of ${fmtDateTime(sn.taken_at)} (${ago(sn.taken_at)}) · ${plural(lines, 'line')}${w.selector ? ' of the part you picked' : ''}`}
      actions={sn.has_shot ? <Segmented size="sm" label="Show" value={view} onChange={setView} options={[{ value: 'text', label: 'Text' }, { value: 'picture', label: 'Picture' }]} /> : undefined}>
      {view === 'picture' && sn.has_shot
        ? <img className="wp-shot" src={`${V1}/snapshots/${sn.id}/shot`} alt={`The page as of ${fmtDateTime(sn.taken_at)}`} loading="lazy" />
        : <pre className="text-pre wp-text">{sn.body || '(empty)'}</pre>}
      <p className="muted small mt-sm">This is the text the last change was found in; when only dates, times or counters move, the newest check is not stored again.</p>
    </Card>
  )
}

// ---------- checks ----------

function ChecksTab({ w, rev }: { w: Watch; rev: number }) {
  const [checks, setChecks] = useState<Check[] | null>(null)
  useEffect(() => { api.get<Check[]>(`${V1}/watches/${w.id}/checks`).then(setChecks).catch(() => setChecks([])) }, [w.id, rev])
  if (!checks) return <Loading />
  if (!checks.length) return <Card><Empty title="No checks yet" icon="clock" /></Card>
  const failed = checks.filter(c => !c.ok).length
  const okMS = checks.filter(c => c.ok).map(c => c.ms)
  const avg = okMS.length ? Math.round(okMS.reduce((a, b) => a + b, 0) / okMS.length) : 0
  return (
    <Card title="Last checks" sub={`${plural(checks.length, 'check')} · ${failed ? `${failed} failed · ` : ''}${avg ? `about ${avg} ms each` : ''}`} flush>
      <div className="wp-chart-wrap"><TimingChart checks={checks} /></div>
      <div className="wp-table-wrap">
        <table className="table compact wp-checks">
          <thead><tr><th>When</th><th>Result</th><th className="wp-hide-sm">Read with</th><th className="num">Time</th><th className="num wp-hide-sm">Size</th><th className="num wp-hide-sm">HTTP</th></tr></thead>
          <tbody>
            {checks.map(c => (
              <tr key={c.id} className={c.ok ? '' : 'wp-fail'}>
                <td title={fmtDateTime(c.at)}>{ago(c.at)}</td>
                <td>
                  {!c.ok ? <span className="wp-res bad" title={c.error}><Icon name="x-circle" size={14} /><span className="ellipsis">{c.error || 'failed'}</span></span>
                    : c.changed ? <span className="wp-res changed"><Icon name="zap" size={14} />changed</span>
                    : <span className="wp-res ok"><Icon name="check-circle" size={14} />same</span>}
                </td>
                <td className="wp-hide-sm">{c.via === 'browser' ? 'Browser' : 'Fast'}</td>
                <td className="num">{c.ms ? `${c.ms} ms` : '—'}</td>
                <td className="num wp-hide-sm">{c.bytes ? fmtBytes(c.bytes) : '—'}</td>
                <td className="num wp-hide-sm">{c.status || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  )
}

/** Response time per check, oldest left; failures are red full-height bars. */
function TimingChart({ checks }: { checks: Check[] }) {
  const pts = [...checks].reverse().slice(-60)
  const maxMS = Math.max(1, ...pts.filter(c => c.ok).map(c => c.ms))
  const W = 600, H = 56, gap = 2
  const bw = Math.max(2, (W - gap * (pts.length - 1)) / Math.max(pts.length, 1))
  return (
    <svg className="wp-chart" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={`Response times of the last ${pts.length} checks`}>
      {pts.map((c, i) => {
        const h = c.ok ? Math.max(3, (c.ms / maxMS) * (H - 4)) : H
        return <rect key={c.id} x={i * (bw + gap)} y={H - h} width={bw} height={h} rx={1.5} className={c.ok ? (c.changed ? 'bar changed' : 'bar') : 'bar fail'}><title>{`${fmtDateTime(c.at)} · ${c.ok ? `${c.ms} ms` : c.error || 'failed'}`}</title></rect>
      })}
    </svg>
  )
}

// ---------- about ----------

function whatIsWatched(w: Watch): string {
  const sel = w.selector.split('\n').map(s => s.trim()).filter(Boolean)
  if (!sel.length) return 'The whole page'
  if (sel.length === 1 && sel[0] === '@price') return 'The price and whether it is in stock'
  if (sel.every(s => s.startsWith('$'))) return `JSON: ${sel.join(', ')}`
  return `Part of the page: ${sel.join(', ')}`
}

function AboutTab({ w }: { w: Watch }) {
  const lines = (s: string) => s.split('\n').map(x => x.trim()).filter(Boolean)
  const ignored = lines(w.ignore_text)
  const trig = lines(w.trigger_text)
  const excl = lines(w.exclude)
  return (
    <Card title="About this watch" actions={<Link to={`/watches/${w.id}/edit`} className="btn sm"><Icon name="edit" size={14} />Edit</Link>}>
      <dl className="kv wp-kv">
        <dt>Watches</dt><dd>{whatIsWatched(w)}</dd>
        {excl.length > 0 && <><dt>Left out</dt><dd>{excl.join(', ')}</dd></>}
        <dt>Read with</dt><dd>{FETCH_LABEL[w.fetch_mode] || w.fetch_mode}{w.fetch_mode === 'auto' && w.needs_browser ? ' — this page needs the browser' : ''}{w.screenshot ? ' · keeps a picture' : ''}</dd>
        <dt>How often</dt><dd>{everyLabel(w.interval_minutes)}{w.paused ? ' (paused)' : ''}</dd>
        <dt>Counts</dt><dd>{NOTIFY_ON_LABEL[w.notify_on] || w.notify_on}</dd>
        <dt>Noise filter</dt><dd>{w.noise_filter ? 'On — dates, times and counters are ignored' : 'Off — every character counts'}</dd>
        <dt>Ignored lines</dt><dd>{ignored.length ? plural(ignored.length, 'rule') + ': ' + ignored.slice(0, 5).join(' · ') + (ignored.length > 5 ? ' …' : '') : 'None'}</dd>
        <dt>Only when</dt><dd>{trig.length ? `a changed line mentions ${trig.map(t => `“${t}”`).join(' or ')}` : 'Always'}</dd>
        <dt>AI condition</dt><dd>{w.ai_condition || 'None'}</dd>
        <dt>You hear</dt><dd>{NOTIFY_LABEL[w.notify_mode] || w.notify_mode}</dd>
        {w.source && <><dt>Added by</dt><dd>Pocket {w.source.charAt(0).toUpperCase() + w.source.slice(1)}</dd></>}
        {w.notes && <><dt>Notes</dt><dd className="text-pre">{w.notes}</dd></>}
        <dt>Added</dt><dd>{fmtDateTime(w.created_at)}</dd>
        <dt>Changes</dt><dd>{w.changes ? `${plural(w.changes, 'change')}, last ${w.last_changed_at ? ago(w.last_changed_at) : '—'}` : 'None yet'}</dd>
      </dl>
    </Card>
  )
}
