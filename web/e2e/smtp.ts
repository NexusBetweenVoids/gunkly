import { appendFileSync, readFileSync, writeFileSync } from 'node:fs';
import { type Server, createServer } from 'node:net';

// A tiny SMTP server for the e2e run (node:net, no dependencies): it accepts any
// message and appends it as one JSON line to `file`, so specs (which run in other
// processes) can read what the app "sent". It only ever listens on 127.0.0.1.
export type Mail = { from: string; to: string[]; data: string };

export function startFakeSmtp(file: string): Promise<{ port: number; close: () => Promise<void> }> {
  writeFileSync(file, '');
  const server: Server = createServer((sock) => {
    let from = '';
    let to: string[] = [];
    let buf = '';
    let inData = false;
    const say = (s: string) => sock.write(s + '\r\n');
    say('220 fake ESMTP');
    sock.on('error', () => {});
    sock.on('data', (chunk) => {
      buf += chunk.toString('latin1');
      for (;;) {
        if (inData) {
          const end = buf.indexOf('\r\n.\r\n');
          if (end < 0) return;
          const data = buf.slice(0, end).replace(/^\./gm, ''); // undo dot-stuffing
          buf = buf.slice(end + 5);
          inData = false;
          const mail: Mail = { from, to, data: Buffer.from(data, 'latin1').toString('utf8') };
          appendFileSync(file, JSON.stringify(mail) + '\n');
          from = '';
          to = [];
          say('250 queued');
          continue;
        }
        const nl = buf.indexOf('\r\n');
        if (nl < 0) return;
        const line = buf.slice(0, nl);
        buf = buf.slice(nl + 2);
        const cmd = line.toUpperCase();
        if (cmd.startsWith('EHLO') || cmd.startsWith('HELO')) say('250 fake');
        else if (cmd.startsWith('MAIL FROM:')) {
          from = line.slice(10).replace(/[<>\s]/g, '');
          say('250 ok');
        } else if (cmd.startsWith('RCPT TO:')) {
          to.push(line.slice(8).replace(/[<>\s]/g, ''));
          say('250 ok');
        } else if (cmd === 'DATA') {
          inData = true;
          say('354 go ahead');
        } else if (cmd === 'QUIT') {
          say('221 bye');
          sock.end();
        } else say('250 ok');
      }
    });
  });
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      if (!addr || typeof addr === 'string') return reject(new Error('no smtp port'));
      resolve({ port: addr.port, close: () => new Promise<void>((r) => server.close(() => r())) });
    });
  });
}

export const readMails = (file: string): Mail[] =>
  readFileSync(file, 'utf8')
    .split('\n')
    .filter(Boolean)
    .map((l) => JSON.parse(l) as Mail);

/** Decodes a quoted-printable (UTF-8) body; headers are left alone. */
export function decodeBody(data: string): string {
  const [head, ...rest] = data.split('\r\n\r\n');
  const body = rest.join('\r\n\r\n');
  if (!/content-transfer-encoding:\s*quoted-printable/i.test(head)) return body;
  const bytes: number[] = [];
  const text = body.replace(/=\r?\n/g, '');
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '=' && /^[0-9A-F]{2}$/i.test(text.slice(i + 1, i + 3))) {
      bytes.push(parseInt(text.slice(i + 1, i + 3), 16));
      i += 2;
    } else bytes.push(...Buffer.from(text[i], 'utf8'));
  }
  return Buffer.from(bytes).toString('utf8');
}
