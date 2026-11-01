// /changes/:id — one change: what is different, why it counted, and the page's picture before and after.
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { ago, fmtDateTime } from '../lib/format'
import type { ChangeDetail, DiffLine } from '../lib/types'
import { Icon } from '../components/icons'
import { Empty, Loading, MenuButton, type MenuItem, Segmented, errMsg, modalOpen, useMediaQuery, useToast } from '../components/ui'
import { Counts, DiffView, SiteIcon, Summary } from '../components/watch'
import { announceChange, useCrumb } from '../components/Layout'

type View = 'changes' | 'context' | 'side' | 'picture'
const VIEW_KEY = 'plk-diff-view'

export default function ChangePage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const wide = useMediaQuery('(min-width: 900px)')
  const [d, setD] = useState<ChangeDetail | null>(null)
  const [err, setErr] = useState('')
  const [view, setViewRaw] = useState<View>(() => (localStorage.getItem(VIEW_KEY) as View) || 'changes')
  const setView = (v: View) => { setViewRaw(v); localStorage.setItem(VIEW_KEY, v) }
  useCrumb(d?.change.watch_name)

  const load = useCallback(() => {
    api.get<ChangeDetail>(`${V1}/changes/${id}`).then(x => { setD(x); setErr('') }).catch(e => setErr(errMsg(e)))
  }, [id])
  useEffect(() => { setD(null); load() }, [load])

  // opening a change marks it seen
  useEffect(() => {
    if (!d || d.change.seen || String(d.change.id) !== id) return
    api.post(`${V1}/changes/${d.change.id}/seen`).then(() => announceChange()).catch(() => {})
  }, [d, id])

  const c = d?.change
  const go = useCallback((to: number) => { if (to) navigate(`/changes/${to}`) }, [navigate])
  const open = useCallback(() => { if (c?.watch_url) window.open(c.watch_url, '_blank', 'noopener') }, [c?.watch_url])

  useEffect(() => {
    if (!d) return
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      if (e.key === 'j') { e.preventDefault(); go(d.prev) }
      else if (e.key === 'k') { e.preventDefault(); go(d.next) }
      else if (e.key === 'o') { e.preventDefault(); open() }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [d, go, open])

  if (err) return <Empty icon="alert" title="This change cannot be shown">{err}. <Link to="/changes">All changes</Link></Empty>
  if (!d || !c) return <Loading />

  const dismiss = async (body: Record<string, boolean>, msg: string) => {
    try { await api.post(`${V1}/changes/${c.id}/dismiss`, body); toast(msg); load(); announceChange() } catch (e) { toast(errMsg(e), 'err') }
  }
  const ignoreLine = async (line: string) => {
    try { await api.post(`${V1}/watches/${c.watch_id}/ignore`, { lines: [line] }); toast('Lines like this will not count any more') } catch (e) { toast(errMsg(e), 'err') }
  }
  const menu: MenuItem[] = c.dismissed
    ? [{ label: 'Undo “not important”', icon: 'undo', onSelect: () => dismiss({ undo: true }, 'Marked as important again') }]
    : [
      { label: 'Just this once', icon: 'check', onSelect: () => dismiss({}, 'Marked as not important') },
      { label: 'Never count these lines again', icon: 'eye-off', onSelect: () => dismiss({ ignore: true }, 'These lines will not count any more') },
    ]
  const views: { value: View; label: string; disabled?: boolean; title?: string }[] = [
    { value: 'changes', label: 'Changes only' },
    { value: 'context', label: 'With context' },
  ]
  if (wide) views.push({ value: 'side', label: 'Side by side' })
  if (c.has_shots) views.push({ value: 'picture', label: 'Picture' })
  const shown: View = views.some(v => v.value === view) ? view : 'changes'

  return (
    <div className="chg-detail">
      <div className="chg-detail-head">
        <SiteIcon host={c.host} size="lg" />
        <div className="grow">
          <div className="chg-detail-watch">
            <Link to={`/watches/${c.watch_id}`} className="chg-watch">{c.watch_name}</Link>
            <a href={c.watch_url} target="_blank" rel="noreferrer" className="chg-host" title={c.watch_url}>{c.host}<Icon name="external" size={12} /></a>
          </div>
          <div className="chg-detail-meta">
            <span title={fmtDateTime(c.at)}>{fmtDateTime(c.at)} · {ago(c.at)}</span>
            <Counts added={c.added} removed={c.removed} />
            {c.dismissed && <span className="badge">Not important</span>}
          </div>
        </div>
      </div>

      <h1 className="chg-detail-summary"><Summary c={c} /></h1>
      {c.matched && c.reason && <div className="callout info"><Icon name="info" size={15} /><span>Counted because {c.reason}</span></div>}
      {!c.matched && <div className="callout"><Icon name="eye-off" size={15} /><span>This change did not count{c.reason ? `: ${c.reason}` : ''}. Nobody was told about it.</span></div>}

      <div className="chg-toolbar">
        <div className="chg-nav">
          <button type="button" className="btn sm" disabled={!d.prev} onClick={() => go(d.prev)} title="Older change (j)" aria-label="Older change"><Icon name="chevron-left" size={15} /><span className="chg-nav-l">Older</span></button>
          <button type="button" className="btn sm" disabled={!d.next} onClick={() => go(d.next)} title="Newer change (k)" aria-label="Newer change"><span className="chg-nav-l">Newer</span><Icon name="chevron-right" size={15} /></button>
        </div>
        <span className="spacer" />
        <a className="btn sm" href={c.watch_url} target="_blank" rel="noreferrer" title="Open the page (o)"><Icon name="external" size={14} />Open the page</a>
        <MenuButton label="Not important" className="btn sm" align="right" width={260} items={menu}>
          <Icon name={c.dismissed ? 'undo' : 'eye-off'} size={14} />{c.dismissed ? 'Not important' : 'Not important…'}
        </MenuButton>
      </div>

      <div className="chg-views">
        <Segmented size="sm" label="How to show the change" value={shown} onChange={setView} options={views} />
      </div>

      {shown === 'changes' && <DiffView diff={c.diff} only="changes" onIgnore={ignoreLine} />}
      {shown === 'context' && <DiffView diff={c.diff} onIgnore={ignoreLine} />}
      {shown === 'side' && <SideBySide diff={c.diff} />}
      {shown === 'picture' && c.before_id && c.after_id && <PictureCompare before={c.before_id} after={c.after_id} boxes={c.visual || []} />}

      <p className="muted small chg-hint">
        <Icon name="eye-off" size={12} /> Next to a changed line, the crossed-out eye stops that line from counting again.
      </p>
    </div>
  )
}

/** Before on the left, after on the right; unchanged lines on both sides. */
function SideBySide({ diff }: { diff: DiffLine[] }) {
  type Row = { l?: DiffLine; r?: DiffLine; gap?: string }
  const rows: Row[] = []
  let i = 0
  while (i < diff.length) {
    const l = diff[i]
    if (l.op === '~') { rows.push({ gap: l.text }); i++; continue }
    if (l.op === '=') { rows.push({ l, r: l }); i++; continue }
    const del: DiffLine[] = [], add: DiffLine[] = []
    while (i < diff.length && (diff[i].op === '-' || diff[i].op === '+')) {
      if (diff[i].op === '-') del.push(diff[i]); else add.push(diff[i])
      i++
    }
    for (let k = 0; k < Math.max(del.length, add.length); k++) rows.push({ l: del[k], r: add[k] })
  }
  const cell = (x: DiffLine | undefined) => {
    if (!x) return <div className="sbs-cell sbs-blank" />
    const kind = x.op === '+' ? 'add' : x.op === '-' ? 'del' : 'ctx'
    return <div className={`sbs-cell diff-line ${kind}`}><span className="op">{x.op === '+' ? '+' : x.op === '-' ? '−' : ''}</span><span className="txt">{x.text}</span></div>
  }
  return (
    <div className="diff sbs">
      <div className="sbs-head"><span>Before</span><span>After</span></div>
      {rows.map((r, k) => r.gap
        ? <div key={k} className="diff-line gap sbs-gap"><span className="op">⋯</span><span className="txt">{r.gap}</span></div>
        : <div key={k} className="sbs-row">{cell(r.l)}{cell(r.r)}</div>)}
    </div>
  )
}

/** The page's picture before and after, with a slider between them and the changed areas outlined. */
function PictureCompare({ before, after, boxes }: { before: number; after: number; boxes: [number, number, number, number][] }) {
  const [pos, setPos] = useState(50)
  const [showBoxes, setShowBoxes] = useState(true)
  const ref = useRef<HTMLDivElement>(null)
  const drag = useRef(false)
  const move = (clientX: number) => {
    const r = ref.current?.getBoundingClientRect()
    if (!r || r.width === 0) return
    setPos(Math.min(100, Math.max(0, ((clientX - r.left) / r.width) * 100)))
  }
  return (
    <div className="pic-wrap">
      <div className="pic-bar">
        <span className="muted small"><b>Before</b> on the left, <b>after</b> on the right — drag to compare.</span>
        <span className="spacer" />
        {boxes.length > 0 && (
          <button type="button" className={`chip sm ${showBoxes ? 'active' : ''}`} aria-pressed={showBoxes} onClick={() => setShowBoxes(b => !b)}>
            <Icon name="crosshair" size={13} />Show changed areas
          </button>
        )}
      </div>
      <div className="pic" ref={ref}
        onPointerDown={e => { drag.current = true; (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId); move(e.clientX) }}
        onPointerMove={e => { if (drag.current) move(e.clientX) }}
        onPointerUp={() => { drag.current = false }} onPointerCancel={() => { drag.current = false }}>
        <img src={`${V1}/snapshots/${after}/shot`} alt="The page after the change" className="pic-after" draggable={false} />
        <img src={`${V1}/snapshots/${before}/shot`} alt="The page before the change" className="pic-before" draggable={false} style={{ clipPath: `inset(0 ${100 - pos}% 0 0)` }} />
        {showBoxes && boxes.map((b, i) => (
          <span key={i} className="pic-box" style={{ left: `${b[0] * 100}%`, top: `${b[1] * 100}%`, width: `${b[2] * 100}%`, height: `${b[3] * 100}%` }} />
        ))}
        <div className="pic-handle" style={{ left: `${pos}%` }}
          role="slider" aria-label="Before and after" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pos)} tabIndex={0}
          onKeyDown={e => { if (e.key === 'ArrowLeft') setPos(p => Math.max(0, p - 5)); if (e.key === 'ArrowRight') setPos(p => Math.min(100, p + 5)) }}>
          <span className="pic-knob"><Icon name="columns" size={14} /></span>
        </div>
      </div>
      {boxes.length === 0 && <p className="muted small mt-sm">The pictures look the same; only the text changed.</p>}
    </div>
  )
}
