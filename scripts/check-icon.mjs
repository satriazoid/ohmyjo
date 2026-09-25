// Verifies assets/ohmyjo.ico: it decodes the 256px entry and asserts the mark
// is what the design calls for: an opaque black tile with a closed blue ring
// and a black centre. Run it after regenerating the icon.
import { readFileSync } from "node:fs";
import { inflateSync } from "node:zlib";

function decodePng(buf) {
  let p = 8, w = 0, h = 0;
  const idat = [];
  while (p < buf.length) {
    const len = buf.readUInt32BE(p);
    const type = buf.toString("latin1", p + 4, p + 8);
    if (type === "IHDR") {
      w = buf.readUInt32BE(p + 8);
      h = buf.readUInt32BE(p + 12);
    }
    if (type === "IDAT") idat.push(buf.subarray(p + 8, p + 8 + len));
    p += 12 + len;
  }
  const raw = inflateSync(Buffer.concat(idat));
  const stride = w * 4 + 1;
  const px = Buffer.alloc(w * h * 4);
  for (let y = 0; y < h; y++) {
    if (raw[y * stride] !== 0) throw new Error("unexpected filter " + raw[y * stride]);
    raw.copy(px, y * w * 4, y * stride + 1, y * stride + 1 + w * 4);
  }
  return { w, h, px };
}

const ico = readFileSync(process.argv[2]);
const off = ico.readUInt32LE(6 + 12);
const len = ico.readUInt32LE(6 + 8);
const img = decodePng(ico.subarray(off, off + len));
const at = (x, y) => {
  const o = (Math.round(y) * img.w + Math.round(x)) * 4;
  return [img.px[o], img.px[o + 1], img.px[o + 2], img.px[o + 3]];
};

const c = (img.w - 1) / 2;
const mid = 0.3 * img.w;
const samples = [];
for (let i = 0; i < 16; i++) {
  const th = (i * Math.PI) / 8;
  samples.push(at(c + mid * Math.cos(th), c + mid * Math.sin(th)));
}

const black = (p) => p[0] < 40 && p[1] < 40 && p[2] < 40;
const checks = {
  "256x256": img.w === 256 && img.h === 256,
  "centre is black": black(at(c, c)),
  "corner is black": black(at(1, 1)),
  "outside the ring is black": black(at(2, c)),
  "hole inside the ring is black": black(at(c + 0.15 * img.w, c)),
  "ring is the accent colour": samples.every((s) => s[2] > 200 && s[0] > 80 && s[0] < 170),
  "ring is a closed circle": samples.every((s) => s[2] === samples[0][2]),
  "tile is opaque": at(c, c)[3] === 255 && samples.every((s) => s[3] === 255),
};

for (const [name, ok] of Object.entries(checks)) {
  console.log(`${ok ? "ok  " : "FAIL"}  ${name}`);
}
const failed = Object.values(checks).filter((ok) => !ok).length;
console.log(failed === 0 ? "PASS: icon is the O-on-black mark" : `FAIL: ${failed} check(s)`);
process.exit(failed === 0 ? 0 : 1);
