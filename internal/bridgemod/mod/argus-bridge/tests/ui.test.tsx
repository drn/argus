import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const START = { cwd: '/work', surface: 'terminal', isInteractive: true } as const
const BAND = { plugin: 'argus-bridge', component: 'AbovePrompt', props: { hasSurvey: false, isWorking: false, maxRows: 8, bodyColumns: 100, scroll: { offset: 0, bodyRows: 8 }, view: {} } } as const
const COMMAND = { args: '', origin: { kind: 'composer' } as const, presentation: { isFullscreen: false, columns: 100 } }

function world(on: On) {
  const clock = mock.clock(on, { now: 1_000_000 })
  mock.env(on, { HOME: '/h', ARGUS_TASK_ID: 'T1', ARGUS_BRIDGE_GENERATION: 'launch' })
  let snapshot = { settings: { enabled: true, delivery: true, band: true, toasts: true }, live: true, context: { tokens: 250000, percent: 25, window: 1000000 }, bindings: [{ binding_id: 9, role: 'coord', kind: 'coordinator', orchestrator: 'team', status: 'working', unread: 0, budget: 300000 }], unread: 0, messages: [] as { id: string; sender: string; tldr: string; sent_at: string }[] }
  const toasts: string[] = []
  const submits: string[] = []
  const urls: string[] = []
  const registered: string[] = []
  let unavailable = false
  on('fs.read', (_, e) => ({ value: e.path.endsWith('api-token') ? 'tok' : '7743' }))
  on('http.fetch', (_, e) => { urls.push(e.url); return { value: { status: unavailable ? 503 : 200, ok: !unavailable, headers: {}, text: JSON.stringify(e.url.endsWith('/ui') ? snapshot : {}) } } })
  on('session.id', () => ({ value: 's1' }))
  on('session.model', () => ({ value: 'opus' }))
  on('session.cwd', () => ({ value: '/work' }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('turn.start', (_, e) => ({ turnId: e.turnId }))
  on('turn.complete', (_, e) => ({ text: e.answer }))
  on('session.measure', (_, e) => ({ changed: e.changed }))
  on('command.register', (_, e) => { registered.push(e.name); return { value: { command: e.name } } })
  on('ui.toast', (_, e) => { toasts.push(e.text); return { value: undefined } })
  on('prompt.submit', (_, e) => { submits.push(e.text); return { text: e.text } })
  on('ui.render', ($, e) => { const { Text } = $.ui.resolve(e); return <Text>BENEATH</Text> })
  const refresh = async () => { await clock.advance(15000); await clock.settle(); await clock.settle() }
  return { clock, refresh, snapshot, toasts, submits, urls, registered, fail: () => { unavailable = true } }
}

test('band composes and live flips on/off on every surface', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await w.refresh()
  for (const surface of ['terminal', 'desktop', 'vscode', 'mobile'] as const) {
    const ui = await $.ui.mount({ ...BAND, surface })
    expect((await ui.find({ type: 'Text', text: /coord/ }))?.text).toContain('coord (coordinator)')
    expect((await ui.find({ type: 'Text', text: /coord/ }))?.text).toContain('25%')
    expect((await ui.find({ type: 'Text', text: /coord/ }))?.text).toContain('300,000')
    expect((await ui.find({ type: 'Text', text: /BENEATH/ }))?.text).toBe('BENEATH')
    w.snapshot.settings.band = false
    await w.refresh()
    expect(await ui.find({ type: 'Text', text: /coord/ })).toBeUndefined()
    expect((await ui.find({ type: 'Text', text: /BENEATH/ }))?.text).toBe('BENEATH')
    w.snapshot.settings.band = true
    await w.refresh()
    expect((await ui.find({ type: 'Text', text: /coord/ }))?.text).toContain('coord')
    await ui.unmount()
  }
})

test('unbound, disabled and unavailable band yields beneath; render never fetches', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await w.refresh()
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  const bindings = w.snapshot.bindings
  w.snapshot.bindings = []
  await w.refresh()
  expect(await ui.find({ type: 'Text', text: /coord/ })).toBeUndefined()
  w.snapshot.bindings = bindings
  await w.refresh()
  expect((await ui.find({ type: 'Text', text: /coord/ }))?.text).toContain('coord')
  w.snapshot.settings.enabled = false
  await w.refresh()
  expect(await ui.find({ type: 'Text', text: /coord/ })).toBeUndefined()
  expect((await ui.find({ type: 'Text', text: /BENEATH/ }))?.text).toBe('BENEATH')
  w.snapshot.settings.enabled = true
  await w.refresh()
  expect((await ui.find({ type: 'Text', text: /coord/ }))?.text).toContain('coord')
  w.fail()
  await w.refresh()
  expect(await ui.find({ type: 'Text', text: /coord/ })).toBeUndefined()
  const n = w.urls.length
  await ui.find({ type: 'Text', text: /BENEATH/ })
  expect(w.urls.length).toBe(n)
  await ui.unmount()
})

test('toasts baseline old mail, deduplicate arrivals and honor live switches', async ($, on) => {
  const w = world(on)
  const mail = (id: string) => ({ id, sender: 'worker', tldr: 'Ready for review', sent_at: '1970-01-01T00:15:00Z' })
  w.snapshot.messages = [mail('hera:old')]
  await $.session.start(START)
  await w.refresh()
  expect(w.toasts).toEqual([])
  await $.turn.start({ text: 'busy', turnId: 't1' })
  w.snapshot.messages.push(mail('hera:1'))
  await w.refresh()
  expect(w.toasts.length).toBe(1)
  expect(w.toasts[0]).toContain('worker')
  expect(w.toasts[0]).toContain('Ready for review')
  await w.refresh()
  expect(w.toasts.length).toBe(1)
  w.snapshot.settings.toasts = false
  w.snapshot.messages.push(mail('task:2'))
  await w.refresh()
  expect(w.toasts.length).toBe(1)
  w.snapshot.settings.toasts = true
  await w.refresh()
  expect(w.toasts.length).toBe(1)
  w.snapshot.messages.push(mail('task:3'))
  await w.refresh()
  expect(w.toasts.length).toBe(2)
  w.snapshot.settings.enabled = false
  w.snapshot.messages.push(mail('hera:4'))
  await w.refresh()
  expect(w.toasts.length).toBe(2)
  expect(w.submits).toEqual([])
})

test('local commands list unread mail and status without a model prompt or write', async ($, on) => {
  const w = world(on)
  w.snapshot.messages = [{ id: 'hera:1', sender: 'worker', tldr: 'Review me', sent_at: '1970-01-01T00:15:00Z' }]
  w.snapshot.unread = 1
  await $.session.start(START)
  expect(w.registered).toEqual(['hera-inbox', 'hera-status'])
  for (let i = 0; i < 2; i++) {
    const reply = await $.command.run({ ...COMMAND, command: 'hera-inbox' })
    expect(reply.text).toContain('worker')
    expect(reply.text).toContain('Review me')
    expect(reply.text).toContain('1m ago')
  }
  const reply = await $.command.run({ ...COMMAND, command: 'hera-status' })
  for (const text of ['coord', 'coordinator', 'team', 'working', '9', '1 unread', '25%', '300,000', 'live']) expect(reply.text).toContain(text)
  expect(w.submits).toEqual([])
  expect(w.urls.every(u => u.endsWith('/events') || u.endsWith('/ui'))).toBe(true)
  w.fail()
  expect((await $.command.run({ ...COMMAND, command: 'hera-status' })).text).toContain('unavailable')
})

test('narrow band stays one truncate-end Text and yields to surveys', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await w.refresh()
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal', props: { ...BAND.props, bodyColumns: 12 } })
  const rows = await ui.findAll({ type: 'Text', text: /coord/ })
  expect(rows.length).toBe(1)
  expect(rows[0]?.props.wrap).toBe('truncate-end')
  await ui.redraw({ ...BAND.props, bodyColumns: 12, hasSurvey: true })
  expect(await ui.find({ type: 'Text', text: /coord/ })).toBeUndefined()
  expect((await ui.find({ type: 'Text', text: /BENEATH/ }))?.text).toBe('BENEATH')
  await ui.unmount()
})

test('turn completion and measurements refresh a mounted band before the slow timer', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await w.clock.advance(0)
  await w.clock.settle()
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  w.snapshot.unread = 4
  await $.turn.complete({ turnId: 't1', reason: 'answer', durationMs: 1, isAborted: false, answer: '' })
  await w.clock.advance(0)
  await w.clock.settle()
  expect((await ui.find({ type: 'Text', text: /coord/ }))?.text).toContain('4 unread')
  w.snapshot.context.percent = 40
  await $.session.measure({ context: { tokens: 400000, percent: 40, window: 1000000 }, rateLimits: [], changed: ['context'] })
  await w.clock.advance(0)
  await w.clock.settle()
  expect((await ui.find({ type: 'Text', text: /coord/ }))?.text).toContain('40%')
  await ui.unmount()
})
