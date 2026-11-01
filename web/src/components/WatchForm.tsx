// The parts of the "watch a page" form: what the page looks like, what is
// watched on it (the whole page, picked parts, the price, a JSON value) and
// when Lookout tells you. AddWatch puts them together as steps (new) or as
// one page (edit).
import { type ReactNode, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { INTERVALS, everyLabel } from '../lib/format'
import type { Change, Preview, Settings, WatchInput } from '../lib/types'
import { Icon } from './icons'
import { useShell } from './Layout'
import { Select } from './Select'
import { Field, Segmented, Spinner, Switch, errMsg, useDebounce } from './ui'
import { AIStatusLine, FETCH_LABEL, NOTIFY_LABEL, NOTIFY_ON_LABEL, SiteIcon, useAIStatus } from './watch'

export type Choice = 'page' | 'part' | 'price' | 'json'

/** What a selector means, for the choice cards. */
export function choiceOf(selector: string): Choice {
  const s = selector.trim()
  if (!s) return 'page'
  if (s === '@price') return 'price'
  if (s.startsWith('$')) return 'json'
  return 'part'
}

/** "example.com" → "https://example.com". */
export function normalizeURL(raw: string): string {
  const u = raw.trim()
  if (!u) return ''
  return /^[a-z][a-z0-9+.-]*:\/\//i.test(u) ? u : `https://${u}`
}

/** The body sent to /preview for a watch's reading settings. */
export function previewBody(f: WatchInput, extra: Record<string, unknown> = {}) {
  return { url: normalizeURL(f.url || ''), fetch_mode: f.fetch_mode || 'auto', headers: f.headers || '', browser_wait: f.browser_wait || 2, ...extra }
}

export const runPreview = (body: Record<string, unknown>) => api.post<Preview>(`${V1}/preview`, body)

/** A plain hint for a preview error. */
export function errorHint(err: string, settings: Settings | null): string {
  const e = err.toLowerCase()
  if (e.includes('private network')) return 'This address is on your own network. An admin can allow that with FETCH_PRIVATE=true.'
  if (e.includes('no browser') || e.includes('browser_url')) return 'No browser is set up on this server. An admin can add one with BROWSER_URL (for example a sockpuppetbrowser or browserless container).'
  if (e.includes('does not resolve')) return 'Check the spelling of the address.'
  if (e.includes('http 403') || e.includes('http 401') || e.includes('refused') || e.includes('http 429') || e.includes('http 503')) {
    return settings?.browser
      ? 'The site turned a robot away. Try “How to read the page → Browser” under More options.'
      : 'The site turns robots away. With a browser set up (an admin sets BROWSER_URL) Lookout can read it like a person would.'
  }
  if (e.includes('not there any more') || e.includes('picked')) return 'The part you picked is not on the page. Pick it again, or watch the whole page.'
  if (e.includes('too long')) return 'The site is slow right now. Try again in a moment.'
  return ''
}

export function PreviewError({ err }: { err: string }) {
  const { settings } = useApp()
  const hint = errorHint(err, settings)
  return <div className="callout warn"><Icon name="alert" size={15} /><div><b>{err}</b>{hint && <div className="mt-sm">{hint}</div>}</div></div>
}

// ---------- step 1: the page ----------

const KIND_LABEL: Record<string, string> = { html: 'Web page', json: 'JSON data', text: 'Plain text', other: 'A file' }

export function PageResult({ p }: { p: Preview }) {
  const host = (() => { try { return new URL(p.final_url || p.url).hostname } catch { return p.url } })()
  return (
    <div className="wf-result">
      <div className="row top">
        <SiteIcon host={host} size="lg" />
        <div className="grow" style={{ minWidth: 0 }}>
          <b className="wf-result-title">{p.title || host}</b>
          <div className="muted small ellipsis" title={p.final_url}>{p.final_url || p.url}</div>
          <div className="wf-facts">
            {p.kind && <span className="badge">{KIND_LABEL[p.kind] || p.kind}</span>}
            {p.lines > 0 && <span className="badge">{p.lines.toLocaleString()} lines of text</span>}
            {p.via && <span className="badge">{p.via === 'browser' ? 'Read with the browser' : 'Read without a browser'}</span>}
            {p.ms > 0 && <span className="muted small">{(p.ms / 1000).toFixed(1)} s</span>}
          </div>
        </div>
      </div>
      {p.needs_browser && <p className="muted small row mt-sm" style={{ gap: 6 }}><Icon name="info" size={13} />Read with the browser because the page needs JavaScript.</p>}
      {p.error && <div className="mt-sm"><PreviewError err={p.error} /></div>}
      {p.shot && <div className="wf-shot"><img src={p.shot} alt="A picture of the page" /></div>}
    </div>
  )
}

// ---------- step 2: what to watch ----------

function firstLines(text: string, n: number) {
  const ls = text.split('\n')
  return ls.slice(0, n).join('\n') + (ls.length > n ? `\n… ${ls.length - n} more lines` : '')
}

/** Choose what is compared: the whole page, picked parts, the price or a JSON value. */
export function WhatToWatch({ form, page, onChange }: { form: WatchInput; page: Preview; onChange: (patch: WatchInput) => void }) {
  const selector = form.selector || ''
  const [choice, setChoice] = useState<Choice>(() => choiceOf(selector))
  const [lastPart, setLastPart] = useState(choice === 'part' ? selector : '')
  const [advanced, setAdvanced] = useState(() => choiceOf(selector) === 'part' && !!(form.exclude || '').trim())

  const pick = (c: Choice) => {
    if (c === choice) return
    if (choice === 'part') setLastPart(selector)
    setChoice(c)
    if (c === 'page') onChange({ selector: '' })
    else if (c === 'price') onChange({ selector: '@price' })
    else if (c === 'json') onChange({ selector: selector.startsWith('$') ? selector : '$.' })
    else onChange({ selector: lastPart })
  }

  const cards: { id: Choice; title: string; sub: string; icon: string; disabled?: boolean; why?: string }[] = [
    { id: 'page', title: 'The whole page', sub: 'Every change in the page’s text counts.', icon: 'globe' },
  ]
  if (page.kind === 'json') {
    cards.push({ id: 'json', title: 'A value in the data', sub: 'One value, like the latest version or a price.', icon: 'braces' })
  } else {
    cards.push({ id: 'part', title: 'One part of the page', sub: 'Pick the part that matters; the rest is left out.', icon: 'crosshair', disabled: page.kind !== 'html' && page.kind !== '', why: 'Only web pages have parts to pick.' })
    cards.push({ id: 'price', title: 'Price and stock', sub: page.has_price ? `Now ${page.price}` : 'This page publishes no price for robots.', icon: 'tag', disabled: !page.has_price && choice !== 'price', why: 'This page publishes no price for robots; pick the price on the page instead.' })
  }

  return (
    <div className="wf-what">
      <div className="choice-grid" role="radiogroup" aria-label="What to watch">
        {cards.map(c => (
          <button key={c.id} type="button" role="radio" aria-checked={choice === c.id} disabled={c.disabled} title={c.disabled ? c.why : undefined}
            className={`choice ${choice === c.id ? 'on' : ''}`} onClick={() => pick(c.id)}>
            <span className="choice-ico"><Icon name={c.icon} size={18} /></span>
            <span className="grow" style={{ minWidth: 0 }}>
              <span className="choice-title">{c.title}</span>
              <span className="choice-sub">{c.sub}</span>
            </span>
            <span className="choice-radio" aria-hidden />
          </button>
        ))}
      </div>

      {choice === 'page' && page.text && <pre className="text-pre wf-sample">{firstLines(page.text, 12)}</pre>}
      {choice === 'part' && <Picker form={form} page={page} onChange={onChange} advanced={advanced} setAdvanced={setAdvanced} />}
      {choice === 'json' && (
        <Field label="Path to the value" help="Like $.data.price or $.releases[0].version — one per line for several values.">
          <textarea rows={2} value={selector} onChange={e => onChange({ selector: e.target.value })} placeholder="$.data.price" spellCheck={false} className="mono-input" />
        </Field>
      )}
      <Extracted form={form} page={page} choice={choice} />
    </div>
  )
}

function Picker({ form, page, onChange, advanced, setAdvanced }: { form: WatchInput; page: Preview; onChange: (patch: WatchInput) => void; advanced: boolean; setAdvanced: (v: boolean) => void }) {
  const [find, setFind] = useState('')
  const q = useDebounce(find.trim(), 500)
  const [found, setFound] = useState<Preview | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const formRef = useRef(form)
  formRef.current = form
  useEffect(() => {
    if (!q) { setFound(null); setErr(''); return }
    let live = true
    setBusy(true)
    runPreview(previewBody(formRef.current, { find: q }))
      .then(p => { if (live) { setFound(p); setErr(p.error || '') } })
      .catch(e => { if (live) setErr(errMsg(e)) })
      .finally(() => { if (live) setBusy(false) })
    return () => { live = false }
  }, [q])
  const blocks = (q && found ? found.blocks : page.blocks) || []
  const picked = (form.selector || '').split('\n').map(s => s.trim()).filter(Boolean)
  const toggle = (sel: string) => {
    const next = picked.includes(sel) ? picked.filter(s => s !== sel) : [...picked, sel].slice(-5)
    onChange({ selector: next.join('\n') })
  }
  const matches = q ? blocks.filter(b => b.found).length : 0
  return (
    <div className="picker">
      <div className="picker-search">
        <Icon name="search" size={16} />
        <input value={find} onChange={e => setFind(e.target.value)} placeholder="Find text on the page, like “Sold out” or a price" aria-label="Find text on the page" />
        {busy && <Spinner />}
      </div>
      {q && !busy && found && <p className="muted small">{matches ? `${matches} part${matches === 1 ? '' : 's'} of the page say “${q}” — they are first below.` : `Nothing on the page says “${q}”.`}</p>}
      {err && <PreviewError err={err} />}
      <p className="muted small">Tap the part you want to watch (up to five).</p>
      {blocks.length === 0
        ? <p className="muted small">Lookout found no parts to suggest on this page. Use “Advanced” below to type a CSS selector, or watch the whole page.</p>
        : <div className="block-list" role="listbox" aria-multiselectable aria-label="Parts of the page">
          {blocks.map(b => {
            const on = picked.includes(b.selector)
            return (
              <button key={b.selector} type="button" role="option" aria-selected={on} className={`block-card ${on ? 'on' : ''} ${b.found ? 'found' : ''}`} onClick={() => toggle(b.selector)}>
                <span className="block-head">
                  <span className="block-label">{b.label}</span>
                  {b.found && <span className="badge accent">match</span>}
                  <span className="grow" />
                  <span className="muted xsmall">{b.lines === 1 ? '1 line' : `${b.lines} lines`}</span>
                  <span className="block-check" aria-hidden>{on && <Icon name="check" size={13} strokeWidth={2.4} />}</span>
                </span>
                <span className="block-text">{b.text}</span>
              </button>
            )
          })}
        </div>}
      <button type="button" className="wf-toggle" aria-expanded={advanced} onClick={() => setAdvanced(!advanced)}>
        <Icon name={advanced ? 'chevron-down' : 'chevron-right'} size={14} />Advanced: type CSS selectors
      </button>
      {advanced && (
        <div className="wf-advanced">
          <Field label="CSS selectors to watch" help="One per line, like #price or .stock-status. XPath is not supported.">
            <textarea rows={3} value={form.selector || ''} onChange={e => onChange({ selector: e.target.value })} spellCheck={false} className="mono-input" placeholder="#price" />
          </Field>
          <Field label="Parts to leave out" help="CSS selectors, one per line — ads, counters, “people also bought”.">
            <textarea rows={2} value={form.exclude || ''} onChange={e => onChange({ exclude: e.target.value })} spellCheck={false} className="mono-input" placeholder=".ads" />
          </Field>
        </div>
      )}
    </div>
  )
}

/** "This is what Lookout will watch": the text that is compared, read again
 *  when the selection changes (the old text stays while it loads). */
function Extracted({ form, page, choice }: { form: WatchInput; page: Preview; choice: Choice }) {
  const key = JSON.stringify([form.url, form.selector, form.exclude, form.fetch_mode, form.headers, choice])
  const dkey = useDebounce(key, 700)
  const [res, setRes] = useState<{ text: string; lines: number; error: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [tick, setTick] = useState(0)
  const formRef = useRef(form)
  formRef.current = form
  const sel = (form.selector || '').trim()
  const needsRead = choice !== 'page' && sel !== '' && sel !== '$.'
  useEffect(() => {
    if (!needsRead) { setRes(null); return }
    let live = true
    setBusy(true)
    runPreview(previewBody(formRef.current, { selector: formRef.current.selector, exclude: formRef.current.exclude }))
      .then(p => { if (live) setRes({ text: p.text, lines: p.lines, error: p.error || '' }) })
      .catch(e => { if (live) setRes({ text: '', lines: 0, error: errMsg(e) }) })
      .finally(() => { if (live) setBusy(false) })
    return () => { live = false }
  }, [dkey, needsRead, tick])
  if (choice === 'page') return null
  return (
    <section className="wf-extract" aria-live="polite">
      <div className="wf-extract-head">
        <b>This is what Lookout will watch</b>
        {busy && <Spinner />}
        <span className="grow" />
        {needsRead && <button type="button" className="btn ghost sm" onClick={() => setTick(t => t + 1)} disabled={busy}><Icon name="refresh" size={13} />Test</button>}
      </div>
      {!needsRead
        ? <p className="muted small">{choice === 'part' ? 'Pick a part of the page above.' : 'Type the path to a value above.'}</p>
        : res?.error ? <PreviewError err={res.error} />
          : res ? <pre className="text-pre wf-sample">{res.text ? firstLines(res.text, 40) : '(no text)'}</pre>
            : <p className="muted small">Reading…</p>}
    </section>
  )
}

// ---------- step 3: when to tell ----------

function intervalOptions(current: number, min: number) {
  const list = INTERVALS.includes(current) || !current ? INTERVALS : [...INTERVALS, current].sort((a, b) => a - b)
  return list.map(m => {
    const label = everyLabel(m)
    return { value: String(m), label: label[0].toUpperCase() + label.slice(1), disabled: m < min, hint: m < min ? 'too often for this server' : undefined }
  })
}

const NOTIFY_SHORT: Record<string, string> = { now: 'Right away', digest: 'Daily summary', off: 'Never' }

/** Name, how often, how to tell, what counts, AI condition, tags, notes. */
export function WhenToTell({ form, onChange, watchId, hasChanges }: { form: WatchInput; onChange: (patch: WatchInput) => void; watchId?: number; hasChanges?: boolean }) {
  const { settings } = useApp()
  const { tags } = useShell()
  const ai = useAIStatus()
  const mine = (form.tags || '').split(',').map(t => t.trim().toLowerCase()).filter(Boolean)
  const toggleTag = (t: string) => {
    const next = mine.includes(t) ? mine.filter(x => x !== t) : [...mine, t]
    onChange({ tags: next.join(', ') })
  }
  const mode = form.notify_mode || 'now'
  return (
    <div className="wf-fields">
      <Field label="Name" help="Leave it empty to use the page’s own title.">
        <input value={form.title || ''} onChange={e => onChange({ title: e.target.value })} placeholder="Visa appointments" />
      </Field>
      <div className="wf-two">
        <Field label="How often" group>
          <Select value={String(form.interval_minutes || 360)} onChange={v => onChange({ interval_minutes: Number(v) })} options={intervalOptions(form.interval_minutes || 360, settings?.min_interval || 5)} ariaLabel="How often" />
        </Field>
        <Field label="Counts as a change" group>
          <Select value={form.notify_on || 'any'} onChange={v => onChange({ notify_on: v as WatchInput['notify_on'] })} options={Object.entries(NOTIFY_ON_LABEL).map(([value, label]) => ({ value, label }))} ariaLabel="Counts as a change" />
        </Field>
      </div>
      <Field label="Tell me" group help={mode === 'digest'
        ? <>One push a day with everything that changed, at the hour set in <Link to="/settings/notifications">Settings → Notifications</Link>.</>
        : NOTIFY_LABEL[mode] + (mode === 'now' && settings && !settings.notify_configured ? ' — set up where pushes go in Settings → Notifications.' : '.')}>
        <Segmented value={mode} onChange={v => onChange({ notify_mode: v })} label="Tell me"
          options={(['now', 'digest', 'off'] as const).map(v => ({ value: v, label: NOTIFY_SHORT[v], title: NOTIFY_LABEL[v] }))} />
      </Field>
      <Field label="Only when a changed line mentions" help="One word or phrase per line (case does not matter); /a regular expression/ works too. Empty = every change counts.">
        <textarea rows={2} value={form.trigger_text || ''} onChange={e => onChange({ trigger_text: e.target.value })} placeholder={'available\n/\\d+ slots?/'} />
      </Field>
      <Field label="AI condition" group help={ai && !ai.ready ? 'Until an AI is connected, every change counts.' : 'Say in your own words when a change matters. The AI reads each change and decides.'}>
        <textarea rows={2} value={form.ai_condition || ''} onChange={e => onChange({ ai_condition: e.target.value })} placeholder="Only if a 2-bedroom under 800 KM shows up" />
        <AIStatusLine st={ai} />
        {watchId && hasChanges && <TryAI watchId={watchId} condition={form.ai_condition || ''} ready={!!ai?.ready} />}
      </Field>
      <Field label="Tags" group help="Comma separated. Tags group your watches in the sidebar.">
        <input value={form.tags || ''} onChange={e => onChange({ tags: e.target.value })} placeholder="shopping, errands" aria-label="Tags" />
        {tags.length > 0 && (
          <div className="chips wf-tag-chips">
            {tags.slice(0, 12).map(t => (
              <button key={t.tag} type="button" className={`chip sm ${mine.includes(t.tag) ? 'active' : ''}`} aria-pressed={mine.includes(t.tag)} onClick={() => toggleTag(t.tag)}>
                <Icon name="hash" size={12} />{t.tag}
              </button>
            ))}
          </div>
        )}
      </Field>
      <Field label="Notes">
        <textarea rows={2} value={form.notes || ''} onChange={e => onChange({ notes: e.target.value })} placeholder="Why you watch it, what to do when it changes" />
      </Field>
    </div>
  )
}

/** Runs the AI condition on the watch's last change. */
function TryAI({ watchId, condition, ready }: { watchId: number; condition: string; ready: boolean }) {
  const [busy, setBusy] = useState(false)
  const [res, setRes] = useState<{ match: boolean | null; reason: string; summary: string } | null>(null)
  const [err, setErr] = useState('')
  const run = async () => {
    setBusy(true); setErr(''); setRes(null)
    try {
      const cs = await api.get<Change[]>(`${V1}/watches/${watchId}/changes?limit=1&all=1`)
      if (!cs.length) { setErr('This watch has no change to try it on yet.'); return }
      const v = await api.post<{ summary: string; match: boolean | null; reason: string }>(`${V1}/ai/try`, { change_id: cs[0].id, condition })
      setRes({ match: v.match, reason: v.reason, summary: v.summary })
    } catch (e) { setErr(errMsg(e)) } finally { setBusy(false) }
  }
  return (
    <div className="wf-try">
      <button type="button" className="btn sm" onClick={run} disabled={busy || !ready || !condition.trim()} title={!ready ? 'Needs an AI (Settings → AI)' : !condition.trim() ? 'Write a condition first' : undefined}>
        {busy ? <Spinner size={12} /> : <Icon name="sparkles" size={13} />}Try it on the last change
      </button>
      {err && <p className="bad small">{err}</p>}
      {res && (
        <div className={`callout ${res.match === false ? 'warn' : 'info'}`}>
          <Icon name={res.match === false ? 'x-circle' : 'check-circle'} size={15} />
          <div><b>{res.match === false ? 'Would not count' : 'Would count'}</b>{res.reason && <> — {res.reason}</>}{res.summary && <div className="muted small mt-sm">{res.summary}</div>}</div>
        </div>
      )}
    </div>
  )
}

// ---------- more options ----------

export function MoreOptions({ form, onChange, open, setOpen }: { form: WatchInput; onChange: (patch: WatchInput) => void; open: boolean; setOpen: (v: boolean) => void }) {
  const { settings } = useApp()
  const browser = !!settings?.browser
  const noBrowser = 'No browser is set up on this server (an admin sets BROWSER_URL)'
  const usesBrowser = form.fetch_mode === 'browser' || (form.fetch_mode === 'auto' && browser)
  return (
    <section className="wf-more">
      <button type="button" className="wf-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
        <Icon name={open ? 'chevron-down' : 'chevron-right'} size={14} />More options
      </button>
      {open && (
        <div className="wf-more-body">
          <Row title="Ignore dates, times and counters" help="“Updated 10:42”, “3 minutes ago”, “1,204 views” never count as a change. Switch it off when dates are what you watch (appointments, timetables, release dates).">
            <Switch checked={form.noise_filter ?? true} onChange={v => onChange({ noise_filter: v })} label="Ignore dates, times and counters" />
          </Row>
          <Field label="Lines that never count" help="One per line: text anywhere in the line, or /a regular expression/.">
            <textarea rows={2} value={form.ignore_text || ''} onChange={e => onChange({ ignore_text: e.target.value })} placeholder={'Sponsored\n/visitors? online/'} />
          </Field>
          <Field label="How to read the page" group help={form.fetch_mode === 'auto' ? 'Fast first; the browser only when the page needs JavaScript or turns robots away.' : form.fetch_mode === 'fast' ? 'Plain download, no JavaScript — quickest and lightest.' : 'A real browser opens the page every time — needed for pages built with JavaScript.'}>
            <Segmented value={form.fetch_mode || 'auto'} onChange={v => onChange({ fetch_mode: v })} label="How to read the page"
              options={(['auto', 'fast', 'browser'] as const).map(v => ({ value: v, label: FETCH_LABEL[v], disabled: v === 'browser' && !browser, title: v === 'browser' && !browser ? noBrowser : undefined }))} />
            {!browser && <p className="muted small">{noBrowser}.</p>}
          </Field>
          {usesBrowser && (
            <Field label="Wait for the page (seconds)" help="How long the browser lets the page settle before reading it.">
              <input type="number" min={0} max={30} value={form.browser_wait ?? 2} onChange={e => onChange({ browser_wait: Math.max(0, Math.min(30, Number(e.target.value) || 0)) })} className="wf-num" />
            </Field>
          )}
          <Row title="Keep a picture with every check" help={browser ? 'The browser takes a picture each time, so you can compare how the page looked.' : noBrowser + '.'}>
            <Switch checked={!!form.screenshot && browser} onChange={v => onChange({ screenshot: v })} disabled={!browser} label="Keep a picture with every check" />
          </Row>
          <Field label="Extra request headers" help="One “Name: value” per line — a cookie or a language for sites that need it.">
            <textarea rows={2} value={form.headers || ''} onChange={e => onChange({ headers: e.target.value })} spellCheck={false} className="mono-input" placeholder="Accept-Language: bs" />
          </Field>
          <Row title="Paused" help="A paused watch is not checked until you resume it.">
            <Switch checked={!!form.paused} onChange={v => onChange({ paused: v })} label="Paused" />
          </Row>
        </div>
      )}
    </section>
  )
}

function Row({ title, help, children }: { title: string; help: string; children: ReactNode }) {
  return (
    <div className="wf-row">
      <div className="grow"><div className="wf-row-title">{title}</div><div className="muted small">{help}</div></div>
      {children}
    </div>
  )
}
