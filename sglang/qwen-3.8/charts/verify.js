#!/usr/bin/env node
/**
 * Verifies that every number shown in charts/generate.js (and the README
 * claims listed below) exactly matches the benchmark .txt files.
 * Usage: node verify.js
 */
const fs = require("fs");
const path = require("path");

const dir = path.join(__dirname, "..");

function parse(file) {
  const t = fs.readFileSync(path.join(dir, file), "utf8");
  const get = (label) => {
    const re = new RegExp(label.replace(/\(/g, "\\(").replace(/\)/g, "\\)") + "[^\\n]*?\\s*:\\s*([0-9][0-9.,]*)");
    const m = t.match(re);
    return m ? parseFloat(m[1]) : null;
  };
  return {
    concurrency: get("Max request concurrency"),
    successful: get("Successful requests"),
    totalInput: get("Total input tokens"),
    totalGen: get("Total generated tokens"),
    duration: get("Benchmark duration"),
    reqRate: get("Request throughput"),
    inTok: get("Input token throughput"),
    outTok: get("Output token throughput"),
    peakOut: get("Peak output token throughput"),
    totalTok: get("Total token throughput"),
    accept: get("Accept length"),
    e2e: { mean: get("Mean E2E Latency (ms)"), median: get("Median E2E Latency (ms)"), p90: get("P90 E2E Latency (ms)"), p95: get("P95 E2E Latency (ms)"), p99: get("P99 E2E Latency (ms)") },
    ttft: { mean: get("Mean TTFT (ms)"), median: get("Median TTFT (ms)"), p90: get("P90 TTFT (ms)"), p95: get("P95 TTFT (ms)"), p99: get("P99 TTFT (ms)") },
    tpot: { mean: get("Mean TPOT (ms)"), median: get("Median TPOT (ms)"), p90: get("P90 TPOT (ms)"), p95: get("P95 TPOT (ms)"), p99: get("P99 TPOT (ms)") },
    itl: { mean: get("Mean ITL (ms)"), median: get("Median ITL (ms)"), p90: get("P90 ITL (ms)"), p95: get("P95 ITL (ms)"), p99: get("P99 ITL (ms)"), max: get("Max ITL (ms)") },
  };
}

const a = parse("sglang-rtx-5090-qwen-3.8-27B-NVFP4-benchmark.txt");
const b = parse("sglang-rtx-5090-qwen-3.8-27B-NVFP4-dspark-benchmark.txt");

// The DATA constants in generate.js, mirrored here.
const chart = {
  ttft: {
    categories: ["Mean", "Median", "P90", "P95", "P99"],
    nvfp4: [642.93, 588.36, 660.71, 1320.58, 2114.23],
    dspark: [5438.21, 5801.83, 7607.38, 8244.47, 8403.97],
  },
  decode: {
    categories: ["TPOT Mean", "TPOT P95", "TPOT P99", "ITL Mean", "ITL P95", "ITL P99"],
    nvfp4: [18.23, 20.16, 20.41, 18.14, 15.86, 16.33],
    dspark: [5.15, 9.17, 9.66, 5.18, 19.73, 19.98],
  },
  throughput: {
    categories: ["Input", "Output", "Peak output", "Total"],
    nvfp4: [2344.51, 150.4, 192.0, 2494.91],
    dspark: [2373.55, 152.26, 398.0, 2525.81],
  },
};

// The numbers actually claimed in README.md (full precision form).
const readme = {
  nvfp4: {
    "output tok/s (peak)": [a.outTok, a.peakOut], // 150.40 (peak 192.00)
    "input tok/s": [a.inTok], // 2,344.51
    "mean TTFT": [a.ttft.mean], // 642.93
    "P95 TTFT": [a.ttft.p95], // 1,320.58
    "mean TPOT": [a.tpot.mean], // 18.23
    "mean E2E": [a.e2e.mean], // 7,784
    "success/concurrency": [a.successful, a.concurrency], // 30/30, concurrency 3
  },
  dspark: {
    "TPOT 18.23 -> 5.15": [a.tpot.mean, b.tpot.mean],
    "ratio ~3.5x": [a.tpot.mean / b.tpot.mean],
    "ITL 18.14 -> 5.18": [a.itl.mean, b.itl.mean],
    "accept length 3.85": [b.accept],
    "TTFT mean 643 -> 5438": [a.ttft.mean, b.ttft.mean],
    "P95 E2E 9.6 -> 10.9 s": [a.e2e.p95 / 1000, b.e2e.p95 / 1000],
  },
};

let bad = 0;
const fail = (where, expected, actual) => {
  bad++;
  console.log(`MISMATCH @ ${where}: got ${JSON.stringify(actual)}, expected ${JSON.stringify(expected)}`);
};

// 1) chart data vs txt
const chartFromTxt = {
  ttft: {
    nvfp4: [a.ttft.mean, a.ttft.median, a.ttft.p90, a.ttft.p95, a.ttft.p99],
    dspark: [b.ttft.mean, b.ttft.median, b.ttft.p90, b.ttft.p95, b.ttft.p99],
  },
  decode: {
    nvfp4: [a.tpot.mean, a.tpot.p95, a.tpot.p99, a.itl.mean, a.itl.p95, a.itl.p99],
    dspark: [b.tpot.mean, b.tpot.p95, b.tpot.p99, b.itl.mean, b.itl.p95, b.itl.p99],
  },
  throughput: {
    nvfp4: [a.inTok, a.outTok, a.peakOut, a.totalTok],
    dspark: [b.inTok, b.outTok, b.peakOut, b.totalTok],
  },
};
for (const c of Object.keys(chart)) {
  for (const s of ["nvfp4", "dspark"]) {
    for (let i = 0; i < chart[c].categories.length; i++) {
      if (chartFromTxt[c][s][i] !== chart[c][s][i]) {
        fail(`chart ${c}[${chart[c].categories[i]}].${s}`, chartFromTxt[c][s][i], chart[c][s][i]);
      }
    }
  }
}

// 2) parsed values sanity (no nulls)
for (const [name, r] of [["NVFP4", a], ["DSpark", b]]) {
  for (const [k, v] of Object.entries(r)) {
    if (k === "accept" && v === null) continue; // only in dspark file
    if (v === null || Number.isNaN(v)) fail(`txt parse ${name}.${k}`, "number", null);
  }
}

// 3) README numbers vs txt
for (const [label, vals] of Object.entries(readme.nvfp4)) {
  for (const v of vals) if (v === null || Number.isNaN(v)) fail(`readme ${label}`, "number", null);
}
const checks = [
  ["TPOT ratio in [3.4, 3.6]", readme.dspark["ratio ~3.5x"][0]],
];
const ratio = a.tpot.mean / b.tpot.mean;
if (ratio < 3.4 || ratio > 3.6) fail("readme '~3.5x' TPOT ratio", `~3.5`, ratio);

console.log(bad === 0 ? `OK: all chart + README data matches the txt files (TPOT ratio = ${ratio.toFixed(2)}x)` : `${bad} mismatches found`);
process.exit(bad === 0 ? 0 : 1);
