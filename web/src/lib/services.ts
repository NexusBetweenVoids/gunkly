import type { Key } from './i18n';

// Notification services in plain terms: what to fill in, how to get it, and how that
// becomes a Shoutrrr service URL (built here, so people never write one by hand).
// Kinds match the server's allowlist (internal/notify); unknown kinds fall back to
// the raw URL field.

export type ServiceField = { id: string; label: Key; hint?: Key; placeholder?: string; secret?: boolean };
export type Service = {
  name: string;
  /** Path under the Shoutrrr docs' services/ for the full guide. */
  docs: string;
  /** Example service URL, for the raw URL field (only where the guided fields exist). */
  format?: string;
  /** Plain-language setup steps. */
  steps?: Key[];
  /** Fields that replace the raw URL; build returns the URL or null when a value is invalid. */
  fields?: ServiceField[];
  build?: (v: Record<string, string>) => string | null;
};

const DOCS = 'https://shoutrrr.nickfedor.com/latest/services/';
export const docsUrl = (s: Service): string => DOCS + s.docs + '/';

const enc = encodeURIComponent;
const webhook: ServiceField = { id: 'webhook', label: 'svc.webhook', secret: true };

/** Turns a pasted webhook link (or service URL) into a Shoutrrr URL, or null. */
export function fromLink(raw: string): string | null {
  const v = raw.trim();
  let m = v.match(/^https:\/\/(?:ptb\.|canary\.)?discord(?:app)?\.com\/api\/webhooks\/(\d+)\/([\w-]+)\/?$/);
  if (m) return `discord://${m[2]}@${m[1]}`;
  m = v.match(/^https:\/\/hooks\.slack\.com\/services\/(\w+)\/(\w+)\/(\w+)\/?$/);
  if (m) return `slack://hook:${m[1]}-${m[2]}-${m[3]}@webhook`;
  m = v.match(/^https:\/\/chat\.googleapis\.com\/(v1\/spaces\/[^/?#]+\/messages\?\S+)$/);
  if (m) return `googlechat://chat.googleapis.com/${m[1]}`;
  return null;
}

const linkOf = (scheme: string) => (v: Record<string, string>) => {
  const u = fromLink(v.webhook ?? '') ?? (v.webhook?.trim().startsWith(`${scheme}://`) ? v.webhook.trim() : null);
  return u?.startsWith(`${scheme}://`) ? u : null;
};
const topicOK = (s: string) => /^[\w-]{1,64}$/.test(s);

export const SERVICES: Record<string, Service> = {
  ntfy: {
    name: 'ntfy',
    docs: 'push/ntfy',
    format: 'ntfy://ntfy.sh/TOPIC',
    steps: ['svc.ntfy.step1', 'svc.ntfy.step2'],
    fields: [{ id: 'topic', label: 'svc.topic', hint: 'svc.topicHint', placeholder: 'chores-a8f3k2' }],
    build: (v) => {
      const topic = (v.topic ?? '').trim().replace(/^https:\/\/ntfy\.sh\//, '').replace(/\/$/, '');
      return topicOK(topic) ? `ntfy://ntfy.sh/${topic}` : null;
    },
  },
  'ntfy-selfhosted': {
    name: 'ntfy (your own server)',
    docs: 'push/ntfy',
    format: 'ntfy://ntfy.example.com/TOPIC',
    steps: ['svc.ntfySelf.step1', 'svc.ntfy.step2'],
    fields: [
      { id: 'server', label: 'svc.server', placeholder: 'https://ntfy.example.com' },
      { id: 'topic', label: 'svc.topic', hint: 'svc.topicHint', placeholder: 'chores-a8f3k2' },
    ],
    build: (v) => {
      const topic = (v.topic ?? '').trim();
      const m = (v.server ?? '').trim().match(/^(https?):\/\/([^/?#\s]+)(\/[^?#\s]*)?$/i);
      if (!m || !topicOK(topic)) return null;
      const path = (m[3] ?? '').replace(/\/+$/, '');
      return `ntfy://${m[2]}${path}/${topic}${m[1].toLowerCase() === 'http' ? '?scheme=http' : ''}`;
    },
  },
  discord: {
    name: 'Discord',
    docs: 'chat/discord',
    format: 'discord://TOKEN@WEBHOOK_ID',
    steps: ['svc.discord.step1', 'svc.discord.step2'],
    fields: [{ ...webhook, placeholder: 'https://discord.com/api/webhooks/…' }],
    build: linkOf('discord'),
  },
  slack: {
    name: 'Slack',
    docs: 'chat/slack',
    format: 'slack://hook:TOKEN@webhook',
    steps: ['svc.slack.step1', 'svc.slack.step2'],
    fields: [{ ...webhook, placeholder: 'https://hooks.slack.com/services/…' }],
    build: linkOf('slack'),
  },
  googlechat: {
    name: 'Google Chat',
    docs: 'chat/googlechat',
    format: 'googlechat://chat.googleapis.com/v1/spaces/…',
    steps: ['svc.googlechat.step1', 'svc.googlechat.step2'],
    fields: [{ ...webhook, placeholder: 'https://chat.googleapis.com/v1/spaces/…' }],
    build: linkOf('googlechat'),
  },
  telegram: {
    name: 'Telegram',
    docs: 'chat/telegram',
    format: 'telegram://BOT_TOKEN@telegram?chats=CHAT_ID',
    steps: ['svc.telegram.step1', 'svc.telegram.step2', 'svc.telegram.step3'],
    fields: [
      { id: 'token', label: 'svc.botToken', placeholder: '123456789:AA…', secret: true },
      { id: 'chat', label: 'svc.chat', hint: 'svc.chatHint', placeholder: '@my_channel' },
    ],
    build: (v) => {
      const token = (v.token ?? '').trim();
      const chat = (v.chat ?? '').trim();
      return /^\d+:[\w-]+$/.test(token) && /^(@\w{4,}|-?\d+)$/.test(chat) ? `telegram://${token}@telegram?chats=${enc(chat)}` : null;
    },
  },
  pushover: {
    name: 'Pushover',
    docs: 'push/pushover',
    format: 'pushover://shoutrrr:API_TOKEN@USER_KEY/',
    steps: ['svc.pushover.step1', 'svc.pushover.step2'],
    fields: [
      { id: 'user', label: 'svc.userKey', placeholder: 'uQiRzpo4DXghDmr9QzzfQu27cmVRsG', secret: true },
      { id: 'token', label: 'svc.appToken', placeholder: 'azGDORePK8gMaC0QOYAMyEEuzJnyUi', secret: true },
    ],
    build: (v) => {
      const user = (v.user ?? '').trim();
      const token = (v.token ?? '').trim();
      return /^\w{20,40}$/.test(user) && /^\w{20,40}$/.test(token) ? `pushover://shoutrrr:${token}@${user}/` : null;
    },
  },
  pushbullet: {
    name: 'Pushbullet',
    docs: 'push/pushbullet',
    format: 'pushbullet://ACCESS_TOKEN',
    steps: ['svc.pushbullet.step1'],
    fields: [{ id: 'token', label: 'svc.accessToken', placeholder: 'o.…', secret: true }],
    build: (v) => {
      const token = (v.token ?? '').trim();
      return /^[\w.-]{8,}$/.test(token) ? `pushbullet://${token}` : null;
    },
  },
  // Optional services (an admin enables them): name, guide and format only.
  generic: { name: 'Webhook (generic)', docs: 'specialized/generic' },
  smtp: { name: 'Email (SMTP)', docs: 'email/smtp' },
  mqtt: { name: 'MQTT', docs: 'push/mqtt' },
  xmpp: { name: 'XMPP', docs: 'chat/xmpp' },
  gotify: { name: 'Gotify', docs: 'push/gotify' },
  matrix: { name: 'Matrix', docs: 'chat/matrix' },
  mattermost: { name: 'Mattermost', docs: 'chat/mattermost' },
  rocketchat: { name: 'Rocket.Chat', docs: 'chat/rocketchat' },
  zulip: { name: 'Zulip', docs: 'chat/zulip' },
  homeassistant: { name: 'Home Assistant', docs: 'push/homeassistant' },
  signal: { name: 'Signal', docs: 'chat/signal' },
  bark: { name: 'Bark', docs: 'push/bark' },
  teams: { name: 'Microsoft Teams', docs: 'chat/teams' },
  twilio: { name: 'Twilio (SMS)', docs: 'sms/twilio' },
  pagerduty: { name: 'PagerDuty', docs: 'incident/pagerduty' },
  opsgenie: { name: 'Opsgenie', docs: 'incident/opsgenie' },
  ifttt: { name: 'IFTTT', docs: 'push/ifttt' },
  join: { name: 'Join', docs: 'push/join' },
  lark: { name: 'Lark', docs: 'chat/lark' },
  notifiarr: { name: 'Notifiarr', docs: 'specialized/notifiarr' },
  signalgrid: { name: 'Signalgrid', docs: 'push/signalgrid' },
  wecom: { name: 'WeCom', docs: 'chat/wecom' },
};

/** Display name of a kind; the kind itself when unknown. */
export const serviceName = (kind: string): string => SERVICES[kind]?.name ?? kind;
