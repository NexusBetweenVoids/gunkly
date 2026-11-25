// Checks for src/lib/markdown.ts (release notes on Administration, Updates): the supported subset
// parses as expected and nothing unsafe gets through. Runs under plain Node (type stripping).
import { inline, parse, safeHref } from '../src/lib/markdown.ts';

let bad = 0;
const eq = (name, got, want) => {
  const g = JSON.stringify(got);
  const w = JSON.stringify(want);
  if (g !== w) {
    console.error(`md-check: ${name}\n  got  ${g}\n  want ${w}`);
    bad++;
  }
};
const text = (v) => ({ t: 'text', v });
/** Every node of the tree, depth first. */
const nodes = (x, out = []) => {
  if (Array.isArray(x)) x.forEach((y) => nodes(y, out));
  else if (x && typeof x === 'object') {
    out.push(x);
    for (const v of Object.values(x)) if (typeof v === 'object') nodes(v, out);
  }
  return out;
};

// Blocks: a real CHANGELOG section.
eq(
  'changelog section',
  parse('### Changed\n\n- Gunkly has a **new** home: <https://github.com/NexusBetweenVoids/gunkly>.\n  Second line.\n- `ghcr.io/x` *now*\n  - nested\n\n### Fixed\n\n1. one\n2. two'),
  [
    { t: 'h', level: 3, c: [text('Changed')] },
    {
      t: 'ul',
      items: [
        {
          c: [text('Gunkly has a '), { t: 'strong', c: [text('new')] }, text(' home: '), { t: 'link', href: 'https://github.com/NexusBetweenVoids/gunkly', c: [text('https://github.com/NexusBetweenVoids/gunkly')] }, text('. Second line.')],
          sub: [],
        },
        { c: [{ t: 'code', v: 'ghcr.io/x' }, text(' '), { t: 'em', c: [text('now')] }], sub: [{ t: 'ul', items: [{ c: [text('nested')], sub: [] }] }] },
      ],
    },
    { t: 'h', level: 3, c: [text('Fixed')] },
    { t: 'ol', items: [{ c: [text('one')], sub: [] }, { c: [text('two')], sub: [] }] },
  ],
);
eq('paragraph and code block', parse('a\nb\n\n```\n<b>x</b>\n```\n---'), [{ t: 'p', c: [text('a b')] }, { t: 'code', v: '<b>x</b>' }, { t: 'hr' }]);
eq('link', inline('[the docs](https://gunkly.app/docs/)'), [{ t: 'link', href: 'https://gunkly.app/docs/', c: [text('the docs')] }]);
eq('code wins over emphasis', inline('`GUNKLY_OIDC_*` and *x*'), [{ t: 'code', v: 'GUNKLY_OIDC_*' }, text(' and '), { t: 'em', c: [text('x')] }]);
eq('escapes', inline('\\*not em\\*'), [text('*not em*')]);
eq('lone stars stay text', inline('2 * 3 * 4 and a*'), [text('2 * 3 * 4 and a*')]);

// Safety: raw HTML is text, unsafe links are text, no node type outside the allowlist.
eq('raw html stays text', inline('<script>alert(1)</script><img src=x onerror=alert(1)>'), [text('<script>alert(1)</script><img src=x onerror=alert(1)>')]);
for (const href of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', ' javascript:alert(1)', 'data:text/html,<b>x', 'vbscript:x', '//evil.example', '/relative', 'https://user:pw@example.com/', 'file:///etc/passwd']) {
  eq(`unsafe href ${href}`, safeHref(href), null);
  const got = inline(`[x](${href})`);
  if (nodes(got).some((n) => n.t === 'link')) {
    console.error(`md-check: [x](${href}) became a link`);
    bad++;
  }
}
eq('autolink only http(s)', inline('<javascript:alert(1)>'), [text('<javascript:alert(1)>')]);
eq('no links inside link text', nodes(inline('[a [b](https://b.example) c](https://a.example)')).filter((n) => n.t === 'link').length, 1);
const allowed = new Set(['text', 'code', 'strong', 'em', 'link', 'h', 'p', 'ul', 'ol', 'hr']);
const all = nodes(parse('# <h1>\n- [x](https://a.example) **<b>** `<i>`\n\n> <iframe>\n\n```html\n<script>\n```'));
for (const n of all) {
  if ('t' in n && !allowed.has(n.t)) {
    console.error(`md-check: unexpected node ${n.t}`);
    bad++;
  }
  if (n.t === 'link' && !/^https?:\/\//.test(n.href)) {
    console.error(`md-check: unsafe href ${n.href}`);
    bad++;
  }
}

// Robustness: deep nesting and long hostile input finish quickly and do not throw.
const t0 = performance.now();
parse('- a\n' + Array.from({ length: 200 }, (_, i) => ' '.repeat(i + 1) + '- x').join('\n'));
inline('**'.repeat(4000));
inline('*a'.repeat(4000));
inline('['.repeat(8000));
inline('`'.repeat(8000));
parse('<'.repeat(8000));
const ms = performance.now() - t0;
if (ms > 1000) {
  console.error(`md-check: hostile input took ${Math.round(ms)} ms`);
  bad++;
}

if (bad) process.exit(1);
console.log('md-check: ok');
