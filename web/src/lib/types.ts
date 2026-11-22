// Response shapes of the /api/v1 endpoints the UI uses.
export type Role = 'owner' | 'member' | 'guest';
export type Me = {
  id: string;
  username: string;
  email?: string;
  displayName: string;
  locale: string;
  weekStart: number | null;
  isAdmin: boolean;
  totpEnabled: boolean;
};
export type SessionView = { authenticated: boolean; twoFactorRequired?: boolean; user?: Me };
export type Household = {
  id: string;
  name: string;
  timezone: string;
  quietHours: { start: number; end: number } | null;
  defaultReminderPreset?: string;
  role: Role;
  createdAt: string;
};
export type Member = { userId: string; displayName: string; role: Role; joinedAt: string };
export type Invite = { id: string; role: Role; createdAt: string; expiresAt: string; maxUses: number; uses: number; token?: string };
export type Status = 'overdue' | 'today' | 'thisWeek' | 'later' | 'none' | 'paused' | 'archived';
export type Schedule = {
  type: 'interval' | 'rrule';
  interval?: { every: number; unit: 'day' | 'week' | 'month' };
  rrule?: string;
  anchor?: 'from_completion' | 'from_scheduled';
  firstDue: string;
};
export type Chore = {
  id: string;
  areaId: string | null;
  name: string;
  notes: string;
  link: string;
  kind: 'recurring' | 'oneoff';
  schedule: Schedule;
  dueDate: string;
  status: Status;
  pausedUntil: string | null;
  pausedIndefinitely: boolean;
  archivedAt: string | null;
  reminderPreset: string;
  assignees: string[];
  muted: boolean;
};
export type Area = { id: string; name: string };
export type Completion = {
  id: string;
  userId: string | null;
  completedAt: string;
  source: string;
  note: string;
  revertedAt: string | null;
};
export type Reminder = { kind: 'before' | 'on' | 'overdue'; days: number; minute: number };
export type Channel = { id: string; kind: string; label: string; hint: string };
export type Delivery = {
  id: string;
  type: string;
  choreName?: string;
  channelLabel?: string;
  kind?: string;
  status: string;
  attempts: number;
  error?: string;
  createdAt: string;
};
export type Page<T> = { items: T[]; next?: string | null };
export type Template = { key: string; area: string; interval: { every: number; unit: 'day' | 'week' | 'month' } };
