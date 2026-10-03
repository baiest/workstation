// Delivery side of notifications: kept apart from the browser so it can be tested.
import type { NotifyEvent } from './notify'

const MAX_PER_BATCH = 5

export interface DeliverState {
  enabled: boolean
  permission: string // Notification.permission
  hidden: boolean // document.hidden: no point interrupting someone who is looking
  make: (title: string, options: { body: string; tag: string }) => void
}

export function deliver(events: NotifyEvent[], st: DeliverState): void {
  if (!st.enabled || st.permission !== 'granted' || !st.hidden) return
  for (const e of events.slice(0, MAX_PER_BATCH)) st.make(e.title, { body: e.body, tag: e.id })
}

export const tabTitle = (waiting: number): string => (waiting > 0 ? `(${waiting}) workstation` : 'workstation')
