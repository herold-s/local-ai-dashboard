import { Call } from '/wails/runtime.js';

const $ = (id) => document.getElementById(id);
const call = (method, ...args) => Call.ByName(`main.MonitorService.${method}`, ...args);
const esc = (value) => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const number = (n, digits = 0) => n == null || !Number.isFinite(n) ? '—' : n.toLocaleString(undefined, {maximumFractionDigits: digits});
const speed = r => r.tokensPerSecond>0?(r.rateBasis==='estimated'?'~':'')+number(r.tokensPerSecond,1):'—';
const gib = n => n == null ? '—' : number(n / 1024 ** 3, 1);
const mibToGib = n => n == null ? '—' : number(n / 1024, 1);
const trails = new Map();
let refreshing = false;
let benchmarking = false;
let hasLocalModels = false;

function notice(id, text) { $(id).textContent = text || ''; $(id).hidden = !text; }

function gpuCard(gpu, index) {
  const samples = trails.get(gpu.id) || [];
  samples.push(gpu.utilization);
  if (samples.length > 30) samples.shift();
  trails.set(gpu.id, samples);
  const paths = [];
  let segment = [];
  samples.forEach((n, i) => {
    if (n == null) { if (segment.length) paths.push(segment); segment = []; return; }
    segment.push(`${(i * 100 / 29).toFixed(2)},${(58 - Math.max(0, Math.min(100, n)) * .52).toFixed(2)}`);
  });
  if (segment.length) paths.push(segment);
  const percentage = gpu.memoryTotal > 0 && gpu.memoryUsed != null ? Math.min(100, gpu.memoryUsed / gpu.memoryTotal * 100) : 0;
  return `<article class="gpu-card"><div class="gpu-top"><div><span class="gpu-index">GPU ${String(index).padStart(2, '0')}</span><h3 class="gpu-name">${esc(gpu.name)}</h3></div><span class="gpu-icon" aria-hidden="true">▦</span></div><div class="gpu-metrics"><div><span>Utilization</span><strong>${number(gpu.utilization)}<small> %</small></strong></div><div><span>Temperature</span><strong>${number(gpu.temperature)}<small> °C</small></strong></div><div><span>Power draw</span><strong>${number(gpu.power)}<small> W</small></strong></div></div><div class="memory-label"><span>VRAM usage</span><span>${mibToGib(gpu.memoryUsed)} / ${gpu.memoryTotal == null ? '—' : number(gpu.memoryTotal / 1024, 1)} GB</span></div><div class="meter" role="meter" aria-label="VRAM utilization" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${percentage.toFixed(1)}"><span style="width:${percentage.toFixed(1)}%"></span></div><div class="chart"><svg viewBox="0 0 100 65" preserveAspectRatio="none" role="img" aria-label="GPU utilization history, up to 150 seconds"><path d="M0 20 H100 M0 40 H100" stroke="#26313d" stroke-width=".25" fill="none"/>${paths.map(points => `<polyline points="${points.join(' ')}" fill="none" stroke="#7de2bd" stroke-width="1.5" vector-effect="non-scaling-stroke"/>`).join('')}</svg></div><div class="chart-label"><span>UTILIZATION · 5s SAMPLES</span><span>LAST 150s</span></div></article>`;
}

async function refresh() {
  if (refreshing) return;
  refreshing = true;
  $('refresh').disabled = true;
  try {
    const state = await call('Snapshot');
    $('gpu-count').textContent = state.gpus.length;
    $('gpu-badge').textContent = state.gpus.length;
    const memoryGPUs = state.gpus.filter(g => g.memoryTotal != null && g.memoryUsed != null);
    const used = memoryGPUs.reduce((n, g) => n + g.memoryUsed, 0);
    const total = memoryGPUs.reduce((n, g) => n + g.memoryTotal, 0);
    $('memory').innerHTML = total ? `${number(used / 1024, 1)} <span class="unit">/ ${number(total / 1024, 1)} GB</span>` : '—';
    $('memory-detail').textContent = total ? `${number(used / total * 100)}% of reported GPU memory` : 'No GPU memory reported';
    const activeIDs = new Set(state.gpus.map(g => g.id));
    for (const key of trails.keys()) if (!activeIDs.has(key)) trails.delete(key);
    $('gpu-cards').innerHTML = state.gpus.map(gpuCard).join('') || '<div class="panel empty">No NVIDIA telemetry available. Local inference can still run through a supported provider, including FastFlowLM on an AMD NPU.</div>';
    notice('gpu-error', state.gpuError);
    const online=state.inferenceOnline??state.ollamaOnline;
    const providerNames=state.providers?.filter(item=>item.online).map(item=>item.name).join(' + ')||'Ollama';
    $('connection').textContent = online ? '● '+providerNames+' online' : '○ Local providers offline';
    $('connection').className = `pill ${online ? '' : 'neutral'}`;
    $('model-count').textContent = online ? state.models.length : '—';
    $('running-count').textContent = online ? `${state.running.length} currently loaded` : 'Start a local provider to connect';
    notice('ollama-error', online?'':(state.ollamaError||'Start Ollama or FastFlowLM to discover local models.'));
    const loaded = new Set(state.running.map(m => m.name));
    $('model-rows').innerHTML = state.models.map(m => `<tr><td><span class="model-name"><span class="model-symbol">◈</span>${esc(m.name.replace(/^fastflowlm::/,"FLM · "))}</span></td><td>${esc(m.details?.parameter_size || '—')}</td><td>${esc(m.details?.quantization_level || '—')}</td><td>${m.provider==="fastflowlm"&&!m.size?"—":gib(m.size)+" GB"}</td><td><span class="pill ${loaded.has(m.name) ? '' : 'neutral'}">${m.provider==='fastflowlm'?'Available':loaded.has(m.name) ? 'Loaded' : 'On disk'}</span></td></tr>`).join('') || `<tr><td colspan="5" class="empty">${online ? 'No installed models. Pull a local model in Ollama to get started.' : 'Start Ollama or FastFlowLM to discover available models.'}</td></tr>`;
    const selected = $('model-select').value;
    const local = state.models.filter(m => !m.name.toLowerCase().includes('cloud'));
    hasLocalModels = local.length > 0;
    // Avoid replacing the selected model while a benchmark is running.
    if (!benchmarking) {
      $('model-select').innerHTML = local.map(m => `<option value="${esc(m.name.replace(/^fastflowlm::/,"FLM · "))}">${esc(m.name.replace(/^fastflowlm::/,"FLM · "))}</option>`).join('') || '<option value="">No local models available</option>';
      if (local.some(m => m.name === selected)) $('model-select').value = selected;
      $('run').disabled = !hasLocalModels;
    }
    $('updated').textContent = `Updated ${new Date(state.timestamp).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit',second:'2-digit'})}`;
  } catch (err) {
    notice('gpu-error', `Unable to refresh: ${err.message || err}`);
    $('connection').textContent = 'Refresh failed';
    $('connection').className = 'pill neutral';
    $('updated').textContent = 'Data may be stale';
  } finally { refreshing = false; $('refresh').disabled = false; }
}

function showResult(r) {
  $('latest-speed').innerHTML = `${speed(r)} <span class="unit">tok/s</span>`;
  $('latest-model').textContent = r.model.replace(/^fastflowlm::/,'FLM · ');
  $('result-speed').innerHTML = `${speed(r)} <span>tok/s</span>`;
  $('result-caption').textContent = r.model.replace(/^fastflowlm::/,'FLM · ');
  $('result-tokens').textContent = r.tokens;
  $('result-duration').textContent = `${number(r.totalSeconds, 2)}s`;
  $('result-load').textContent = r.loadSeconds<0?'—':`${number(r.loadSeconds, 2)}s`;
  $('response').textContent = r.response;
  $('response-details').hidden = false;
}

async function loadHistory(updateResult = true) {
  try {
    const runs = await call('History');
    notice('history-error', '');
    $('history-rows').innerHTML = runs.map(r => `<tr><td>${esc(r.model)}</td><td>${esc(new Date(r.timestamp).toLocaleString())}</td><td>${number(r.tokens)} tokens</td><td>${number(r.totalSeconds, 2)}s</td><td class="speed-cell">${r.rateBasis==="estimated"?"~":""}${number(r.tokensPerSecond, 1)} tok/s</td></tr>`).join('') || '<tr><td colspan="5" class="empty">No runs yet. Set your first baseline in the benchmark lab.</td></tr>';
    if (updateResult && runs.length) showResult(runs[0]);
  } catch (err) { notice('history-error', err.message || String(err)); }
}

$('benchmark-form').addEventListener('submit', async event => {
  event.preventDefault();
  if (benchmarking || !hasLocalModels) return;
  benchmarking = true;
  $('run').disabled = true;
  $('model-select').disabled = true;
  $('prompt').disabled = true;
  $('run').textContent = 'Running…';
  $('result-caption').textContent = 'Generating locally. A cold model may take longer to load (up to 3 minutes).';
  notice('benchmark-error', '');
  try {
    const result = await call('Benchmark', $('model-select').value, $('prompt').value);
    showResult(result);
    notice('benchmark-error', result.saveWarning);
    await loadHistory(false);
  } catch (err) { notice('benchmark-error', err.message || String(err)); $('result-caption').textContent = 'Benchmark failed. Check the message and try again.'; }
  finally {
    benchmarking = false;
    $('run').disabled = !hasLocalModels;
    $('model-select').disabled = false;
    $('prompt').disabled = false;
    $('run').textContent = 'Run benchmark ↗';
    refresh();
  }
});
$('refresh').addEventListener('click', refresh);
document.querySelectorAll('.nav-link').forEach(link => link.addEventListener('click', () => {
  document.querySelectorAll('.nav-link').forEach(item => item.classList.toggle('active', item === link));
  document.querySelector('.breadcrumb strong').textContent = link.textContent.trim().replace(/^\S+\s*/, '');
}));
refresh();
loadHistory();
setInterval(() => { if (!document.hidden) refresh(); }, 5000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) refresh(); });
