// /changes — every change on every watched page, newest first, grouped by day.
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { addDays, fmtDate, fmtISOTime, plural, todayISO, weekday } from '../lib/format'
import type { Change } from '../lib/types'
import { Icon } from '../components/icons'
import { Empty, Loading, PageHeader, Segmented, Spinner, errMsg, useToast } from '../components/ui'
import { Counts, SiteIcon, Summary } from '../components/watch'
import { announceChange, useOnChange, useShell } from '../components/Layout'

const PAGE = 40

/** The local day (YYYY-MM-DD) a change happened on. */
export function localDay(iso: string): string {
  const t = new Date(iso)
  return `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, '0')}-${String(t.getDate()).padStart(2, '0')}`
}

export function dayLabel(day: string): string {
  const today = todayISO()
  if (day === today) return 'Today'
  if (day === addDays(today, -1)) return 'Yesterday'
  return `${weekday(day, true)}, ${fmtDate(day, day.slice(0, 4) !== today.slice(0, 4))}`
}

export default function Changes() {
  const navigate = useNavigate()
  const [sp, setSp] = useSearchParams()
  const { tags, dash } = useShell()
  const { toast } = useToast()
  const unseen = sp.get('unseen') === '1'
  const all = sp.get('all') === '1'
  const tag = sp.get('tag') || ''
  const [items, setItems] = useState<Change[] | null>(null)
  const [more, setMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [pending, setPending] = useState<Change[]>([])
  const [marking, setMarking] = useState(false)
  const itemsRef = useRef<Change[] | null>(null)
  itemsRef.current = items

  const query = useCallback((before = 0) => {
    const q = new URLSearchParams({ limit: String(PAGE) })
    if (unseen) q.set('unseen', '1')
    if (all) q.set('all', '1')
    if (tag) q.set('tag', tag)
    if (before) q.set('before', String(before))
    return `${V1}/changes?${q}`
  }, [unseen, all, tag])

  const load = useCallback(() => {
    api.get<Change[]>(query()).then(cs => { setItems(cs); setMore(cs.length === PAGE); setPending([]) }).catch(e => { setItems([]); toast(errMsg(e), 'err') })
  }, [query, toast])
  useEffect(() => { setItems(null); load() }, [load])
  useOnChange(load)

  // quietly look for newer changes; only put them in place when the reader is at the top
  useEffect(() => {
    const t = window.setInterval(() => {
      const cur = itemsRef.current
      if (!cur) return
      api.get<Change[]>(query()).then(cs => {
        const top = cur.reduce((m, c) => Math.max(m, c.id), 0)
        const fresh = cs.filter(c => c.id > top)
        if (fresh.length === 0) {
          // seen / dismissed flags may have moved: refresh the shown rows in place
          const byId = new Map(cs.map(c => [c.id, c]))
          setItems(list => list && list.map(c => byId.get(c.id) || c))
          return
        }
        const atTop = (document.querySelector('.content')?.scrollTop || 0) < 40
        if (atTop) { setItems(list => [...fresh, ...(list || [])]); setPending([]) } else setPending(fresh)
      }).catch(() => {})
    }, 30_000)
    return () => window.clearInterval(t)
  }, [query])

  const showPending = () => {
    setItems(list => [...pending, ...(list || []).filter(c => !pending.some(p => p.id === c.id))])
    setPending([])
    document.querySelector('.content')?.scrollTo({ top: 0, behavior: 'smooth' })
  }

  const older = () => {
    if (!items?.length) return
    setLoadingMore(true)
    api.get<Change[]>(query(items[items.length - 1].id))
      .then(cs => { setItems(list => [...(list || []), ...cs]); setMore(cs.length === PAGE) })
      .catch(e => toast(errMsg(e), 'err'))
      .finally(() => setLoadingMore(false))
  }

  const unseenCount = dash?.unseen ?? (items || []).filter(c => !c.seen && c.matched).length
  const markAll = async () => {
    setMarking(true)
    try {
      await api.post(`${V1}/changes/seen`, {})
      setItems(list => list && (unseen ? [] : list.map(c => ({ ...c, seen: true }))))
      announceChange()
    } catch (e) { toast(errMsg(e), 'err') }
    setMarking(false)
  }

  const setParam = (k: string, v: string) => {
    const n = new URLSearchParams(sp)
    if (v) n.set(k, v); else n.delete(k)
    setSp(n, { replace: true })
  }

  // group by local day, keeping the order
  const groups: { day: string; items: Change[] }[] = []
  for (const c of items || []) {
    const d = localDay(c.at)
    const g = groups[groups.length - 1]
    if (g && g.day === d) g.items.push(c)
    else groups.push({ day: d, items: [c] })
  }

  return (
    <div className="chg-page">
      <PageHeader title="Changes" sub={unseenCount ? plural(unseenCount, 'new change') : 'Nothing new'}
        actions={<>
          <Segmented size="sm" label="Which changes" value={unseen ? 'new' : 'all'} onChange={v => setParam('unseen', v === 'new' ? '1' : '')}
            options={[{ value: 'new', label: 'New' }, { value: 'all', label: 'All' }]} />
          <button type="button" className="btn sm" onClick={markAll} disabled={!unseenCount || marking}><Icon name="check" size={14} />Mark all as seen</button>
        </>} />

      <div className="filters chg-filters">
        <button type="button" className={`chip sm ${all ? 'active' : ''}`} aria-pressed={all} onClick={() => setParam('all', all ? '' : '1')}
          title="Also show changes your rules or the AI decided not to tell you about">
          <Icon name="filter" size={13} />Changes that did not count
        </button>
        {tags.length > 0 && <span className="chg-filter-sep" aria-hidden />}
        {tags.map(t => (
          <button key={t.tag} type="button" className={`chip sm ${tag === t.tag ? 'active' : ''}`} aria-pressed={tag === t.tag} onClick={() => setParam('tag', tag === t.tag ? '' : t.tag)}>
            <Icon name="hash" size={12} />{t.tag}
          </button>
        ))}
      </div>

      {pending.length > 0 && (
        <div className="chg-pending-wrap">
          <button type="button" className="chg-pending" onClick={showPending}><Icon name="chevron-up" size={14} />{plural(pending.length, 'new change')} — show</button>
        </div>
      )}

      {items === null ? <Loading /> : items.length === 0 ? (
        unseen
          ? <Empty icon="check" title="You're all caught up">Every change has been looked at. <Link to="/changes">See all changes</Link></Empty>
          : <Empty icon="activity" title={tag ? `No changes on pages tagged “${tag}”` : 'No changes yet'}>
            When a page you watch changes, it shows up here with a short summary of what is different.{' '}
            <Link to="/add">Watch a page</Link>
          </Empty>
      ) : (
        <div className="chg-feed">
          {groups.map(g => (
            <section key={g.day} className="chg-day">
              <h2 className="chg-day-h">{dayLabel(g.day)}<span className="chg-day-n">{g.items.length}</span></h2>
              <div className="chg-list">
                {g.items.map(c => <ChangeRow key={c.id} c={c} onOpen={() => navigate(`/changes/${c.id}`)} />)}
              </div>
            </section>
          ))}
          {more && <div className="chg-more"><button type="button" className="btn" onClick={older} disabled={loadingMore}>{loadingMore ? <Spinner /> : <Icon name="chevron-down" size={15} />}Load older</button></div>}
        </div>
      )}
    </div>
  )
}

/** One change in a list: the page, the summary and the first changed lines. */
export function ChangeRow({ c, onOpen, hideWatch }: { c: Change; onOpen: () => void; hideWatch?: boolean }) {
  const lines = c.diff.filter(l => l.op === '+' || l.op === '-').slice(0, 3)
  const rest = c.added + c.removed - lines.length
  return (
    <article className={`chg-row ${c.matched ? '' : 'not-counted'} ${!c.seen && c.matched ? 'is-new' : ''}`} onClick={onOpen}
      role="link" tabIndex={0} onKeyDown={e => { if (e.key === 'Enter') onOpen() }} aria-label={`${c.watch_name}: ${c.summary}`}>
      <div className="chg-row-head">
        {!hideWatch && <SiteIcon host={c.host} size="sm" />}
        {!hideWatch && <Link to={`/watches/${c.watch_id}`} className="chg-watch ellipsis" onClick={e => e.stopPropagation()}>{c.watch_name}</Link>}
        <span className="chg-time">{hideWatch ? `${fmtDate(localDay(c.at))} ` : ''}{fmtISOTime(c.at)}</span>
        {!c.seen && c.matched && <span className="chg-dot" title="New" aria-label="New" />}
        <span className="spacer" />
        {c.dismissed && <span className="badge" title="You marked this as not important">Not important</span>}
        <Counts added={c.added} removed={c.removed} />
      </div>
      <p className="chg-summary"><Summary c={c} /></p>
      {!c.matched && c.reason && <p className="chg-reason"><Icon name="eye-off" size={12} />Did not count: {c.reason}</p>}
      {lines.length > 0 && (
        <div className="chg-mini" aria-hidden>
          {lines.map((l, i) => <div key={i} className={`chg-mini-line ${l.op === '+' ? 'add' : 'del'}`}><span className="op">{l.op === '+' ? '+' : '−'}</span>{l.text}</div>)}
          {rest > 0 && <div className="chg-mini-more">and {plural(rest, 'more line')}</div>}
        </div>
      )}
    </article>
  )
}
