export type Binding = { binding_id: number; role: string; kind: string; orchestrator: string; status: string; unread: number; budget: number }
export type Message = { id: string; sender: string; tldr: string; sent_at: string }
export type Snapshot = {
  settings: { enabled: boolean; delivery: boolean; band: boolean; toasts: boolean }
  live: boolean
  context: { tokens?: number; percent?: number; window?: number } | null
  bindings: Binding[]
  unread: number
  messages: Message[]
}

declare module 'claude-code' {
  interface PluginState {
    'argus-bridge': { ui: Snapshot | null; seenMessages: string[] | null }
  }
}
