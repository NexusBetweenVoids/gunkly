// A small Markdown subset for release notes, parsed into a tree that components render with
// ordinary Svelte markup. Nothing here produces HTML: every piece of text ends up as a text node,
// so raw HTML in the source (<script>, <img onerror>, …) is shown literally, never interpreted.
// Links only keep http(s) URLs; anything else (javascript:, data:, relative paths) stays text.
// No images. Supported: ATX headings, paragraphs, bullet and numbered lists (nested by indent),
// fenced code, horizontal rules, **strong**, *em*, `code`, [text](url), <https://autolinks>, \escapes.

export type Inline =
  | { t: 'text'; v: string }
  | { t: 'code'; v: string }
  | { t: 'strong'; c: Inline[] }
  | { t: 'em'; c: Inline[] }
  | { t: 'link'; href: string; c: Inline[] };

export type ListItem = { c: Inline[]; sub: Block[] };

export type Block =
  | { t: 'h'; level: number; c: Inline[] }
  | { t: 'p'; c: Inline[] }
  | { t: 'ul' | 'ol'; items: ListItem[] }
  | { t: 'code'; v: string }
  | { t: 'hr' };

/** The URL as a safe link target, or null: absolute http(s) only, no credentials. */
export function safeHref(raw: string): string | null {
  const s = raw.trim();
  if (!/^https?:\/\//i.test(s)) return null;
  try {
    const u = new URL(s);
    if ((u.protocol !== 'https:' && u.protocol !== 'http:') || u.username || u.password) return null;
    return u.href;
  } catch {
    return null;
  }
}

const MAX_DEPTH = 8;
const PUNCT = '\\`*_{}[]()#+-.!<>|~';

/** Inline Markdown to nodes. Searches for a closing delimiter remember failures, so it stays linear-ish. */
export function inline(src: string, depth = 0, links = true): Inline[] {
  const out: Inline[] = [];
  let buf = '';
  const flush = (): void => {
    if (buf) out.push({ t: 'text', v: buf });
    buf = '';
  };
  const none = new Set<string>(); // delimiters with no closing match from here on
  let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (ch === '\\' && i + 1 < src.length && PUNCT.includes(src[i + 1])) {
      buf += src[i + 1];
      i += 2;
      continue;
    }
    if (ch === '`' && !none.has('`')) {
      let n = 1;
      while (src[i + n] === '`') n++;
      const fence = '`'.repeat(n);
      const end = src.indexOf(fence, i + n);
      if (end < 0) {
        if (n === 1) none.add('`');
        buf += fence;
        i += n;
        continue;
      }
      flush();
      out.push({ t: 'code', v: src.slice(i + n, end).replace(/^ (.*\S.*) $/, '$1') });
      i = end + n;
      continue;
    }
    if (depth < MAX_DEPTH && ch === '*' && src[i + 1] === '*' && !none.has('**') && src[i + 2] && src[i + 2] !== ' ') {
      const end = src.indexOf('**', i + 2);
      if (end > i + 2 && src[end - 1] !== ' ') {
        flush();
        out.push({ t: 'strong', c: inline(src.slice(i + 2, end), depth + 1, links) });
        i = end + 2;
        continue;
      }
      if (end < 0) none.add('**');
    }
    if (depth < MAX_DEPTH && ch === '*' && src[i + 1] !== '*' && src[i + 1] && src[i + 1] !== ' ' && !none.has('*')) {
      let end = i + 1;
      for (;;) {
        end = src.indexOf('*', end + 1);
        if (end < 0 || src[end + 1] !== '*') break;
        end++; // skip a ** inside the emphasis
      }
      if (end > i + 1 && src[end - 1] !== ' ') {
        flush();
        out.push({ t: 'em', c: inline(src.slice(i + 1, end), depth + 1, links) });
        i = end + 1;
        continue;
      }
      if (end < 0) none.add('*');
    }
    if (links && ch === '[' && !none.has('[')) {
      const close = src.indexOf('](', i + 1);
      const end = close < 0 ? -1 : src.indexOf(')', close + 2);
      if (close < 0 || end < 0) none.add('[');
      else {
        const href = safeHref(src.slice(close + 2, end));
        if (href) {
          flush();
          out.push({ t: 'link', href, c: inline(src.slice(i + 1, close), depth + 1, false) });
          i = end + 1;
          continue;
        }
      }
    }
    if (links && ch === '<') {
      const m = /^<(https?:\/\/[^\s<>]+)>/i.exec(src.slice(i, i + 2048));
      const href = m && safeHref(m[1]);
      if (m && href) {
        flush();
        out.push({ t: 'link', href, c: [{ t: 'text', v: m[1] }] });
        i += m[0].length;
        continue;
      }
    }
    buf += ch;
    i++;
  }
  flush();
  return out;
}

type Entry = { indent: number; ordered: boolean; text: string };
const ITEM = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;
const HEADING = /^#{1,6}\s+(.*?)\s*#*\s*$/;
const FENCE = /^\s*(```|~~~)/;
const RULE = /^\s*([-*_])(\s*\1){2,}\s*$/;
const width = (s: string): number => s.replace(/\t/g, '    ').length;

function list(entries: Entry[], from: number, depth: number): [Block, number] {
  const indent = entries[from].indent;
  const items: ListItem[] = [];
  let i = from;
  while (i < entries.length && entries[i].indent >= indent) {
    if (entries[i].indent > indent && depth < MAX_DEPTH) {
      const [sub, next] = list(entries, i, depth + 1);
      if (!items.length) items.push({ c: [], sub: [] });
      items[items.length - 1].sub.push(sub);
      i = next;
    } else {
      items.push({ c: inline(entries[i].text), sub: [] });
      i++;
    }
  }
  return [{ t: entries[from].ordered ? 'ol' : 'ul', items }, i];
}

/** Block-level Markdown to a tree. Line endings are normalised; the caller caps the length. */
export function parse(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, '\n').split('\n');
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) {
      i++;
      continue;
    }
    const fence = FENCE.exec(line);
    if (fence) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++]);
      i++; // the closing fence (or past the end)
      out.push({ t: 'code', v: body.join('\n') });
      continue;
    }
    const h = HEADING.exec(line);
    if (h) {
      out.push({ t: 'h', level: line.indexOf(' '), c: inline(h[1]) });
      i++;
      continue;
    }
    if (RULE.test(line)) {
      out.push({ t: 'hr' });
      i++;
      continue;
    }
    if (ITEM.test(line)) {
      const entries: Entry[] = [];
      while (i < lines.length) {
        const l = lines[i];
        const m = ITEM.exec(l);
        if (m && !RULE.test(l)) {
          entries.push({ indent: width(m[1]), ordered: /\d/.test(m[2]), text: m[3] });
        } else if (l.trim() && /^\s/.test(l) && entries.length && !FENCE.test(l)) {
          entries[entries.length - 1].text += ' ' + l.trim(); // continuation line
        } else if (!l.trim()) {
          // A blank line inside a list keeps it going only if another item follows.
          let j = i + 1;
          while (j < lines.length && !lines[j].trim()) j++;
          if (j >= lines.length || !ITEM.test(lines[j]) || RULE.test(lines[j])) break;
        } else break;
        i++;
      }
      let k = 0;
      const base = Math.min(...entries.map((e) => e.indent));
      for (const e of entries) e.indent -= base;
      while (k < entries.length) {
        const [block, next] = list(entries, k, 0);
        out.push(block);
        k = next;
      }
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() && !FENCE.test(lines[i]) && !HEADING.test(lines[i]) && !ITEM.test(lines[i]) && !RULE.test(lines[i])) {
      para.push(lines[i].trim().replace(/^>\s?/, ''));
      i++;
    }
    out.push({ t: 'p', c: inline(para.join(' ')) });
  }
  return out;
}
