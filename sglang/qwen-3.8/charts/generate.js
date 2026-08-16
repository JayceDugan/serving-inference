#!/usr/bin/env node
/**
 * Generates the SVG benchmark charts used in README.md.
 *
 * Data is hard-coded from:
 *   - sglang-rtx-5090-qwen-3.8-27B-NVFP4-benchmark.txt
 *   - sglang-rtx-5090-qwen-3.8-27B-NVFP4-dspark-benchmark.txt
 *
 * Usage: node generate.js   (writes chart-ttft.svg, chart-decode.svg,
 *                            chart-throughput.svg next to this script)
 */
const fs = require("fs");
const path = require("path");

const COLORS = { nvfp4: "#4e79a7", dspark: "#f28e2b", grid: "#e0e0e0", axis: "#555555", text: "#222222", sub: "#777777" };

// ---------------------------------------------------------------------------
// Benchmark data (ms unless noted)
// ---------------------------------------------------------------------------
const DATA = {
  ttft: {
    title: "Time to First Token (TTFT) — mean / median / P90 / P95 / P99",
    unit: "ms",
    categories: ["Mean", "Median", "P90", "P95", "P99"],
    nvfp4: [642.93, 588.36, 660.71, 1320.58, 2114.23],
    dspark: [5438.21, 5801.83, 7607.38, 8244.47, 8403.97],
  },
  decode: {
    title: "Per-token decode latency — TPOT & ITL, mean / P95 / P99",
    unit: "ms",
    categories: ["TPOT Mean", "TPOT P95", "TPOT P99", "ITL Mean", "ITL P95", "ITL P99"],
    nvfp4: [18.23, 20.16, 20.41, 18.14, 15.86, 16.33],
    dspark: [5.15, 9.17, 9.66, 5.18, 19.73, 19.98],
  },
  throughput: {
    title: "Serving throughput",
    unit: "tok/s",
    categories: ["Input", "Output", "Peak output", "Total"],
    nvfp4: [2344.51, 150.4, 192.0, 2494.91],
    dspark: [2373.55, 152.26, 398.0, 2525.81],
  },
};

// ---------------------------------------------------------------------------
// SVG helpers
// ---------------------------------------------------------------------------
function esc(s) {
  return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function fmt(v) {
  // 2 decimals with thousands separators — matches the .txt files exactly.
  return v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function niceMax(v) {
  const mag = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) {
    if (m * mag >= v) return m * mag;
  }
  return 10 * mag;
}

/**
 * Grouped bar chart. data: { title, unit, categories, nvfp4[], dspark[] }
 */
function barChart(data, w = 960, h = 520) {
  const M = { top: 80, right: 30, bottom: 60, left: 80 };
  const plotW = w - M.left - M.right;
  const plotH = h - M.top - M.bottom;

  const n = data.categories.length;
  const groupW = plotW / n;
  const barW = Math.min(48, (groupW * 0.6) / 2);
  const gap = 6;

  const maxV = niceMax(Math.max(...data.nvfp4, ...data.dspark));
  const y = (v) => M.top + plotH - (v / maxV) * plotH;

  const parts = [];
  parts.push(
    `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" font-family="Segoe UI, Helvetica, Arial, sans-serif">`
  );
  parts.push(`<rect width="${w}" height="${h}" fill="#ffffff"/>`);
  parts.push(
    `<text x="${w / 2}" y="30" text-anchor="middle" font-size="20" font-weight="600" fill="${COLORS.text}">${esc(data.title)}</text>`
  );

  // legend
  const legendY = 52;
  parts.push(`<rect x="${w / 2 - 190}" y="${legendY - 11}" width="13" height="13" rx="2" fill="${COLORS.nvfp4}"/>`);
  parts.push(`<text x="${w / 2 - 171}" y="${legendY + 1}" font-size="13" fill="${COLORS.text}">NVFP4 (standard)</text>`);
  parts.push(`<rect x="${w / 2 + 40}" y="${legendY - 11}" width="13" height="13" rx="2" fill="${COLORS.dspark}"/>`);
  parts.push(`<text x="${w / 2 + 59}" y="${legendY + 1}" font-size="13" fill="${COLORS.text}">DSpark (speculative)</text>`);

  // gridlines + y labels
  const ticks = 5;
  for (let i = 0; i <= ticks; i++) {
    const v = (maxV / ticks) * i;
    const yy = y(v);
    parts.push(`<line x1="${M.left}" y1="${yy}" x2="${w - M.right}" y2="${yy}" stroke="${i === 0 ? COLORS.axis : COLORS.grid}"/>`);
    parts.push(
      `<text x="${M.left - 8}" y="${yy + 4}" text-anchor="end" font-size="12" fill="${COLORS.sub}">${v.toLocaleString("en-US", { maximumFractionDigits: 0 })}</text>`
    );
  }
  parts.push(
    `<text x="18" y="${M.top + plotH / 2}" text-anchor="middle" font-size="12" fill="${COLORS.sub}" transform="rotate(-90 18 ${M.top + plotH / 2})">${esc(data.unit)}</text>`
  );

  // bars
  data.categories.forEach((cat, i) => {
    const gx = M.left + groupW * i;
    const cx = gx + groupW / 2;

    const x1 = cx - barW - gap / 2;
    const x2 = cx + gap / 2;
    const heights = [
      [x1, data.nvfp4[i], COLORS.nvfp4],
      [x2, data.dspark[i], COLORS.dspark],
    ];
    for (const [x, v, color] of heights) {
      parts.push(
        `<rect x="${x}" y="${y(v)}" width="${barW}" height="${M.top + plotH - y(v)}" fill="${color}" rx="2">` +
          `<title>${esc(cat)}: ${v} ${esc(data.unit)}</title></rect>`
      );
      parts.push(
        `<text x="${x + barW / 2}" y="${y(v) - 6}" text-anchor="middle" font-size="11.5" fill="${COLORS.text}">${fmt(v)}</text>`
      );
    }
    parts.push(
      `<text x="${cx}" y="${M.top + plotH + 22}" text-anchor="middle" font-size="13" fill="${COLORS.text}">${esc(cat)}</text>`
    );
  });

  parts.push("</svg>");
  return parts.join("\n");
}

const outDir = __dirname;
for (const [name, data] of Object.entries(DATA)) {
  const file = path.join(outDir, `chart-${name}.svg`);
  fs.writeFileSync(file, barChart(data));
  console.log(`wrote ${file}`);
}
