// Watch a page (/add, three steps) or change a watch (/watches/:id/edit, one page).
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Preview, Settings, Watch, WatchInput } from '../lib/types'
import { Icon } from '../components/icons'
import { announceChange, useCrumb } from '../components/Layout'
import { Loading, PageHeader, Spinner, errMsg, useToast } from '../components/ui'
import { MoreOptions, PageResult, WhatToWatch, WhenToTell, choiceOf, normalizeURL, previewBody, runPreview } from '../components/WatchForm'

const FIELDS: (keyof WatchInput)[] = ['url', 'title', 'tags', 'notes', 'selector', 'exclude', 'fetch_mode', 'browser_wait', 'headers', 'screenshot',
  'noise_filter', 'ignore_text', 'notify_on', 'trigger_text', 'ai_condition', 'notify_mode', 'interval_minutes', 'paused']

function defaults(s: Settings | null): WatchInput {
  return {
    url: '', title: '', tags: '', notes: '', selector: '', exclude: '', fetch_mode: s?.default_fetch_mode || 'auto', browser_wait: 2, headers: '',
    screenshot: false, noise_filter: s?.default_noise ?? true, ignore_text: '', notify_on: 'any', trigger_text: '', ai_condition: '',
    notify_mode: s?.default_notify_mode || 'now', interval_minutes: s?.default_interval || 360, paused: false,
  }
}

function fromWatch(w: Watch): WatchInput {
  const out: WatchInput = {}
  for (const k of FIELDS) (out as Record<string, unknown>)[k] = w[k as keyof Watch]
  return out
}

/** What a watch sends: every field, the address made whole, an unfinished JSON path dropped. */
function payload(f: WatchInput): WatchInput {
  const out: WatchInput = {}
  for (const k of FIELDS) (out as Record<string, unknown>)[k] = f[k]
  out.url = normalizeURL(f.url || '')
  if ((f.selector || '').trim() === '$.') out.selector = ''
  return out
}

/** The first web address in shared text ("Look at this https://…"). */
function urlFrom(text: string): string {
  const m = text.match(/https?:\/\/\S+/)
  return m ? m[0].replace(/[).,]+$/, '') : text.trim()
}

export default function AddWatch() {
  const { id } = useParams()
  return id ? <EditWatch key={id} id={Number(id)} /> : <NewWatch />
}

// ---------- new: three steps ----------

const STEPS = ['Which page?', 'What to watch', 'When to tell you']

function NewWatch() {
  const { settings } = useApp()
  const navigate = useNavigate()
  const { toast } = useToast()
  const [sp] = useSearchParams()
  const shared = urlFrom(sp.get('url') || sp.get('text') || '')
  const [form, setForm] = useState<WatchInput>(() => ({ ...defaults(settings), url: shared, title: sp.get('title') || '' }))
  const set = (patch: WatchInput) => setForm(f => ({ ...f, ...patch }))
  const [step, setStep] = useState(0)
  const [page, setPage] = useState<Preview | null>(null)
  const [looking, setLooking] = useState(false)
  const [lookErr, setLookErr] = useState('')
  const [more, setMore] = useState(false)
  const [saving, setSaving] = useState(false)
  const titled = useRef(!!form.title)
  useCrumb('Watch a page')

  // the defaults arrive with the settings
  const seeded = useRef(!!settings)
  useEffect(() => {
    if (settings && !seeded.current) {
      seeded.current = true
      const d = defaults(settings)
      setForm(f => ({ ...f, fetch_mode: d.fetch_mode, noise_filter: d.noise_filter, notify_mode: d.notify_mode, interval_minutes: d.interval_minutes }))
    }
  }, [settings])

  const look = async () => {
    const url = normalizeURL(form.url || '')
    if (!url) return
    setLooking(true); setLookErr('')
    try {
      const p = await runPreview(previewBody({ ...form, url }))
      setPage(p)
      setForm(f => ({ ...f, url, selector: '', exclude: '', ...(!titled.current && p.title ? { title: p.title } : {}) }))
    } catch (e) { setLookErr(errMsg(e)) } finally { setLooking(false) }
  }
  // a shared address is looked at right away
  useEffect(() => { if (shared) look() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const canNext = [!!page, !!page && (choiceOf(form.selector || '') !== 'json' || (form.selector || '').trim().length > 2), true]
  const save = async () => {
    setSaving(true)
    try {
      const w = await api.post<Watch>(`${V1}/watches`, payload(form))
      toast(`Watching ${w.name}. The first check runs now.`)
      announceChange()
      navigate(`/watches/${w.id}`)
    } catch (e) { toast(errMsg(e), 'err') } finally { setSaving(false) }
  }

  return (
    <div className="wf-page">
      <PageHeader title="Watch a page" sub="Lookout reads the page on a schedule and tells you when something changes that matters." />
      <ol className="wf-steps" aria-label="Steps">
        {STEPS.map((s, i) => (
          <li key={s}>
            <button type="button" className={`wf-step ${i === step ? 'active' : ''} ${i < step ? 'done' : ''}`} aria-current={i === step ? 'step' : undefined}
              disabled={i > step && !canNext.slice(0, i).every(Boolean)} onClick={() => setStep(i)}>
              <span className="wf-step-n">{i < step ? <Icon name="check" size={12} strokeWidth={2.6} /> : i + 1}</span><span className="wf-step-l">{s}</span>
            </button>
          </li>
        ))}
      </ol>

      {step === 0 && (
        <section className="card wf-card">
          <div className="card-body">
            <label className="field">
              <span className="label">The page’s address</span>
              <div className="url-row">
                <input autoFocus inputMode="url" autoCapitalize="off" autoCorrect="off" spellCheck={false} value={form.url || ''} placeholder="www.example.com/page"
                  onChange={e => set({ url: e.target.value })} onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); look() } }} />
                <button type="button" className="btn primary" onClick={look} disabled={looking || !(form.url || '').trim()}>
                  {looking ? <Spinner size={13} /> : <Icon name="eye" size={15} />}{page ? 'Look again' : 'Look at it'}
                </button>
              </div>
              <span className="help">Paste any web address — a shop, a news page, an appointment calendar or a JSON API.</span>
            </label>
            {lookErr && <p className="bad small mt-sm">{lookErr}</p>}
            {page && <div className="mt"><PageResult p={page} /></div>}
          </div>
        </section>
      )}

      {step === 1 && page && (
        <section className="card wf-card">
          <div className="card-body"><WhatToWatch key={page.url + page.final_url} form={form} page={page} onChange={set} /></div>
        </section>
      )}

      {step === 2 && (
        <section className="card wf-card">
          <div className="card-body">
            <WhenToTell form={form} onChange={set} />
            <MoreOptions form={form} onChange={set} open={more} setOpen={setMore} />
          </div>
        </section>
      )}

      <div className="wf-actions">
        {step > 0 ? <button type="button" className="btn" onClick={() => setStep(step - 1)}><Icon name="chevron-left" size={15} />Back</button>
          : <button type="button" className="btn ghost" onClick={() => navigate(-1)}>Cancel</button>}
        <span className="grow" />
        {step < 2
          ? <button type="button" className="btn primary" disabled={!canNext[step]} onClick={() => setStep(step + 1)}>
            {step === 0 && page?.error ? 'Continue anyway' : 'Next'}<Icon name="chevron-right" size={15} />
          </button>
          : <button type="button" className="btn primary" disabled={saving || !(form.url || '').trim()} onClick={save}>
            {saving ? <Spinner size={13} /> : <Icon name="eye" size={15} />}Start watching
          </button>}
      </div>
    </div>
  )
}

// ---------- edit: one page ----------

function describe(sel: string): string {
  const c = choiceOf(sel)
  if (c === 'page') return 'The whole page'
  if (c === 'price') return 'Price and stock'
  if (c === 'json') return `A value in the data: ${sel.split('\n').join(', ')}`
  const n = sel.split('\n').filter(s => s.trim()).length
  return n === 1 ? 'One part of the page' : `${n} parts of the page`
}

function EditWatch({ id }: { id: number }) {
  const navigate = useNavigate()
  const { toast } = useToast()
  const [watch, setWatch] = useState<Watch | null>(null)
  const [loadErr, setLoadErr] = useState('')
  const [form, setForm] = useState<WatchInput>({})
  const set = (patch: WatchInput) => setForm(f => ({ ...f, ...patch }))
  const [page, setPage] = useState<Preview | null>(null)
  const [looking, setLooking] = useState(false)
  const [lookErr, setLookErr] = useState('')
  const [picking, setPicking] = useState(false)
  const [more, setMore] = useState(false)
  const [saving, setSaving] = useState(false)
  useCrumb(watch?.name)

  useEffect(() => {
    api.get<Watch>(`${V1}/watches/${id}`).then(w => {
      setWatch(w)
      setForm(fromWatch(w))
      setMore(!!(w.ignore_text || w.headers || w.fetch_mode !== 'auto' || w.screenshot || !w.noise_filter || w.paused))
    }).catch(e => setLoadErr(errMsg(e)))
  }, [id])

  const look = async (): Promise<Preview | null> => {
    const url = normalizeURL(form.url || '')
    if (!url) return null
    setLooking(true); setLookErr('')
    try {
      const p = await runPreview(previewBody({ ...form, url }))
      setPage(p)
      set({ url })
      return p
    } catch (e) { setLookErr(errMsg(e)); return null } finally { setLooking(false) }
  }
  const startPicking = async () => {
    if (!page && !(await look())) return
    setPicking(true)
  }

  if (loadErr) return <div className="callout warn"><Icon name="alert" size={15} />{loadErr}</div>
  if (!watch) return <Loading />

  const reset = normalizeURL(form.url || '') !== watch.url || (form.selector || '') !== watch.selector || (form.exclude || '') !== watch.exclude
  const save = async () => {
    setSaving(true)
    try {
      await api.put<Watch>(`${V1}/watches/${id}`, payload(form))
      toast('Saved')
      announceChange()
      navigate(`/watches/${id}`)
    } catch (e) { toast(errMsg(e), 'err') } finally { setSaving(false) }
  }

  return (
    <div className="wf-page">
      <PageHeader title="Change this watch" sub={watch.name} />

      <section className="card wf-card">
        <div className="card-head"><div className="grow"><h2>Which page?</h2></div>{looking && <Spinner />}</div>
        <div className="card-body">
          <div className="url-row">
            <input inputMode="url" autoCapitalize="off" autoCorrect="off" spellCheck={false} value={form.url || ''} aria-label="The page’s address"
              onChange={e => set({ url: e.target.value })} onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); look() } }} />
            <button type="button" className="btn" onClick={() => look()} disabled={looking || !(form.url || '').trim()}><Icon name="eye" size={15} />Look at it again</button>
          </div>
          {lookErr && <p className="bad small mt-sm">{lookErr}</p>}
          {page && <div className="mt"><PageResult p={page} /></div>}
        </div>
      </section>

      <section className="card wf-card">
        <div className="card-head"><div className="grow"><h2>What is watched</h2></div></div>
        <div className="card-body">
          {picking && page
            ? <WhatToWatch form={form} page={page} onChange={set} />
            : <div className="wf-row">
              <div className="grow" style={{ minWidth: 0 }}><div className="wf-row-title">{describe(form.selector || '')}</div>
                {choiceOf(form.selector || '') === 'part' && <code className="wf-sel">{(form.selector || '').split('\n').join('  ·  ')}</code>}
              </div>
              <button type="button" className="btn" onClick={startPicking} disabled={looking}>{looking ? <Spinner size={13} /> : <Icon name="crosshair" size={15} />}Change what is watched</button>
            </div>}
        </div>
      </section>

      <section className="card wf-card">
        <div className="card-head"><div className="grow"><h2>When to tell you</h2></div></div>
        <div className="card-body">
          <WhenToTell form={form} onChange={set} watchId={id} hasChanges={watch.changes > 0} />
          <MoreOptions form={form} onChange={set} open={more} setOpen={setMore} />
        </div>
      </section>

      {reset && <div className="callout warn wf-reset"><Icon name="info" size={15} /><span>You changed the address or what is watched: the next check becomes the new starting point. The changes found so far stay.</span></div>}

      <div className="wf-actions">
        <button type="button" className="btn ghost" onClick={() => navigate(`/watches/${id}`)}>Cancel</button>
        <span className="grow" />
        <button type="button" className="btn primary" disabled={saving || !(form.url || '').trim()} onClick={save}>{saving ? <Spinner size={13} /> : <Icon name="check" size={15} />}Save</button>
      </div>
    </div>
  )
}
