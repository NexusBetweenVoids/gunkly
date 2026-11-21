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
  role: Role;
  createdAt: string;
};
export type Member = { userId: string; displayName: string; role: Role; joinedAt: string };
export type Invite = { id: string; role: Role; createdAt: string; expiresAt: string; token?: string };
