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
  emailVerified: boolean;
  emailOtpEnabled: boolean;
  twoFactor: '' | 'totp' | 'email';
  emailReminders: boolean;
  quietHours: { start: number; end: number } | null;
  timeZone: string | null;
  passkeys: number;
  hasPassword: boolean;
  ssoLinked: boolean;
};
export type SessionView = {
  authenticated: boolean;
  twoFactorRequired?: boolean;
  method?: 'totp' | 'email' | 'passkey';
  passkey?: boolean; // a passkey can be the second factor
  user?: Me;
};
export type Instance = { openSignup: boolean; emailEnabled: boolean; hostedMode: boolean; requirePow: boolean; baseUrl?: string; passkeysAvailable?: boolean; sso?: { name: string }; mcpEnabled?: boolean; oauthEnabled?: boolean; webhooksEnabled?: boolean; aiEnabled?: boolean; privacyUrl?: string; termsUrl?: string };
export type Household = {
  id: string;
  name: string;
  timezone: string;
  quietHours: { start: number; end: number } | null;
  defaultReminderPreset?: string;
  areasEnabled: boolean;
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
  rotation: boolean;
  advanceOn: 'any' | 'assignee';
  turnUserId: string | null;
  muted: boolean;
};
export type Area = { id: string; name: string };
export type Completion = {
  id: string;
  userId: string | null;
  completedAt: string;
  source: string;
  via?: string;
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
