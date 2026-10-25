import { atom, read, update } from 'claude-code'
import type { Hook, On } from 'claude-code'
import type { Snapshot } from '../types'

type Engine = Parameters<Hook<'session.start'>>[0]
const snapshot = atom({ plugin: 'argus-bridge', key: 'ui' } as const, null)
const seenMessages = atom({ plugin: 'argus-bridge', key: 'seenMessages' } as const, null)

export type UI = { running: Promise<void> | undefined; timer: { cancel(): void } | undefined }

async function fetchSnapshot($: Engine): Promise<Snapshot | null> {
  const task = await $.env.get('ARGUS_TASK_ID')
  if (!task) return null
  const dir = (await $.env.get('ARGUS_BRIDGE_DIR')) || `${await $.env.get('HOME')}/.argus`
  const port = (await $.fs.read(`${dir}/api-port`)).trim()
  const auth = (await $.fs.read(`${dir}/api-token`)).trim()
  if (!/^\d+$/.test(port) || !auth) return null
  const response = await Promise.race([
    $.http.fetch(`http://127.0.0.1:${port}/api/tasks/${encodeURIComponent(task)}/bridge/ui`, { headers: { Authorization: `Bearer ${auth}` } }),
    $.clock.sleep(2000).then(() => undefined),
  ])
  if (!response?.ok) return null
  const value = JSON.parse(response.text) as Snapshot
  // An older daemon or mismatched endpoint must not publish a malformed atom.
  if (!value.settings || !Array.isArray(value.bindings) || !Array.isArray(value.messages) || typeof value.unread !== 'number') return null
  return value
}

async function load($: Engine): Promise<void> {
  let value: Snapshot | null = null
  try { value = await fetchSnapshot($) } catch { /* fail open */ }
  await update($, snapshot, () => value)
  if (!value) return
  const seen = await read($, seenMessages)
  const known = new Set(seen ?? [])
  const arrivals = seen === null ? [] : value.messages.filter(m => !known.has(m.id))
  for (const message of value.messages) known.add(message.id)
  // Persist before toasting: reloads and a refused toast must not repeat mail.
  await update($, seenMessages, () => [...known])
  if (value.settings.enabled && value.settings.toasts) {
    for (const message of arrivals) await $.ui.toast(`${message.sender}: ${message.tldr}`)
  }
}

export function refreshUI($: Engine, ui: UI): Promise<void> {
  if (ui.running) return ui.running
  ui.running = load($).catch(() => {}).finally(() => { ui.running = undefined })
  return ui.running
}

export function bandText(value: Snapshot): string {
  const roles = value.bindings.map(b => `${b.role} (${b.kind}) · ${b.orchestrator}`).join(' | ')
  const context = value.context?.percent === undefined ? 'context ?' : `context ${Math.round(value.context.percent)}%`
  const budgets = value.bindings.filter(b => b.kind === 'coordinator').map(b => `budget ${value.context?.tokens?.toLocaleString('en-US') ?? '?'} / ${b.budget.toLocaleString('en-US')}`)
  return `${roles} · ${value.unread} unread · ${context}${budgets.length ? ' · ' + budgets.join(' | ') : ''}`
}

export async function startUI($: Engine, ui: UI): Promise<void> {
  await $.command.register({ name: 'hera-inbox', description: 'List unread Hera and task mail without marking it read' })
  await $.command.register({ name: 'hera-status', description: 'Show Hera role, binding, budget and bridge status' })
  ui.timer?.cancel()
  ui.timer = $.clock.every(15000, () => { void refreshUI($, ui) })
  scheduleUI($, ui)
}

export function scheduleUI($: Engine, ui: UI): void {
  $.clock.after(0, () => { void refreshUI($, ui) })
}

function age(sentAt: string, now: number): string {
  const seconds = Math.max(0, Math.floor((now - Date.parse(sentAt)) / 1000))
  if (!Number.isFinite(seconds)) return 'age unknown'
  if (seconds < 60) return `${seconds}s ago`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
  return `${Math.floor(seconds / 86400)}d ago`
}

async function command($: Engine, ui: UI, inbox: boolean): Promise<{ text: string }> {
  await refreshUI($, ui)
  const value = await read($, snapshot)
  if (!value) return { text: 'Argus bridge unavailable. The daemon could not be reached.' }
  if (!value.settings.enabled) return { text: 'Argus bridge disabled.' }
  if (inbox) {
    const now = await $.clock.now()
    return { text: value.messages.length ? value.messages.map(m => `${m.sender} · ${age(m.sent_at, now)} · ${m.tldr}`).join('\n') : 'No unread Hera or task messages.' }
  }
  const roles = value.bindings.map(b => `${b.role} (${b.kind}) · ${b.orchestrator} · ${b.status} · binding ${b.binding_id}`)
  return { text: [...(roles.length ? roles : ['Not Hera-bound.']), `${value.unread} unread`, value.bindings.length ? bandText(value) : `context ${value.context?.percent ?? '?'}%`, `Bridge: ${value.live ? 'live' : 'offline'}`].join('\n') }
}

export function registerUI(on: On, ui: UI): void {
  on('command.run', { command: 'hera-inbox' }, $ => command($, ui, true))
  on('command.run', { command: 'hera-status' }, $ => command($, ui, false))
  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const beneath = await next(e)
    const value = await read($, snapshot)
    if (!value?.settings.enabled || !value.settings.band || value.bindings.length === 0 || e.props.hasSurvey) return beneath
    const { Box, Text } = $.ui.resolve(e)
    return <Box flexDirection="column"><Text wrap="truncate-end">{bandText(value)}</Text>{beneath}</Box>
  })
}
