// Pieces every Lookout page uses: a site's tile, a watch's status, a change's diff.
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, everyLabel, hostColor } from '../lib/format'
import type { AIStatus, Change, DiffLine, Watch } from '../lib/types'
import { Icon } from './icons'
import { CoreHint } from './ui'

/** A letter tile in a colour of the site's own (no favicon lookups elsewhere). */
export function SiteIcon({ host, size = '' }: { host: string; size?: '' | 'sm' | 'lg' }) {
  const h = host.replace(/^www\./, '')
  return <span className={`site-ico ${size}`} style={{ background: hostColor(h) }} aria-hidden>{h.slice(0, 1) || '?'}</span>
}

/** The state that counts for a watch, for the dot and its words. */
export function watchState(w: Watch): 'checking' | 'paused' | 'error' | 'changed' | 'new' | 'ok' {
  if (w.status === 'checking') return 'checking'
  if (w.paused) return 'paused'
  if (w.status === 'error') return 'error'
  if (w.unseen > 0) return 'changed'
  if (w.status === 'new') return 'new'
  return 'ok'
}

export function stateText(w: Watch): string {
  switch (watchState(w)) {
    case 'checking': return 'Checking now…'
    case 'paused': return 'Paused'
    case 'error': return w.last_error || 'The last check failed'
    case 'changed': return w.unseen === 1 ? '1 new change' : `${w.unseen} new changes`
    case 'new': return 'Waiting for the first check'
  }
  return w.last_changed_at ? `Changed ${ago(w.last_changed_at)}` : `No change since it was added`
}

export function StatusDot({ w }: { w: Watch }) {
  const s = watchState(w)
  return <span className={`sdot ${s}`} title={stateText(w)} />
}

/** "every 6 hours · checked 12 min ago" */
export function checkLine(w: Watch): string {
  const parts = [everyLabel(w.interval_minutes)]
  if (w.last_checked_at) parts.push(`checked ${ago(w.last_checked_at)}`)
  return parts.join(' · ')
}

export function Counts({ added, removed }: { added: number; removed: number }) {
  return (
    <span className="counts" title={`${added} lines added, ${removed} gone`}>
      {added > 0 && <span className="a">+{added}</span>}
      {removed > 0 && <span className="r">−{removed}</span>}
    </span>
  )
}

/** The lines of a change: added green, gone red and struck through, unchanged context grey. */
export function DiffView({ diff, only, onIgnore }: { diff: DiffLine[]; only?: 'changes'; onIgnore?: (line: string) => void }) {
  const lines = only === 'changes' ? diff.filter(l => l.op === '+' || l.op === '-') : diff
  if (lines.length === 0) return <p className="muted small">No lines to show.</p>
  return (
    <div className="diff" role="table" aria-label="What changed">
      {lines.map((l, i) => {
        const kind = l.op === '+' ? 'add' : l.op === '-' ? 'del' : l.op === '~' ? 'gap' : 'ctx'
        return (
          <div key={i} className={`diff-line ${kind}`} role="row">
            <span className="op" aria-label={kind === 'add' ? 'added' : kind === 'del' ? 'gone' : ''}>{l.op === '+' ? '+' : l.op === '-' ? '−' : l.op === '~' ? '⋯' : ''}</span>
            <span className="txt">{l.text}
              {onIgnore && (kind === 'add' || kind === 'del') && <button type="button" className="diff-ignore" title="Never count this line again" onClick={() => onIgnore(l.text)}><Icon name="eye-off" size={12} /></button>}
            </span>
          </div>
        )
      })}
    </div>
  )
}

/** One-line summary with its "AI" mark. */
export function Summary({ c }: { c: Pick<Change, 'summary' | 'summary_by'> }) {
  return <>{c.summary}{c.summary_by === 'ai' && <span className="ai-mark" title="Summary written by AI"> <Icon name="sparkles" size={11} />AI</span>}</>
}

/** Which AI writes summaries and judges conditions. */
export function useAIStatus() {
  const [st, setSt] = useState<AIStatus | null>(null)
  useEffect(() => { api.get<AIStatus>(`${V1}/ai/status`).then(setSt).catch(() => {}) }, [])
  return st
}

export function AIStatusLine({ st }: { st: AIStatus | null }) {
  const { settings } = useApp()
  if (!st) return null
  if (st.ready) return <p className="muted small row" style={{ gap: 6 }}><Icon name="sparkles" size={13} />{st.provider === 'pocket' ? 'Pocket AI' : 'Your OpenAI key'} reads each change and decides.</p>
  return (
    <div className="callout info"><Icon name="info" size={15} /><div>
      No AI is connected{st.detail ? ` (${st.detail})` : ''}, so every change counts. <Link to="/settings/ai">Connect an AI</Link>
      {!settings?.core_configured && <CoreHint />}
    </div></div>
  )
}

/** The words for a fetch mode. */
export const FETCH_LABEL: Record<string, string> = { auto: 'Automatic', fast: 'Fast (no JavaScript)', browser: 'Browser (JavaScript)' }
export const NOTIFY_LABEL: Record<string, string> = { now: 'Right away', digest: 'In the daily summary', off: 'Never (just keep the history)' }
export const NOTIFY_ON_LABEL: Record<string, string> = { any: 'Any change', added: 'Only when something is added', removed: 'Only when something goes away' }

/** "Pocket Wishlist": a watch another Pocket app started (it may change or delete it). */
export function SourceBadge({ source }: { source: string }) {
  if (!source) return null
  const name = 'Pocket ' + source.charAt(0).toUpperCase() + source.slice(1)
  return <span className="badge cat" title={`Started by ${name}, which reads it and tells you itself; it may change or delete this watch`}>{name}</span>
}
