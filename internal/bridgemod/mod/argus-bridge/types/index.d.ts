export type Binding = { binding_id: number; role: string; kind: string; orchestrator: string; status: string; unread: number; budget: number; board_unread?: number; board_subject?: string; workers?: { working: number; blocked: number } }
export type Message = { id: string; sender: string; tldr: string; sent_at: string }
export type Snapshot = {
  settings: { enabled: boolean; delivery: boolean; band: boolean; toasts: boolean }
  live: boolean
  context: { tokens?: number; percent?: number; window?: number } | null
  bindings: Binding[]
  unread: number
  messages: Message[]
  // Set by the mod, never the daemon: the last fetch failed and this is the
  // previous snapshot kept on screen.
  offline?: boolean
}

declare module 'claude-code' {
  interface PluginState {
    'argus-bridge': { ui: Snapshot | null; seenMessages: string[] | null }
  }
}
