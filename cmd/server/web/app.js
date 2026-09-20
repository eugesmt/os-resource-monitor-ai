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

function gpuMemoryPercent(gpu) {
  const total = Number(gpu?.memory_total) || 0;
  const used = Number(gpu?.memory_used) || 0;
  return total > 0 ? (used / total) * 100 : 0;
}

function renderGPUs(gpus, missingData) {
  const list = $('gpuList');
  if (list) list.replaceChildren();
  if (!Array.isArray(gpus) || gpus.length === 0) {
    if (list) {
      const empty = document.createElement('div');
      empty.className = 'table-empty';
      empty.textContent = Array.isArray(missingData) && missingData.includes('gpu')
        ? 'NVIDIA GPU или NVML недоступны'
        : 'Нет данных о GPU';
      list.appendChild(empty);
    }
    $('gpu').textContent = '—';
    $('gpuSub').textContent = 'Данные недоступны';
    return;
  }

  const maxUtilization = Math.max(...gpus.map((gpu) => Number(gpu.utilization_percent) || 0));
  const maxTemperature = Math.max(...gpus.map((gpu) => Number(gpu.temperature_celsius) || 0));
  const totalMemory = gpus.reduce((sum, gpu) => sum + (Number(gpu.memory_total) || 0), 0);
  const usedMemory = gpus.reduce((sum, gpu) => sum + (Number(gpu.memory_used) || 0), 0);
  $('gpu').textContent = gpus.length === 1 ? String(gpus[0].name || 'NVIDIA GPU') : `${gpus.length} GPU`;
  $('gpuSub').textContent = `${maxUtilization.toFixed(0)}% · VRAM ${(totalMemory ? usedMemory / totalMemory * 100 : 0).toFixed(1)}% · ${maxTemperature.toFixed(0)} °C`;

  for (const gpu of gpus) {
    const row = document.createElement('div');
    row.className = 'gpu-row';
    const name = document.createElement('div');
    name.className = 'gpu-name';
    name.textContent = String(gpu.name || `GPU ${gpu.index ?? ''}`);
    const stats = document.createElement('div');
    stats.className = 'gpu-stats';
    stats.textContent = `GPU ${Number(gpu.utilization_percent || 0).toFixed(0)}% · VRAM ${gpuMemoryPercent(gpu).toFixed(1)}% (${fmtBytes(gpu.memory_used)} / ${fmtBytes(gpu.memory_total)}) · ${Number(gpu.temperature_celsius || 0).toFixed(0)} °C`;
    row.append(name, stats);
    if (list) list.appendChild(row);
  }
}

function gpuSnapshotStats(snapshot) {
  const gpus = Array.isArray(snapshot?.gpu) ? snapshot.gpu : [];
  if (gpus.length === 0) return null;
  const totalMemory = gpus.reduce((sum, gpu) => sum + (Number(gpu.memory_total) || 0), 0);
  const usedMemory = gpus.reduce((sum, gpu) => sum + (Number(gpu.memory_used) || 0), 0);
  return {
    time: snapshot.timestamp || snapshot.captured_at,
    utilization: Math.max(...gpus.map((gpu) => Number(gpu.utilization_percent) || 0)),
    memory: totalMemory > 0 ? usedMemory / totalMemory * 100 : 0,
    temperature: Math.max(...gpus.map((gpu) => Number(gpu.temperature_celsius) || 0)),
  };
}

function drawGPUChart(svgId, emptyId, points, color, maxValue, unit) {
  const svg = $(svgId);
  const empty = $(emptyId);
  const width = Math.max(260, svg.clientWidth || svg.parentElement.clientWidth || 360);
  const height = 180;
  const pad = { left: 38, right: 10, top: 12, bottom: 22 };
  const plotW = Math.max(1, width - pad.left - pad.right);
  const plotH = Math.max(1, height - pad.top - pad.bottom);
  svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
  svg.replaceChildren();
  if (points.length === 0) {
    empty.classList.remove('hidden');
    return;
  }
  empty.classList.add('hidden');
  const xFor = (i) => pad.left + (points.length === 1 ? plotW / 2 : i / (points.length - 1) * plotW);
  const yFor = (value) => pad.top + plotH - Math.max(0, Math.min(maxValue, value)) / maxValue * plotH;
  for (const value of [0, maxValue / 2, maxValue]) {
    const y = yFor(value);
    svg.appendChild(svgEl('line', { x1: pad.left, y1: y, x2: pad.left + plotW, y2: y, stroke: COLORS.grid, 'stroke-width': 1 }));
    const label = svgEl('text', { x: 4, y: y + 4, fill: COLORS.label, 'font-size': 10, 'font-family': 'system-ui' });
    label.textContent = `${value.toFixed(0)}${unit}`;
    svg.appendChild(label);
  }
  const coordinates = points.map((point, index) => `${xFor(index)},${yFor(point.value)}`).join(' ');
  if (points.length === 1) {
    svg.appendChild(svgEl('circle', { cx: xFor(0), cy: yFor(points[0].value), r: 3.5, fill: color }));
  } else {
    svg.appendChild(svgEl('polyline', { points: coordinates, fill: 'none', stroke: color, 'stroke-width': 2.2, 'stroke-linejoin': 'round', 'stroke-linecap': 'round' }));
  }
}

function drawGPUCharts(items) {
  const stats = (Array.isArray(items) ? items : []).map(gpuSnapshotStats).filter(Boolean);
  drawGPUChart('gpuUtilChart', 'gpuUtilEmpty', stats.map((point) => ({ value: point.utilization })), '#c994f2', 100, '%');
  drawGPUChart('gpuMemoryChart', 'gpuMemoryEmpty', stats.map((point) => ({ value: point.memory })), '#79c7ec', 100, '%');
  drawGPUChart('gpuTemperatureChart', 'gpuTemperatureEmpty', stats.map((point) => ({ value: point.temperature })), '#efad76', 100, '°');
}

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

function renderCPUCores(values, missingData) {
  const container = $('cpuCores');
  container.replaceChildren();
  if (!Array.isArray(values) || values.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'table-empty';
    empty.textContent = Array.isArray(missingData) && missingData.includes('cpu_per_core')
      ? 'Данные по ядрам недоступны'
      : 'Нет данных по ядрам';
    container.appendChild(empty);
    return;
  }
  values.forEach((value, index) => {
    const row = document.createElement('div');
    row.className = 'core-row';
    const label = document.createElement('span');
    label.className = 'core-label';
    label.textContent = `Ядро ${index}`;
    const track = document.createElement('div');
    track.className = 'core-track';
    const fill = document.createElement('div');
    fill.className = 'core-fill';
    fill.style.width = `${Math.max(0, Math.min(100, Number(value) || 0))}%`;
    track.appendChild(fill);
    const amount = document.createElement('span');
    amount.className = 'core-value';
    amount.textContent = `${Number(value || 0).toFixed(1)}%`;
    row.append(label, track, amount);
    container.appendChild(row);
  });
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
  renderCPUCores(snapshot.cpu_per_core, snapshot.missing_data);
  renderGPUs(snapshot.gpu, snapshot.missing_data);
  $('updatedAt').textContent = new Date(snapshot.timestamp).toLocaleString();
  renderProcesses(snapshot.processes);
}

async function loadInitialState() {
  const [snapshotResp, historyResp] = await Promise.all([
    fetch('/api/snapshot', { cache: 'no-store' }),
    fetch('/api/history?limit=120', { cache: 'no-store' }),
  ]);
  if (snapshotResp.ok) applySnapshot(await snapshotResp.json());
  if (historyResp.ok) {
    const payload = await historyResp.json();
    history = Array.isArray(payload.items) ? payload.items : [];
    $('historyCount').textContent = String(history.length);
    drawChart(history);
    drawGPUCharts(history);
  }
}

async function restoreConversation() {
  const response = await fetch('/api/ai/history', { cache: 'no-store' });
  if (!response.ok) throw new Error('Не удалось восстановить историю диалога');
  const payload = await response.json();
  const items = Array.isArray(payload.items) ? payload.items : [];
  $('chat').replaceChildren();
  for (const item of items) {
    if (item.role === 'user') {
      addMessage('user', String(item.content || ''));
      continue;
    }
    if (item.role !== 'assistant') continue;
    try {
      addMessage('ai', formatAIAnswer(JSON.parse(item.content)));
    } catch {
      addMessage('ai', 'Сохранённый ответ AI повреждён и не может быть показан.');
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
      drawGPUCharts(history);
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

  if (answer.assistant_schema_version === 'assistant.v2') {
    if (answer.response_type === 'diagnosis' && answer.diagnosis) {
      return formatAIAnswer(answer.diagnosis);
    }
    if (answer.response_type === 'conversation_recall' && answer.conversation_recall) {
      return answer.conversation_recall.answer || 'Пустой ответ от AI';
    }
    if (answer.response_type === 'metric_comparison' && answer.metric_comparison) {
      const comparison = answer.metric_comparison;
      const points = Array.isArray(comparison.points) ? comparison.points : [];
      const renderedPoints = points.map((point) => `${point.metric ?? ''}: ${point.value ?? ''}${point.unit ? ` ${point.unit}` : ''}`).join('\n');
      return [comparison.answer || '', renderedPoints ? `Наблюдения:\n${renderedPoints}` : ''].filter(Boolean).join('\n\n') || 'Пустой ответ от AI';
    }
    if (answer.response_type === 'general_answer' && answer.general_answer) {
      return answer.general_answer.answer || 'Пустой ответ от AI';
    }
  }

  const parts = [];

  if (answer.status) {
    parts.push(`Статус: ${answer.status}`);
  }

  if (answer.summary) {
    parts.push(answer.summary);
  }

  if (Array.isArray(answer.observations) && answer.observations.length > 0) {
    parts.push(
      'Наблюдения:\n' +
      answer.observations
        .map((observation) => {
          const metric = observation.metric ?? '';
          const value = observation.value ?? '';
          const unit = observation.unit ?? '';
          const interpretation = observation.interpretation ?? '';
          const pid = observation.pid == null ? '' : ` (PID ${observation.pid})`;
          const detail = interpretation ? ` — ${interpretation}` : '';

          return `• ${metric}${pid}: ${value}${unit ? ` ${unit}` : ''}${detail}`;
        })
        .join('\n')
    );
  }

  if (Array.isArray(answer.hypotheses) && answer.hypotheses.length > 0) {
    parts.push(
      'Гипотезы:\n' +
      answer.hypotheses.map((hypothesis) => `• ${hypothesis}`).join('\n')
    );
  }

  if (Array.isArray(answer.missing_data) && answer.missing_data.length > 0) {
    parts.push(
      'Недостающие данные:\n' +
      answer.missing_data.map((missing) => `• ${missing}`).join('\n')
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

function renderMemory(context) {
  const displayContent = (item) => {
    if (item.kind !== 'assistant') return item.content;
    try {
      const response = JSON.parse(item.content);
      const type = response.response_type;
      if (type === 'diagnosis') return response.diagnosis?.summary || 'Диагностический ответ';
      if (type === 'general_answer') return response.general_answer?.answer || 'Общий ответ';
      if (type === 'conversation_recall') return response.conversation_recall?.answer || 'Ответ по истории диалога';
      if (type === 'metric_comparison') return response.metric_comparison?.summary || 'Сравнение метрик';
    } catch {
      // Keep non-JSON historical content readable.
    }
    return item.content;
  };
  window.memoryContext = context;
  const active = document.querySelector('.memory-tab.active')?.dataset.memoryTab || 'short_term';
  const records = Array.isArray(context[active]) ? context[active] : [];
  const view = $('memoryView');
  view.replaceChildren();
  if (!records.length) {
    view.textContent = 'Этот слой памяти пуст';
    return;
  }
  records.forEach((item) => {
    const row = document.createElement('div');
    row.className = 'memory-record';
    const text = document.createElement('span');
    text.textContent = `${item.kind}: ${displayContent(item)}`;
    if (item.layer !== 'short_term') {
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'memory-delete';
      remove.textContent = 'Удалить';
      remove.dataset.memoryId = item.id;
      row.append(text, remove);
    } else {
      row.append(text);
    }
    view.append(row);
  });
}

document.querySelectorAll('.memory-tab').forEach((tab) => {
  tab.addEventListener('click', () => {
    document.querySelectorAll('.memory-tab').forEach((item) => item.classList.toggle('active', item === tab));
    if (window.memoryContext) renderMemory(window.memoryContext);
  });
});

$('memoryView').addEventListener('click', async (event) => {
  const button = event.target.closest('.memory-delete');
  if (!button) return;
  button.disabled = true;
  try {
    const response = await fetch(`/api/ai/memory/${encodeURIComponent(button.dataset.memoryId)}`, { method: 'DELETE' });
    if (!response.ok) throw new Error('Не удалось удалить запись');
    await loadMemory();
  } catch (error) {
    $('memoryError').textContent = error instanceof Error ? error.message : 'Ошибка удаления памяти';
    button.disabled = false;
  }
});

async function loadMemory() {
  const response = await fetch('/api/ai/memory');
  const payload = await response.json();
  if (!response.ok) throw new Error(payload.error || 'Не удалось загрузить память');
  renderMemory(payload.context);
}

if ($('memoryForm')) {
  $('memoryForm').addEventListener('submit', async (event) => {
    event.preventDefault();
    $('memoryError').textContent = '';
    try {
      const response = await fetch('/api/ai/memory', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          layer: document.querySelector('.memory-tab.active')?.dataset.memoryTab || 'short_term',
          scope: 'default',
          kind: $('memoryKind').value.trim(),
          content: $('memoryContent').value.trim(),
        }),
      });
      const payload = await response.json();
      if (!response.ok) throw new Error(payload.error || 'Не удалось сохранить память');
      $('memoryKind').value = '';
      $('memoryContent').value = '';
      await loadMemory();
    } catch (error) {
      $('memoryError').textContent = error instanceof Error ? error.message : 'Ошибка памяти';
    }
  });
}

window.addEventListener('resize', () => drawChart(history));

(async function init() {
  setStatus('pending', 'Подключение…');
  try {
    await loadInitialState();
    await restoreConversation();
    await loadMemory();
  } catch {
    setStatus('bad', 'Нет связи с сервером');
    $('aiError').textContent = 'Не удалось восстановить историю диалога.';
  } finally {
    $('sendBtn').disabled = false;
  }
  subscribe();
})();
