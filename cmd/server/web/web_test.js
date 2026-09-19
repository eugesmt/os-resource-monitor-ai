const assert = require('node:assert/strict');
const fs = require('node:fs');

const source = fs.readFileSync(`${__dirname}/web/app.js`, 'utf8');
const start = source.indexOf('function formatAIAnswer');
const end = source.indexOf("$('askForm').addEventListener", start);
assert.ok(start >= 0 && end > start, 'formatAIAnswer was not found');
const formatAIAnswer = new Function(`${source.slice(start, end)}; return formatAIAnswer;`)();

const rendered = formatAIAnswer({
  status: 'warning',
  summary: 'CPU high',
  observations: [{ metric: 'process.cpu_percent', value: 88, unit: '%', interpretation: 'hot', pid: 42 }],
  hypotheses: ['worker load'],
  missing_data: ['gpu'],
  recommendations: ['inspect worker'],
});

assert.match(rendered, /Статус: warning/);
assert.match(rendered, /process\.cpu_percent \(PID 42\): 88 % — hot/);
assert.match(rendered, /Гипотезы:/);
assert.match(rendered, /Недостающие данные:/);
assert.match(rendered, /Рекомендации:/);
assert.doesNotMatch(rendered, /facts/i);
console.log('web AI answer rendering passed');
