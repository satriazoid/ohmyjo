// Generates ohmyjo.ico: a ring ("O") in the default theme's accent colour on a
// black tile, rendered at every size Windows asks for.
//
// Rebuild the embedded copy after any change here:
//   node scripts/make-icon.mjs assets/ohmyjo.ico
//   node scripts/check-icon.mjs assets/ohmyjo.ico
//   rsrc -ico assets/ohmyjo.ico -o rsrc.syso
//
// The tile is a full square rather than a rounded shape because the icon is
// mostly seen at 16-32px in the taskbar, where a corner radius is invisible but
// a full-bleed background still reads as a solid app mark.
//
// Entries up to 64px are stored as uncompressed DIBs and the larger ones as PNG,
// which is the combination the shell handles best: the legacy drawing paths
// only understand DIBs, while PNG keeps the 256px entry from bloating the exe.
import { deflateSync } from "node:zlib";
import { writeFileSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";

const OUT = resolve(process.argv[2] ?? "ohmyjo.ico");
// Sizes Explorer, the taskbar and Alt+Tab select between.
const SIZES = [256, 128, 64, 48, 32, 24, 16];
// Accent of the default theme, so the mark matches the app it opens.
const RING = [0x7a, 0xa2, 0xf7];
const BG = [0, 0, 0];
const SUPERSAMPLE = 4;

// Outer/inner radius of the ring, as a fraction of the tile. The stroke is
// deliberately heavy: a thin ring dissolves into grey at 16px.
const R_OUT = 0.375;
const R_IN = 0.225;

/** Antialiased RGBA pixels for one tile size. */
function render(size) {
  const px = new Uint8Array(size * size * 4);
  const c = (size - 1) / 2;
  const outer = R_OUT * size;
  const inner = R_IN * size;
  const step = 1 / SUPERSAMPLE;
  const total = SUPERSAMPLE * SUPERSAMPLE;

  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      let hits = 0;
      for (let sy = 0; sy < SUPERSAMPLE; sy++) {
        for (let sx = 0; sx < SUPERSAMPLE; sx++) {
          const dx = x + (sx + 0.5) * step - 0.5 - c;
          const dy = y + (sy + 0.5) * step - 0.5 - c;
          const d = Math.sqrt(dx * dx + dy * dy);
          if (d <= outer && d >= inner) hits++;
        }
      }
      const a = hits / total;
      const o = (y * size + x) * 4;
      // Blend the ring over the black tile; the tile itself stays opaque so the
      // mark does not depend on what is behind it.
      for (let i = 0; i < 3; i++) px[o + i] = Math.round(BG[i] + (RING[i] - BG[i]) * a);
      px[o + 3] = 255;
    }
  }
  return px;
}

// ---------------------------------------------------------------- PNG

const CRC_TABLE = (() => {
  const t = new Int32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c;
  }
  return t;
})();

function crc32(buf) {
  let c = ~0;
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return ~c >>> 0;
}

function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, "latin1"), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([len, body, crc]);
}

function encodePng(size, px) {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(size, 0);
  ihdr.writeUInt32BE(size, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 6; // truecolour with alpha
  // Filter byte 0 per scanline: the image has flat runs and few colours, so
  // deflate alone gets it small without the cost of choosing filters.
  const raw = Buffer.alloc(size * (size * 4 + 1));
  for (let y = 0; y < size; y++) {
    const at = y * (size * 4 + 1);
    raw[at] = 0;
    Buffer.from(px.buffer, px.byteOffset + y * size * 4, size * 4).copy(raw, at + 1);
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", deflateSync(raw, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

// ---------------------------------------------------------------- DIB

function encodeDib(size, px) {
  const header = Buffer.alloc(40);
  header.writeUInt32LE(40, 0);
  header.writeInt32LE(size, 4);
  // Height is doubled: the XOR image plus the AND mask that follows it.
  header.writeInt32LE(size * 2, 8);
  header.writeUInt16LE(1, 12); // planes
  header.writeUInt16LE(32, 14); // bits per pixel
  header.writeUInt32LE(size * size * 4, 20);

  // DIBs are stored bottom-up and as BGRA.
  const xor = Buffer.alloc(size * size * 4);
  for (let y = 0; y < size; y++) {
    const src = (size - 1 - y) * size * 4;
    for (let x = 0; x < size; x++) {
      const s = src + x * 4;
      const d = (y * size + x) * 4;
      xor[d] = px[s + 2];
      xor[d + 1] = px[s + 1];
      xor[d + 2] = px[s];
      xor[d + 3] = px[s + 3];
    }
  }

  // The AND mask is 1bpp, rows padded to four bytes. Every pixel is opaque, so
  // the mask is empty and the alpha channel above decides transparency.
  const stride = Math.ceil(size / 32) * 4;
  const mask = Buffer.alloc(stride * size);
  return Buffer.concat([header, xor, mask]);
}

// ---------------------------------------------------------------- ICO

const entries = [];
for (const size of SIZES) {
  const px = render(size);
  entries.push({ size, data: size >= 128 ? encodePng(size, px) : encodeDib(size, px) });
}

const dir = Buffer.alloc(6);
dir.writeUInt16LE(0, 0);
dir.writeUInt16LE(1, 2); // icon
dir.writeUInt16LE(entries.length, 4);

let offset = 6 + entries.length * 16;
const table = [];
for (const e of entries) {
  const row = Buffer.alloc(16);
  // 256 is encoded as 0 in the single-byte dimension fields.
  row[0] = e.size >= 256 ? 0 : e.size;
  row[1] = e.size >= 256 ? 0 : e.size;
  row[2] = 0; // palette size: 0 means truecolour
  row[3] = 0;
  row.writeUInt16LE(1, 4); // planes
  row.writeUInt16LE(32, 6); // bits per pixel
  row.writeUInt32LE(e.data.length, 8);
  row.writeUInt32LE(offset, 12);
  offset += e.data.length;
  table.push(row);
}

const ico = Buffer.concat([dir, ...table, ...entries.map((e) => e.data)]);
mkdirSync(dirname(OUT), { recursive: true });
writeFileSync(OUT, ico);
console.log(`${OUT}: ${entries.length} sizes (${SIZES.join(", ")}), ${ico.length} bytes`);
