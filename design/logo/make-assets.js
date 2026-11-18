// Builds all Fylvo logo files from the traced mark (paths.json) and the Poppins Bold wordmark.
// Needs: npm install sharp opentype.js @fontsource/poppins   (run from a folder that has them)
// Usage: node make-assets.js <repo-root>
const fs = require("fs");
const path = require("path");
const sharp = require("sharp");
const opentype = require("opentype.js");

const repo = path.resolve(process.argv[2] || path.join(__dirname, "..", ".."));
const { union, white } = JSON.parse(fs.readFileSync(path.join(__dirname, "paths.json"), "utf8"));

const ACCENT = "#7484F4";
const INK = "#131C2E"; // dark part of the mark and the wordmark on light backgrounds
const TILE = "#111A2B"; // app icon background

// The mark is traced in a 1024 x 1024 space and covers about x 75..947, y 212..790.
// The accent layer is the whole silhouette, the second layer sits on top of it.
const mark = (top) =>
  `<path fill="${ACCENT}" fill-rule="evenodd" d="${union}"/><path fill="${top}" d="${white}"/>`;

const svg = (viewBox, body, size = "") =>
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${viewBox}"${size}>${body}</svg>\n`;

const markSvg = (top) => svg("60 200 904 604", mark(top));

const iconSvg = (scale = 1) =>
  svg(
    "0 0 1024 1024",
    `<rect width="1024" height="1024" rx="230" fill="${TILE}"/>` +
      `<g transform="translate(512 512) scale(${scale}) translate(-511 -501)">${mark("#FFFFFF")}</g>`,
  );

const fontFile = require.resolve("@fontsource/poppins/files/poppins-latin-700-normal.woff");
const fontData = fs.readFileSync(fontFile);
const font = opentype.parse(fontData.buffer.slice(fontData.byteOffset, fontData.byteOffset + fontData.byteLength));

const lockupSvg = (top, text) => {
  const markHeight = 260;
  const scale = markHeight / 578;
  const word = font.getPath("Fylvo", 0, 0, 264);
  const box = word.getBoundingBox();
  const markWidth = 872 * scale;
  const gap = 64;
  // Center the wordmark's x-height band on the mark, descender of the y hangs below.
  const baseline = 20 + markHeight - 34;
  const width = Math.ceil(20 + markWidth + gap + (box.x2 - box.x1) + 20);
  const height = Math.ceil(Math.max(20 + markHeight + 20, baseline + box.y2 + 20));
  const wordPath = font.getPath("Fylvo", 20 + markWidth + gap - box.x1, baseline, 264).toPathData(2);
  return svg(
    `0 0 ${width} ${height}`,
    `<g transform="translate(20 20) scale(${scale}) translate(-75 -212)">${mark(top)}</g>` +
      `<path fill="${text}" d="${wordPath}"/>`,
  );
};

const write = (rel, data) => {
  const file = path.join(repo, rel);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, data);
  console.log("wrote", rel);
};

(async () => {
  const lockupOnLight = lockupSvg(INK, INK);
  const lockupOnDark = lockupSvg("#FFFFFF", "#FFFFFF");
  const icon = iconSvg();

  write("design/logo/fylvo-mark-on-dark.svg", markSvg("#FFFFFF"));
  write("design/logo/fylvo-mark-on-light.svg", markSvg(INK));
  write("design/logo/fylvo-icon.svg", icon);
  write("design/logo/fylvo-logo-on-light.svg", lockupOnLight);
  write("design/logo/fylvo-logo-on-dark.svg", lockupOnDark);

  write("assets/icon.svg", icon);
  write("frontend/public/static/img/logo.svg", lockupOnLight);
  write("frontend/public/static/img/logo_light.svg", lockupOnDark);
  write("frontend/src/component/Frame/assets/logo.svg", lockupOnLight);
  write("frontend/src/component/Frame/assets/logo_light.svg", lockupOnDark);
  write("frontend/public/static/img/fylvo.svg", icon);

  for (const size of [192, 512]) {
    write(`frontend/public/static/img/logo${size}.png`, await sharp(Buffer.from(icon)).resize(size, size).png().toBuffer());
  }
  // Small sizes get a slightly larger mark so it stays readable in a browser tab.
  for (const size of [16, 32, 48, 64]) {
    write(`design/logo/favicon-${size}.png`, await sharp(Buffer.from(iconSvg(1.12))).resize(size, size).png().toBuffer());
  }
  write("design/logo/preview.png", await sharp(Buffer.from(lockupOnDark)).resize({ height: 200 }).flatten({ background: "#0C131F" }).png().toBuffer());
})();
