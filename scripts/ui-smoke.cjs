// Optional frontend smoke test: npm install --no-save playwright, then node scripts/ui-smoke.cjs.
// Fixtures are served only by this test server and never included in the desktop application.
const { chromium } = require('playwright');
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const root = path.resolve(__dirname, '../frontend/public/monitor');
let online = false;
const runs = [];
const snapshot = () => ({ timestamp: new Date().toISOString(), gpus: online ? [
  {id:'test-a', name:'NVIDIA GeForce RTX 5080', memoryUsed:2048, memoryTotal:16303, utilization:17, temperature:45, power:64},
  {id:'test-b', name:'NVIDIA GeForce RTX 3070', memoryUsed:null, memoryTotal:8192, utilization:null, temperature:39, power:null}
] : [], gpuError: online ? '' : 'Test: NVIDIA telemetry unavailable', ollamaOnline:online, ollamaError: online ? '' : 'Test: Ollama offline', models: online ? [
  {name:'test-coder:7b',size:5e9,details:{parameter_size:'7B',quantization_level:'Q4_K_M'}}
] : [], running: [] });
const server = http.createServer((req,res) => {
  if (req.url === '/wails/runtime.js') {
    res.setHeader('Content-Type','text/javascript');
    return res.end(`export const Call = {ByName: async(name,...args) => {
      const res = await fetch('/__call', {method:'POST',body:JSON.stringify({name,args})});
      const data = await res.json(); if (!res.ok) throw new Error(data.error); return data;
    }};`);
  }
  if (req.url === '/__call') {
    let body = ''; req.on('data', chunk => body += chunk);
    req.on('end', () => {
      const {name} = JSON.parse(body);
      res.setHeader('Content-Type','application/json');
      if (name.endsWith('.Snapshot')) return res.end(JSON.stringify(snapshot()));
      if (name.endsWith('.History')) return res.end(JSON.stringify(runs));
      if (name.endsWith('.Benchmark')) {
        const result = {timestamp:new Date().toISOString(),model:'test-coder:7b',tokens:100,tokensPerSecond:50,totalSeconds:3.5,loadSeconds:.5,response:'Test output <script>must stay text</script>'};
        runs.unshift(result); return setTimeout(() => res.end(JSON.stringify(result)),150);
      }
      res.writeHead(404); res.end('{}');
    }); return;
  }
  const file = {'/':'index.html','/app.js':'app.js','/style.css':'style.css'}[req.url];
  if (!file) {res.writeHead(404);return res.end();}
  res.setHeader('Content-Type', file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html');
  res.end(fs.readFileSync(path.join(root,file)));
});
(async () => {
  await new Promise(resolve => server.listen(0,'127.0.0.1',resolve));
  let browser;
  try {
    browser = await chromium.launch({headless:true, channel:process.env.DASHBOARD_BROWSER || 'msedge'});
    const page = await browser.newPage({viewport:{width:1240,height:860}});
    const errors=[]; page.on('pageerror', error => errors.push(error.message));
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.waitForFunction(() => document.querySelector('#connection').textContent.includes('offline'));
    assert(await page.locator('#run').isDisabled());
    assert.equal(await page.locator('#gpu-count').textContent(),'0');
    online=true; await page.locator('#refresh').click();
    await page.waitForFunction(() => document.querySelector('#gpu-count').textContent==='2');
    assert.equal(await page.locator('.gpu-card').count(),2);
    assert((await page.locator('.gpu-card').nth(1).textContent()).includes('— / 8'));
    assert(await page.locator('#run').isEnabled());
    await page.locator('#run').click();
    await page.waitForFunction(() => document.querySelector('#result-speed').textContent.includes('50'));
    await page.waitForFunction(() => document.querySelector('#history-rows').textContent.includes('test-coder'));
    assert.equal(await page.locator('#response script').count(),0);
    assert((await page.locator('#response').textContent()).includes('<script>'));
    await page.locator('a[href="#benchmark"]').click();
    assert((await page.locator('.breadcrumb strong').textContent()).includes('Benchmark'));
    await page.locator('a[href="#overview"]').last().click();
    await page.screenshot({path:path.resolve(__dirname,'../../dashboard-ui-check.png'),fullPage:true});
    await page.setViewportSize({width:900,height:650});
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
    assert.deepEqual(errors,[]);
    console.log('UI smoke passed: offline, two GPUs, unavailable metrics, models, benchmark, history, safe text output, navigation, 900px layout.');
  } finally { if (browser) await browser.close(); server.close(); }
})().catch(error => {console.error(error);process.exitCode=1;});
