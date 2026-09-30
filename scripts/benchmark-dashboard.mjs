// Read-only local snapshot benchmark. Does not invoke provider sync or notifications.
import { performance } from 'node:perf_hooks';
const base = process.env.SEC_MONITOR_BASE_URL || 'http://127.0.0.1:9090';
const count = Number(process.env.BENCHMARK_ITERATIONS || 10);
if (!Number.isInteger(count) || count < 3 || count > 100) throw new Error('BENCHMARK_ITERATIONS must be 3..100');
const percentile = (rows, p) => [...rows].sort((a,b)=>a-b)[Math.ceil(rows.length*p)-1];
const results = {};
for (const mode of ['refresh','cached']) {
  const samples = []; const bytes = []; const headers = {};
  for (let i=0;i<count;i++) {
    const start = performance.now();
    const response = await fetch(`${base}/api/dashboard/summary${mode==='refresh'?'?refresh=1':''}`, { signal: AbortSignal.timeout(60000) });
    const body = await response.text();
    if (!response.ok || JSON.parse(body).code !== 0) throw new Error(`Dashboard failed: HTTP ${response.status}`);
    samples.push(performance.now()-start); bytes.push(Buffer.byteLength(body));
    const state = response.headers.get('x-dashboard-cache') || 'unknown'; headers[state]=(headers[state]||0)+1;
  }
  results[mode] = { samples:count, p50_ms:Math.round(percentile(samples,.5)),p95_ms:Math.round(percentile(samples,.95)),max_payload_bytes:Math.max(...bytes),cache_states:headers };
}
console.log(JSON.stringify({ checked_at:new Date().toISOString(), results },null,2));
