// Minimal QR Code encoder (ISO/IEC 18004): byte mode (UTF-8), error correction level M or H,
// versions 1-20, all eight masks with the standard penalty scoring. No dependencies.
// Uses only erasable TypeScript so scripts/qr-check.mjs can import it directly under Node.

export type QrLevel = 'M' | 'H';
// Table 9, versions 1-20 per level: [EC codewords per block, number of blocks]. Format bits: M=00, H=10.
const EC: Record<QrLevel, [number, number][]> = {
  M: [[10, 1], [16, 1], [26, 1], [18, 2], [24, 2], [16, 4], [18, 4], [22, 4], [22, 5], [26, 5], [30, 5], [22, 8], [22, 9], [24, 9], [24, 10], [28, 10], [28, 11], [26, 13], [26, 14], [26, 16]],
  H: [[17, 1], [28, 1], [22, 2], [16, 4], [22, 4], [28, 4], [26, 5], [26, 6], [24, 8], [28, 8], [24, 11], [28, 11], [22, 16], [24, 16], [24, 18], [30, 16], [28, 19], [28, 21], [26, 25], [28, 25]],
};
const MAX_VERSION = 20;
const LEVEL_BITS: Record<QrLevel, number> = { M: 0, H: 2 };

// Total codewords of a version (Table 1): the module count minus function patterns, over 8.
function rawCodewords(v: number): number {
  let m = (16 * v + 128) * v + 64; // all modules, then subtract function patterns
  if (v >= 2) {
    const n = Math.floor(v / 7) + 2;
    m -= (25 * n - 10) * n - 55; // alignment patterns
    if (v >= 7) m -= 36; // version information
  }
  return m >> 3;
}

// GF(256) with the QR polynomial x^8+x^4+x^3+x^2+1 (0x11D).
const EXP = new Uint8Array(512);
const LOG = new Uint8Array(256);
for (let i = 0, x = 1; i < 255; i++) {
  EXP[i] = EXP[i + 255] = x;
  LOG[x] = i;
  x <<= 1;
  if (x & 256) x ^= 0x11d;
}
const mul = (a: number, b: number): number => (a && b ? EXP[LOG[a] + LOG[b]] : 0);

// Reed-Solomon remainder of data divided by the generator of the given degree.
function rs(data: number[], degree: number): number[] {
  let gen = [1];
  for (let i = 0; i < degree; i++) {
    const next = new Array<number>(gen.length + 1).fill(0);
    gen.forEach((c, j) => {
      next[j] ^= c;
      next[j + 1] ^= mul(c, EXP[i]);
    });
    gen = next;
  }
  const r = new Array<number>(degree).fill(0);
  for (const b of data) {
    const f = b ^ r.shift()!;
    r.push(0);
    gen.slice(1).forEach((c, i) => (r[i] ^= mul(c, f)));
  }
  return r;
}

// Encode text; returns the module matrix (true = dark) without quiet zone. The smallest
// fitting version and the best mask are chosen unless forced (used by the tests).
export function qrEncode(text: string, level: QrLevel = 'M', force: { version?: number; mask?: number } = {}): boolean[][] {
  const bytes = Array.from(new TextEncoder().encode(text));
  let v = force.version ?? 1;
  // Section 7.4.4: 4-bit mode + 8/16-bit count (byte mode) + 8 bits per byte.
  const need = (ver: number): number => 4 + (ver < 10 ? 8 : 16) + 8 * bytes.length;
  const dataCw = (ver: number): number => rawCodewords(ver) - EC[level][ver - 1][0] * EC[level][ver - 1][1];
  while (!force.version && v <= MAX_VERSION && need(v) > dataCw(v) * 8) v++;
  if (v > MAX_VERSION || need(v) > dataCw(v) * 8) throw new Error('qr: text too long');

  // Bit stream: mode 0100, count, data, terminator, byte padding, pad codewords 0xEC/0x11.
  const bits: number[] = [];
  const put = (val: number, n: number): void => {
    for (let i = n - 1; i >= 0; i--) bits.push((val >>> i) & 1);
  };
  put(4, 4);
  put(bytes.length, v < 10 ? 8 : 16);
  bytes.forEach((b) => put(b, 8));
  const cap = dataCw(v) * 8;
  put(0, Math.min(4, cap - bits.length));
  while (bits.length % 8) bits.push(0);
  const data: number[] = [];
  for (let i = 0; i < bits.length; i += 8) data.push(parseInt(bits.slice(i, i + 8).join(''), 2));
  for (let pad = 0xec; data.length < dataCw(v); pad ^= 0xec ^ 0x11) data.push(pad);

  // Section 7.6: split into blocks (short blocks first), add RS codewords, interleave.
  const [ecLen, nBlocks] = EC[level][v - 1];
  const nShort = nBlocks - (rawCodewords(v) % nBlocks);
  const shortLen = Math.floor(rawCodewords(v) / nBlocks) - ecLen;
  const blocks: number[][] = [];
  for (let i = 0, k = 0; i < nBlocks; i++) {
    const d = data.slice(k, (k += shortLen + (i < nShort ? 0 : 1)));
    blocks.push(d);
  }
  const ecs = blocks.map((d) => rs(d, ecLen));
  const out: number[] = [];
  for (let i = 0; i <= shortLen; i++) blocks.forEach((b) => i < b.length && out.push(b[i]));
  for (let i = 0; i < ecLen; i++) ecs.forEach((e) => out.push(e[i]));

  const size = 17 + 4 * v;
  const mod: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const fn: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const set = (x: number, y: number, dark: boolean): void => {
    mod[y][x] = dark;
    fn[y][x] = true;
  };

  // Timing patterns (6.3.5), finder patterns with separators (6.3.3).
  for (let i = 0; i < size; i++) {
    set(6, i, i % 2 === 0);
    set(i, 6, i % 2 === 0);
  }
  for (const [cx, cy] of [[3, 3], [size - 4, 3], [3, size - 4]]) {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const x = cx + dx;
        const y = cy + dy;
        const d = Math.max(Math.abs(dx), Math.abs(dy));
        if (x >= 0 && y >= 0 && x < size && y < size) set(x, y, d !== 2 && d !== 4);
      }
    }
  }
  // Alignment patterns (Annex E): evenly spaced centres, skipping the three finder corners.
  const n = v === 1 ? 0 : Math.floor(v / 7) + 2;
  const step = n > 1 ? Math.ceil((v * 4 + 4) / (n * 2 - 2)) * 2 : 0;
  const pos = [6];
  for (let p = size - 7; pos.length < n; p -= step) pos.splice(1, 0, p);
  pos.forEach((cy, i) =>
    pos.forEach((cx, j) => {
      if ((i === 0 && (j === 0 || j === n - 1)) || (i === n - 1 && j === 0)) return;
      for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) set(cx + dx, cy + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
    }),
  );
  // Version information (Annex D, BCH(18,6)), versions 7+.
  if (v >= 7) {
    let r = v;
    for (let i = 0; i < 12; i++) r = (r << 1) ^ ((r >>> 11) * 0x1f25);
    const vi = (v << 12) | r;
    for (let i = 0; i < 18; i++) {
      const a = size - 11 + (i % 3);
      const b = Math.floor(i / 3);
      set(a, b, ((vi >>> i) & 1) === 1);
      set(b, a, ((vi >>> i) & 1) === 1);
    }
  }
  // Format information (Annex C, BCH(15,5), XOR 0x5412).
  const format = (mask: number): void => {
    mask |= LEVEL_BITS[level] << 3;
    let r = mask;
    for (let i = 0; i < 10; i++) r = (r << 1) ^ ((r >>> 9) * 0x537);
    const f = ((mask << 10) | r) ^ 0x5412;
    const bit = (i: number): boolean => ((f >>> i) & 1) === 1;
    for (let i = 0; i <= 5; i++) set(8, i, bit(i));
    set(8, 7, bit(6));
    set(8, 8, bit(7));
    set(7, 8, bit(8));
    for (let i = 9; i < 15; i++) set(14 - i, 8, bit(i));
    for (let i = 0; i < 8; i++) set(size - 1 - i, 8, bit(i));
    for (let i = 8; i < 15; i++) set(8, size - 15 + i, bit(i));
    set(8, size - 8, true); // the always-dark module
  };
  format(0); // reserve the area so data placement skips it

  // Data placement (7.7.3): two-column zigzag from the bottom right, skipping column 6.
  for (let i = 0, right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    for (let vert = 0; vert < size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j;
        const y = ((right + 1) & 2) === 0 ? size - 1 - vert : vert;
        if (!fn[y][x] && i < out.length * 8) mod[y][x] = ((out[i >>> 3] >>> (7 - (i & 7))) & 1) === 1;
        if (!fn[y][x]) i++;
      }
    }
  }

  // Masking (Table 10) and penalty scoring (7.8.3); the lowest score wins, ties to the lowest mask.
  const cond = [
    (x: number, y: number) => (x + y) % 2 === 0,
    (_x: number, y: number) => y % 2 === 0,
    (x: number) => x % 3 === 0,
    (x: number, y: number) => (x + y) % 3 === 0,
    (x: number, y: number) => (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0,
    (x: number, y: number) => ((x * y) % 2) + ((x * y) % 3) === 0,
    (x: number, y: number) => (((x * y) % 2) + ((x * y) % 3)) % 2 === 0,
    (x: number, y: number) => (((x + y) % 2) + ((x * y) % 3)) % 2 === 0,
  ];
  const apply = (mask: number): void => {
    for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) if (!fn[y][x] && cond[mask](x, y)) mod[y][x] = !mod[y][x];
  };
  const penalty = (): number => {
    let p = 0;
    let dark = 0;
    const lines = [mod, mod.map((_, x) => mod.map((row) => row[x]))]; // rows, then columns
    const pat = [true, false, true, true, true, false, true];
    for (const g of lines) {
      for (const row of g) {
        let run = 1;
        for (let i = 0; i < size; i++) {
          if (i > 0 && row[i] === row[i - 1]) run++;
          else run = 1;
          if (run === 5) p += 3; // N1: 5 in a row, +1 for each more
          else if (run > 5) p++;
          const light = (a: number, b: number): boolean => {
            for (let k = a; k < b; k++) if (k >= 0 && k < size && row[k]) return false;
            return true;
          };
          if (i + 7 <= size && pat.every((b, k) => row[i + k] === b)) {
            // N3: 1011101 with four light modules on either side (the quiet zone counts as light)
            if (light(i - 4, i)) p += 40;
            if (light(i + 7, i + 11)) p += 40;
          }
        }
      }
    }
    for (let y = 0; y < size; y++) {
      for (let x = 0; x < size; x++) {
        if (mod[y][x]) dark++;
        // N2: 2x2 blocks of one colour
        if (x + 1 < size && y + 1 < size && mod[y][x] === mod[y][x + 1] && mod[y][x] === mod[y + 1][x] && mod[y][x] === mod[y + 1][x + 1]) p += 3;
      }
    }
    // N4: 10 points per full 5% the dark proportion strays from 50%
    const total = size * size;
    return p + (Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1) * 10;
  };
  let best = 0;
  let bestScore = Infinity;
  for (let m = force.mask ?? 0; m < (force.mask ?? 7) + 1; m++) {
    apply(m);
    format(m);
    const s = penalty();
    if (s < bestScore) [best, bestScore] = [m, s];
    apply(m); // undo
  }
  apply(best);
  format(best);
  return mod;
}

// SVG path data for a matrix with a quiet zone: one run-length rectangle per row run.
export function qrPath(m: boolean[][], quiet = 4): { d: string; size: number } {
  let d = '';
  m.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) {
      if (!row[x]) continue;
      let n = 1;
      while (row[x + n]) n++;
      d += `M${x + quiet} ${y + quiet}h${n}v1h-${n}z`;
      x += n - 1;
    }
  });
  return { d, size: m.length + 2 * quiet };
}

// Side, in modules, of the logo plate for an n-module symbol, or 0 for none. Only from
// version 7 (45 modules) up, where 15 modules is at most 11% of the area, far inside what
// level H restores; it covers the central alignment pattern, which the other patterns make
// redundant. 15 was chosen empirically: other sizes (11, 13, 17 ...) made zbar fail erratically.
export const qrLogoSide = (n: number): number => (n >= 45 ? 15 : 0);
