const assert = require('node:assert/strict');
const fs = require('node:fs');

const source = fs.readFileSync(`${__dirname}/app.js`, 'utf8');
assert.match(source, /profileForm/);
assert.match(source, /\/api\/ai\/profile/);
assert.match(source, /\/api\/ai\/profiles/);
assert.match(source, /\/api\/ai\/invariants/);
assert.match(source, /invariantForm/);
assert.match(source, /invariant-delete/);
assert.match(source, /method: 'DELETE'/);
assert.match(source, /invariant_conflict/);
assert.match(source, /method: 'PUT'/);
assert.match(source, /profileMenuButton/);
assert.match(source, /addProfileButton/);
assert.match(source, /\/api\/ai\/task\/state/);
assert.match(source, /\/api\/ai\/task/);
assert.match(source, /taskPauseBtn/);
assert.match(source, /taskContinueBtn/);
assert.match(source, /taskCancelBtn/);
assert.match(source, /\/api\/ai\/task\/cancel/);
assert.match(source, /setInterval\(\(\) =>/);
assert.match(source, /Агент выполняет следующий шаг автоматически/);
assert.match(source, /task_id: taskState\.task_id/);
assert.match(source, /version: taskState\.version/);
assert.match(source, /function taskAcceptsUserInput/);
assert.match(source, /\$\('question'\)\.disabled = !acceptsInput/);
assert.match(source, /if \(response\.status === 404\)/);
assert.doesNotMatch(source, /taskAdvanceBtn/);
assert.doesNotMatch(source, /taskSaveBtn/);
assert.doesNotMatch(source, /taskResumeBtn/);
assert.doesNotMatch(source, /taskRunBtn/);
assert.doesNotMatch(source, /taskNewBtn/);
assert.doesNotMatch(source, /localStorage/);
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

const conflictRendered = formatAIAnswer({
  assistant_schema_version: 'assistant.v2',
  response_type: 'invariant_conflict',
  invariant_conflict: { answer: 'Не могу автоматически завершить процесс.', invariant_ids: ['no-auto-kill'] },
});
assert.match(conflictRendered, /Не могу автоматически завершить процесс/);
assert.match(conflictRendered, /no-auto-kill/);

const taskRenderStart = source.indexOf('function updateAskAvailability');
const taskRenderMiddle = source.indexOf("$('question').addEventListener", taskRenderStart);
const taskStateRenderStart = source.indexOf('function renderTaskState', taskRenderMiddle);
const taskRenderEnd = source.indexOf('async function loadTaskState', taskRenderStart);
assert.ok(taskRenderStart >= 0 && taskRenderMiddle > taskRenderStart && taskStateRenderStart > taskRenderMiddle && taskRenderEnd > taskStateRenderStart, 'task state rendering was not found');
const taskElements = {};
for (const id of ['question', 'sendBtn', 'taskStateCard', 'taskPhase', 'taskStep', 'taskExpectedAction', 'taskStateStatus', 'taskExpectedKind', 'taskPauseBtn', 'taskContinueBtn', 'taskCancelBtn']) {
  taskElements[id] = { disabled: false, value: '', textContent: '', replaceChildren() {} };
}
const renderTaskState = new Function('$', `let aiBusy = false; let taskState = null; ${source.slice(taskRenderStart, taskRenderMiddle)}${source.slice(taskStateRenderStart, taskRenderEnd)}; return renderTaskState;`)(id => taskElements[id]);
renderTaskState(null);
assert.equal(taskElements.taskStateCard.hidden, true);
renderTaskState({ task_id: 'task-1', phase: 'execution', status: 'paused', outcome: 'open', expected_action_kind: 'agent', step: 'collect', expected_action: 'wait', version: 2 });
assert.equal(taskElements.taskStateCard.hidden, false);
assert.equal(taskElements.question.disabled, true);
assert.equal(taskElements.sendBtn.disabled, true);
assert.equal(taskElements.taskContinueBtn.disabled, false);
renderTaskState({ task_id: 'task-1', phase: 'execution', status: 'active', outcome: 'open', expected_action_kind: 'user', step: 'collect', expected_action: 'wait', version: 3 });
assert.equal(taskElements.question.disabled, false);
assert.equal(taskElements.sendBtn.disabled, false);

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
  assert.deepEqual(chat.children.map((item) => item.className), ['message user', 'message ai', 'message user']);
  assert.equal(chat.children[0].textContent, 'orphan question');
  assert.match(chat.children[1].textContent, /повреждён/);
  assert.equal(chat.children[2].textContent, 'question after malformed answer');
  console.log('web AI answer rendering and history restoration passed');
})().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
