const assert = require('node:assert/strict');
const fs = require('node:fs');

const source = fs.readFileSync(`${__dirname}/app.js`, 'utf8');
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

const restoreStart = source.indexOf('async function restoreConversation');
const restoreEnd = source.indexOf('function svgEl', restoreStart);
assert.ok(restoreStart >= 0 && restoreEnd > restoreStart, 'restoreConversation was not found');

const chat = {
  children: [],
  replaceChildren() { this.children = []; },
  appendChild(node) { this.children.push(node); },
  scrollTop: 0,
  scrollHeight: 0,
};
const documentMock = { createElement: () => ({ className: '', textContent: '' }) };
let fetchedURL = '';
const fetchMock = async (url) => {
  fetchedURL = url;
  return {
    ok: true,
    json: async () => ({
      items: [
        { role: 'user', content: 'orphan question' },
        { role: 'assistant', content: JSON.stringify({ status: 'healthy', summary: 'restored', observations: [], hypotheses: [], missing_data: [], recommendations: [] }) },
        { role: 'assistant', content: '{broken' },
        { role: 'user', content: 'question after malformed answer' },
      ],
    }),
  };
};
const restoreConversation = new Function('$', 'fetch', 'document', 'formatAIAnswer', `${source.slice(restoreStart, restoreEnd)}; return restoreConversation;`)(
  (id) => { assert.equal(id, 'chat'); return chat; },
  fetchMock,
  documentMock,
  formatAIAnswer,
);

(async () => {
  await restoreConversation();
  assert.equal(fetchedURL, '/api/ai/history');
  assert.deepEqual(chat.children.map((item) => item.className), ['message user', 'message ai', 'message ai', 'message user']);
  assert.equal(chat.children[0].textContent, 'orphan question');
  assert.match(chat.children[1].textContent, /restored/);
  assert.match(chat.children[2].textContent, /повреждён/);
  assert.equal(chat.children[3].textContent, 'question after malformed answer');
  console.log('web AI answer rendering and history restoration passed');
})().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
