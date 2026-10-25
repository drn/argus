import { atom, read, update } from 'claude-code'
import type { Color, Hook, On } from 'claude-code'
import type { Snapshot } from '../types'

type Engine = Parameters<Hook<'session.start'>>[0]
const snapshot = atom({ plugin: 'argus-bridge', key: 'ui' } as const, null)
const seenMessages = atom({ plugin: 'argus-bridge', key: 'seenMessages' } as const, null)

export type UI = { running: Promise<void> | undefined; timer: { cancel(): void } | undefined }

// fetchSnapshot answers null when there is nothing to show (no Argus task, or
// a daemon too old to serve the shape) and throws when the daemon cannot be
// reached, so the caller can keep the last snapshot and flag it offline.
async function fetchSnapshot($: Engine): Promise<Snapshot | null> {
  const task = await $.env.get('ARGUS_TASK_ID')
  if (!task) return null
  const dir = (await $.env.get('ARGUS_BRIDGE_DIR')) || `${await $.env.get('HOME')}/.argus`
  const port = (await $.fs.read(`${dir}/api-port`)).trim()
  const auth = (await $.fs.read(`${dir}/api-token`)).trim()
  if (!/^\d+$/.test(port) || !auth) throw new Error('daemon API files unreadable')
  const response = await Promise.race([
    $.http.fetch(`http://127.0.0.1:${port}/api/tasks/${encodeURIComponent(task)}/bridge/ui`, { headers: { Authorization: `Bearer ${auth}` } }),
    $.clock.sleep(2000).then(() => undefined),
  ])
  if (!response?.ok) throw new Error(`daemon unreachable (${response?.status ?? 'timeout'})`)
  const value = JSON.parse(response.text) as Snapshot
  // An older daemon or mismatched endpoint must not publish a malformed atom.
  if (!value.settings || !Array.isArray(value.bindings) || !Array.isArray(value.messages) || typeof value.unread !== 'number') return null
  return value
}

async function load($: Engine): Promise<void> {
  let value: Snapshot | null
  try {
    value = await fetchSnapshot($)
  } catch {
    // Keep the last snapshot on screen, flagged, rather than hiding the band.
    await update($, snapshot, prev => prev ? { ...prev, offline: true } : null)
    return
  }
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

type Part = { text: string; color?: Color; weight?: 'bold' }

const STATUS_COLOR: Record<string, Color> = { blocked: 'claude', done: 'success', failed: 'error' }

function budgetColor(percent: number): Color | undefined {
  if (percent >= 90) return 'claude'
  if (percent >= 75) return 'warning'
  return undefined
}

// bandSegments builds the band's ' • '-separated segments, each a run of
// styled parts. Role name, kind and orchestrator are left to the TUI rail, and
// context is left to the status line, both of which already show them.
export function bandSegments(value: Snapshot): Part[][] {
  const newest = value.unread > 0 ? value.messages[value.messages.length - 1] : undefined
  const unread = `${value.unread} unread${newest ? ` from ${newest.sender}` : ''}`
  const segments: Part[][] = [[{ text: 'argus:', color: 'ide' }, value.unread > 0 ? { text: ` ${unread}`, color: 'warning', weight: 'bold' } : { text: ` ${unread}` }]]
  const tokens = value.context?.tokens
  const coordinators = value.bindings.filter(b => b.kind === 'coordinator')
  for (const b of coordinators) {
    if (tokens === undefined || !(b.budget > 0)) {
      segments.push([{ text: 'budget ?' }])
      continue
    }
    const percent = Math.round((tokens / b.budget) * 100)
    segments.push([{ text: `budget ${percent}%`, color: budgetColor(percent) }])
  }
  const statuses = [...new Set(value.bindings.map(b => b.status || '?'))]
  if (statuses.length) segments.push(statuses.flatMap((status, i) => [...(i ? [{ text: '/' }] : []), { text: status, color: STATUS_COLOR[status] }]))
  const working = coordinators.reduce((n, b) => n + (b.workers?.working ?? 0), 0)
  const blocked = coordinators.reduce((n, b) => n + (b.workers?.blocked ?? 0), 0)
  const counts: Part[] = []
  if (working > 0) counts.push({ text: `${working} working` })
  if (blocked > 0) counts.push(...(counts.length ? [{ text: ', ' }] : []), { text: `${blocked} blocked`, color: 'claude' })
  if (counts.length) segments.push([{ text: 'workers ' }, ...counts])
  if (value.offline) segments.push([{ text: '⚠ bridge offline', color: 'warning' }])
  return segments
}

export function bandText(value: Snapshot): string {
  return bandSegments(value).map(segment => segment.map(part => part.text).join('')).join(' • ')
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
  const context = value.context?.percent === undefined ? '?' : Math.round(value.context.percent)
  return { text: [...(roles.length ? roles : ['Not Hera-bound.']), ...(value.bindings.length ? [bandText(value)] : [`${value.unread} unread`]), `context ${context}%`, `Bridge: ${value.live ? 'live' : 'offline'}`, ...(value.offline ? ['Daemon unreachable: showing the last snapshot.'] : [])].join('\n') }
}

export function registerUI(on: On, ui: UI): void {
  on('command.run', { command: 'hera-inbox' }, $ => command($, ui, true))
  on('command.run', { command: 'hera-status' }, $ => command($, ui, false))
  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const beneath = await next(e)
    const value = await read($, snapshot)
    if (!value?.settings.enabled || !value.settings.band || value.bindings.length === 0 || e.props.hasSurvey) return beneath
    const { Box, Text } = $.ui.resolve(e)
    const line = bandSegments(value).flatMap((segment, i) => [
      ...(i ? [' • '] : []),
      ...segment.map(part => part.color || part.weight ? <Text color={part.color} weight={part.weight}>{part.text}</Text> : part.text),
    ])
    // The engine's own component draws only surveys (yielded to above), but
    // composed under the line it adds a blank row above the prompt box.
    const plugin = beneath.type === 'engine' ? null : beneath
    return <Box flexDirection="column"><Text color="subtle" wrap="truncate-end">{line}</Text>{plugin}</Box>
  })
}
