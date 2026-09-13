const $ = (id) => document.getElementById(id);
let history = [];
let aiBusy = false;
let events = null;

const COLORS = { grid: '#24303c', label: '#738095', cpu: '#79a9ec', mem: '#8fd1b0' };

function fmtBytes(value) {
  let v = Number(value) || 0;
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i += 1; }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function fmtRate(value) { return `${fmtBytes(value)}/s`; }

function setStatus(kind, text) {
  $('status').innerHTML = '';
  const dot = document.createElement('span');
  dot.className = `status-dot ${kind || ''}`.trim();
  const label = document.createElement('span');
  label.textContent = text;
  $('status').append(dot, label);
}

function renderProcesses(processes) {
  const tbody = $('processes');
  tbody.replaceChildren();
  if (!Array.isArray(processes) || processes.length === 0) {
    const tr = document.createElement('tr');
    const td = document.createElement('td');
    td.colSpan = 4;
    td.className = 'table-empty';
    td.textContent = 'Нет данных о процессах';
    tr.appendChild(td);
    tbody.appendChild(tr);
    return;
  }
  for (const p of processes) {
    const tr = document.createElement('tr');
    const pid = document.createElement('td');
    pid.textContent = String(p.pid ?? '');
    const name = document.createElement('td');
    const nameWrap = document.createElement('div');
    nameWrap.className = 'process-name';
    nameWrap.textContent = String(p.name ?? '');
    nameWrap.title = nameWrap.textContent;
    name.appendChild(nameWrap);
    const cpu = document.createElement('td');
    cpu.textContent = `${Number(p.cpu_percent || 0).toFixed(1)}%`;
    const mem = document.createElement('td');
    mem.textContent = `${Number(p.mem_percent || 0).toFixed(1)}%`;
    tr.append(pid, name, cpu, mem);
    tbody.appendChild(tr);
  }
}

function applySnapshot(snapshot) {
  $('cpu').textContent = `${Number(snapshot.cpu_percent || 0).toFixed(1)}%`;
  $('cpuSub').textContent = `обновлено ${new Date(snapshot.timestamp).toLocaleTimeString()}`;
  $('memory').textContent = `${fmtBytes(snapshot.mem_used)} / ${fmtBytes(snapshot.mem_total)}`;
  $('memorySub').textContent = `${Number(snapshot.mem_percent || 0).toFixed(1)}% использовано`;
  $('recv').textContent = fmtRate(snapshot.network?.recv_per_sec || 0);
  $('sendSub').textContent = `↑ ${fmtRate(snapshot.network?.send_per_sec || 0)}`;
  $('diskRead').textContent = fmtRate(snapshot.disk_io?.read_per_sec || 0);
  $('diskWrite').textContent = `↑ ${fmtRate(snapshot.disk_io?.write_per_sec || 0)}`;
  $('updatedAt').textContent = new Date(snapshot.timestamp).toLocaleString();
  renderProcesses(snapshot.processes);
}

async function loadInitialState() {
  const [snapshotResp, historyResp, chatHistoryResp] = await Promise.all([
    fetch('/api/snapshot', { cache: 'no-store' }),
    fetch('/api/history?limit=120', { cache: 'no-store' }),
    fetch('/api/ai/history', { cache: 'no-store' }),
  ]);
  if (snapshotResp.ok) applySnapshot(await snapshotResp.json());
  if (historyResp.ok) {
    const payload = await historyResp.json();
    history = Array.isArray(payload.items) ? payload.items : [];
    $('historyCount').textContent = String(history.length);
    drawChart(history);
  }
  if (chatHistoryResp.ok) {
    const payload = await chatHistoryResp.json();
    for (const item of (Array.isArray(payload.items) ? payload.items : [])) {
      if (item.role === 'user') addMessage('user', item.content);
      if (item.role === 'assistant') {
        try { addMessage('ai', formatAIAnswer(JSON.parse(item.content))); } catch { /* ignore malformed legacy rows */ }
      }
    }
  }
}

function addMessage(role, text) {
  const node = document.createElement('div');
  node.className = `message ${role}`;
  node.textContent = text;
  $('chat').appendChild(node);
  $('chat').scrollTop = $('chat').scrollHeight;
}

function svgEl(name, attrs = {}) {
  const node = document.createElementNS('http://www.w3.org/2000/svg', name);
  for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, String(value));
  return node;
}

function drawChart(items) {
  const svg = $('chart');
  const empty = $('chartEmpty');
  const width = Math.max(320, svg.clientWidth || svg.parentElement.clientWidth || 800);
  const height = Math.max(220, svg.clientHeight || svg.parentElement.clientHeight || 330);
  const pad = { left: 42, right: 14, top: 14, bottom: 28 };
  const plotW = Math.max(1, width - pad.left - pad.right);
  const plotH = Math.max(1, height - pad.top - pad.bottom);

  svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
  svg.replaceChildren();

  for (const value of [0, 25, 50, 75, 100]) {
    const y = pad.top + plotH - (value / 100) * plotH;
    svg.appendChild(svgEl('line', { x1: pad.left, y1: y, x2: pad.left + plotW, y2: y, stroke: COLORS.grid, 'stroke-width': 1 }));
    const label = svgEl('text', { x: 5, y: y + 4, fill: COLORS.label, 'font-size': 11, 'font-family': 'system-ui' });
    label.textContent = `${value}%`;
    svg.appendChild(label);
  }

  const usable = Array.isArray(items) ? items.filter(Boolean) : [];
  if (usable.length === 0) {
    empty.classList.remove('hidden');
    return;
  }
  empty.classList.add('hidden');

  const xFor = (i) => pad.left + (usable.length === 1 ? plotW / 2 : (i / (usable.length - 1)) * plotW);
  const yFor = (v) => pad.top + plotH - (Math.max(0, Math.min(100, Number(v) || 0)) / 100) * plotH;

  const series = [
    { key: 'cpu_percent', color: COLORS.cpu },
    { key: 'mem_percent', color: COLORS.mem },
  ];
  for (const { key, color } of series) {
    const points = usable.map((item, i) => `${xFor(i)},${yFor(item[key])}`).join(' ');
    if (usable.length === 1) {
      svg.appendChild(svgEl('circle', { cx: xFor(0), cy: yFor(usable[0][key]), r: 3.5, fill: color }));
      continue;
    }
    svg.appendChild(svgEl('polyline', { points, fill: 'none', stroke: color, 'stroke-width': 2.2, 'stroke-linejoin': 'round', 'stroke-linecap': 'round' }));
  }

  const labelCount = Math.min(6, usable.length);
  for (let i = 0; i < labelCount; i += 1) {
    const index = labelCount === 1 ? 0 : Math.round((i / (labelCount - 1)) * (usable.length - 1));
    const date = new Date(usable[index].timestamp);
    const label = svgEl('text', { x: xFor(index), y: height - 7, fill: COLORS.label, 'font-size': 10, 'font-family': 'system-ui', 'text-anchor': i === 0 ? 'start' : i === labelCount - 1 ? 'end' : 'middle' });
    label.textContent = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    svg.appendChild(label);
  }
}

function subscribe() {
  if (events) events.close();
  events = new EventSource('/api/events');
  events.addEventListener('snapshot', (event) => {
    try {
      const snapshot = JSON.parse(event.data);
      applySnapshot(snapshot);
      history.push(snapshot);
      if (history.length > 120) history.shift();
      $('historyCount').textContent = String(history.length);
      drawChart(history);
      setStatus('', 'Live');
    } catch {
      setStatus('bad', 'Ошибка данных');
    }
  });
  events.onopen = () => setStatus('', 'Live');
  events.onerror = () => setStatus('bad', 'Переподключение…');
}

function formatAIAnswer(answer) {
  if (!answer || typeof answer !== 'object') {
    return 'Пустой ответ от AI';
  }

  const parts = [];

  if (answer.summary) {
    parts.push(answer.summary);
  }

  if (Array.isArray(answer.facts) && answer.facts.length > 0) {
    parts.push(
      'Факты:\n' +
      answer.facts
        .map((fact) => {
          const metric = fact.metric ?? '';
          const value = fact.value ?? '';
          const unit = fact.unit ?? '';

          return `• ${metric}: ${value}${unit ? ` ${unit}` : ''}`;
        })
        .join('\n')
    );
  }

  if (
    Array.isArray(answer.recommendations) &&
    answer.recommendations.length > 0
  ) {
    parts.push(
      'Рекомендации:\n' +
      answer.recommendations
        .map((recommendation) => `• ${recommendation}`)
        .join('\n')
    );
  }

  return parts.join('\n\n') || 'Пустой ответ от AI';
}

$('askForm').addEventListener('submit', async (event) => {
  event.preventDefault();
  if (aiBusy) return;
  const question = $('question').value.trim();
  if (!question) return;
  aiBusy = true;
  $('sendBtn').disabled = true;
  $('sendBtn').textContent = 'Анализ…';
  $('aiError').textContent = '';
  addMessage('user', question);
  $('question').value = '';
  try {
    const response = await fetch('/api/ai/ask', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question }),
    });
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.error || 'AI request failed');
    addMessage('ai', formatAIAnswer(payload.answer));
  } catch (error) {
    $('aiError').textContent = error instanceof Error ? error.message : 'Не удалось получить ответ AI';
  } finally {
    aiBusy = false;
    $('sendBtn').disabled = false;
    $('sendBtn').textContent = 'Спросить';
    $('question').focus();
  }
});

$('question').addEventListener('keydown', (event) => {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault();
    $('askForm').requestSubmit();
  }
});

window.addEventListener('resize', () => drawChart(history));

(async function init() {
  setStatus('pending', 'Подключение…');
  try {
    await loadInitialState();
  } catch {
    setStatus('bad', 'Нет связи с сервером');
  }
  subscribe();
})();
