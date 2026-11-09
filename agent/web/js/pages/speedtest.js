// 网络测速：经内核的测速接口（/api/core/meowx/speedtest，测法与客户端同一个 component/speedtest），从 NAS 经所选节点
// 或直连测延迟 → 下载 → 上传。宽屏左右两栏（左：节点 + 码表 + 开始按钮；右：三格读数 + 逐秒曲线 + 设置），窄屏单列。
// 测完 / 失败的结果记到 NAS 上（/api/speedtest/history），换浏览器也看得到。
import { html, useState, useEffect, useRef } from '../lib.js';
import { get, post, del } from '../api.js';
import { useStore, toast } from '../store.js';
import { Card, Button, Segmented, Select } from '../ui.js';
import { stripPeriod, relativeTime } from '../util.js';

const DIRECT = 'DIRECT';
const SOURCES = [['google', 'Google'], ['microsoft', 'Microsoft'], ['apple', 'Apple'], ['speedtest', 'Speedtest'], ['cloudflare', 'Cloudflare']];
const DOWNLOAD_ONLY = new Set(['google', 'microsoft']);
const RUNNING = new Set(['preparing', 'latency', 'download', 'upload']);
const PHASES = {
    idle: '准备就绪', preparing: '准备中…', latency: '测延迟…', download: '下载中…', upload: '上传中…',
    done: '测完了', failed: '失败', cancelled: '已停止',
};
// 码表刻度：0–500 Mbps 分段线性（低速段拉开，同客户端）
const TICKS = [0, 5, 10, 50, 100, 250, 500];
const SWEEP = 270;

const fmt = (v) => (v == null ? '—' : v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2));

function sourceTitle(id) {
    return SOURCES.find(([v]) => v == id)?.[1] ?? id;
}

// 速率 → 码表上的比例（0–1）
function gaugeRatio(mbps) {
    const v = Math.max(0, mbps || 0);
    const step = 1 / (TICKS.length - 1);
    for (let i = 1; i < TICKS.length; i++) {
        if (v <= TICKS[i]) {
            return (i - 1 + (v - TICKS[i - 1]) / (TICKS[i] - TICKS[i - 1])) * step;
        }
    }
    return 1;
}

function polar(cx, cy, r, deg) {
    const a = (deg - 90) * Math.PI / 180;
    return [cx + r * Math.cos(a), cy + r * Math.sin(a)];
}

function arc(cx, cy, r, from, to) {
    const [x1, y1] = polar(cx, cy, r, from);
    const [x2, y2] = polar(cx, cy, r, to);
    return `M ${x1} ${y1} A ${r} ${r} 0 ${to - from > 180 ? 1 : 0} 1 ${x2} ${y2}`;
}

function Gauge({ mbps, phase, tint }) {
    const start = -SWEEP / 2;
    const ratio = gaugeRatio(mbps);
    const end = start + SWEEP * ratio;
    const big = mbps > 0 ? fmt(mbps) : '0';
    return html`<svg class="speed-gauge" viewBox="0 0 300 260" role="img" aria-label=${`当前 ${big} Mbps`}>
        <path d=${arc(150, 150, 110, start, start + SWEEP)} class="speed-gauge__track" />
        ${ratio > 0.002 && html`<path d=${arc(150, 150, 110, start, end)} class=${'speed-gauge__fill is-' + tint} />`}
        ${TICKS.map((t, i) => {
        const [x, y] = polar(150, 150, 138, start + SWEEP * i / (TICKS.length - 1));
        return html`<text x=${x} y=${y} class="speed-gauge__tick">${t}</text>`;
    })}
        <text x="150" y="160" class="speed-gauge__num">${big}</text>
        <text x="150" y="192" class="speed-gauge__unit">Mbps</text>
        <text x="150" y="222" class="speed-gauge__phase">${PHASES[phase] || phase}</text>
    </svg>`;
}

// 逐秒速率曲线：下载紫、上传玫红，纵轴按两条里最大的值
function Chart({ down, up }) {
    const all = [...down, ...up];
    if (all.length == 0) {
        return html`<div class="speed-chart is-empty"><b>逐秒速率</b><span>开始测速后，下载（紫）与上传（玫红）的曲线画在这里</span></div>`;
    }
    const max = Math.max(1, ...all) * 1.1;
    const n = Math.max(down.length, up.length, 2);
    const line = (s) => s.map((v, i) => `${(i / (n - 1)) * 600},${200 - (v / max) * 190}`).join(' ');
    return html`<div class="speed-chart">
        <svg viewBox="0 0 600 200" preserveAspectRatio="none">
            ${down.length > 1 && html`<polyline points=${line(down)} class="speed-chart__down" />`}
            ${up.length > 1 && html`<polyline points=${line(up)} class="speed-chart__up" />`}
        </svg>
        <span class="speed-chart__max">${fmt(max / 1.1)} Mbps</span>
    </div>`;
}

function Tile({ label, value, unit, peak, cls }) {
    return html`<div class=${'speed-tile ' + (cls || '')}>
        <span class="speed-tile__label">${label}</span>
        <span class="speed-tile__value">${value}<small>${value == '—' ? '' : unit}</small></span>
        <span class="speed-tile__peak">${peak ? `峰值 ${peak}` : ' '}</span>
    </div>`;
}

// 节点下拉：直连 + 订阅里的节点（不列代理组）；缺省取主代理组当前实际在用的节点
function nodeOptions(proxies) {
    const isGroup = (p) => p && Array.isArray(p.all);
    const order = proxies.GLOBAL?.all ?? Object.keys(proxies);
    const nodes = [];
    const seen = new Set();
    const walk = (name) => {
        const p = proxies[name];
        if (!p || seen.has(name)) {
            return;
        }
        seen.add(name);
        if (isGroup(p)) {
            p.all.forEach(walk);
        } else if (!['Direct', 'Reject', 'RejectDrop', 'Pass', 'Compatible'].includes(p.type)) {
            nodes.push(name);
        }
    };
    order.forEach(walk);
    Object.keys(proxies).forEach(walk);
    return nodes;
}

function defaultNode(proxies, mode, nodes) {
    if (mode == 'direct') {
        return DIRECT;
    }
    const isGroup = (p) => p && Array.isArray(p.all);
    const order = proxies.GLOBAL?.all ?? Object.keys(proxies);
    const first = mode == 'global' ? proxies.GLOBAL : order.map((n) => proxies[n]).find((p) => isGroup(p) && !p.hidden && p.name != 'GLOBAL');
    let cur = first;
    for (let i = 0; i < 8 && isGroup(cur); i++) {
        cur = proxies[cur.now];
    }
    if (cur?.type == 'Direct') {
        return DIRECT;
    }
    return cur && nodes.includes(cur.name) ? cur.name : (nodes[0] ?? DIRECT);
}

export function SpeedTest({ active, go }) {
    const { status } = useStore();
    const running = !!status?.core?.running;
    const [proxies, setProxies] = useState(null);
    const [mode, setMode] = useState('');
    const [node, setNode] = useState('');
    const [source, setSource] = useState('google');
    const [multi, setMulti] = useState(false);
    const [prog, setProg] = useState(null);
    const [unsupported, setUnsupported] = useState(false);
    const [history, setHistory] = useState(null);
    const lastRun = useRef(0);   // 已经记过的 runID：测完的结果只记一次
    const testing = RUNNING.has(prog?.phase);

    const loadNodes = async () => {
        try {
            const [r, c] = await Promise.all([get('/core/proxies'), get('/core/configs').catch(() => null)]);
            setProxies(r?.proxies ?? {});
            setMode(String(c?.mode || '').toLowerCase());
        } catch {
            setProxies({});
        }
    };
    const loadHistory = () => get('/speedtest/history').then((r) => setHistory(r?.records ?? []), () => setHistory([]));
    const poll = async () => {
        try {
            const p = await get('/core/meowx/speedtest');
            setUnsupported(false);
            setProg(p);
            return p;
        } catch (err) {
            if (err.status == 404) {
                setUnsupported(true);
            }
            return null;
        }
    };

    useEffect(() => {
        if (active && running) {
            loadNodes();
            poll().then((p) => { lastRun.current = p?.runID ?? 0; });
        }
        if (active) {
            loadHistory();
        }
    }, [active, running, status?.core?.started_at]);

    // 测的时候 300ms 拉一次进度
    useEffect(() => {
        if (!active || !testing) {
            return;
        }
        const t = setInterval(poll, 300);
        return () => clearInterval(t);
    }, [active, testing]);

    // 离开页面就停（同客户端：关掉测速页即停止）
    useEffect(() => () => { post('/core/meowx/speedtest/stop').catch(() => { }); }, []);
    useEffect(() => {
        if (!active && testing) {
            post('/core/meowx/speedtest/stop').catch(() => { });
        }
    }, [active]);

    // 测完 / 失败记一条
    useEffect(() => {
        if (!prog || !['done', 'failed'].includes(prog.phase) || prog.runID <= lastRun.current) {
            return;
        }
        lastRun.current = prog.runID;
        const record = {
            at: Math.floor(Date.now() / 1000), via: prog.via, source: prog.source, server: prog.server || '',
            multi, latency_ms: prog.latencyMs ?? null, download_mbps: prog.downloadMbps ?? null, upload_mbps: prog.uploadMbps ?? null,
            bytes: prog.bytesUsed || 0, error: prog.phase == 'failed' ? (prog.error || '测速失败') : '',
            exit: prog.exit ? [prog.exit.org, prog.exit.country].filter(Boolean).join(' · ') : '',
        };
        post('/speedtest/history', record).then((r) => setHistory(r?.records ?? []), () => { });
    }, [prog?.runID, prog?.phase]);

    const nodes = proxies ? nodeOptions(proxies) : [];
    const chosen = node && (node == DIRECT || nodes.includes(node)) ? node : (proxies ? defaultNode(proxies, mode, nodes) : '');

    const start = async () => {
        if (!confirm('测速会在十几秒里尽量占满带宽，局域网里其它设备会变慢，并会照实消耗流量（可能数百 MB）。开始测速？')) {
            return;
        }
        try {
            await post('/core/meowx/speedtest/start', { source, proxy: chosen, connections: multi ? 8 : 1 });
        } catch (err) {
            toast(stripPeriod(err.message) || '没能开始测速', 'warn');
            return;
        }
        await poll();
    };
    const stop = async () => {
        await post('/core/meowx/speedtest/stop').catch(() => { });
        await poll();
    };

    if (!running) {
        return html`<${Card} title="网络测速">
            <div class="empty">内核没在运行，启动后才能测速。<br /><br />
                <${Button} kind="primary" onClick=${() => go('overview')}>去概览启动<//></div>
        <//>`;
    }
    if (unsupported) {
        return html`<${Card} title="网络测速"><div class="empty">当前内核不支持测速，请更新 MeowX。</div><//>`;
    }

    const p = prog || { phase: 'idle', downloadSamples: [], uploadSamples: [] };
    const tint = p.phase == 'upload' ? 'up' : 'down';
    const shown = testing ? p.currentMbps : (p.downloadMbps ?? 0);
    const info = [p.server && `服务器 ${p.server}`, p.via && `经由 ${p.via == DIRECT ? '直连' : p.via}`, p.bytesUsed > 0 && `已用 ${(p.bytesUsed / 1048576).toFixed(0)} MB`]
        .filter(Boolean).join(' · ');
    const exit = p.exit ? [p.exit.org, p.exit.country].filter(Boolean).join(' · ') : '';

    return html`<div class="speed">
        <div class="speed__left">
            <${Card} class="speed__dial">
                <label class="speed-node">
                    <span class="speed-node__label">测速节点</span>
                    <${Select} value=${chosen} onChange=${setNode} options=${[[DIRECT, '直连（DIRECT，测 NAS 本身的网络）'], ...nodes.map((n) => [n, n])]} />
                </label>
                <${Gauge} mbps=${shown} phase=${p.phase} tint=${tint} />
                ${exit && html`<div class="speed__exit">出口 ${exit}</div>`}
            <//>
            <${Button} kind=${testing ? '' : 'primary'} onClick=${testing ? stop : start} disabled=${!chosen}>${testing ? '停止' : '开始测速'}<//>
        </div>
        <div class="speed__right">
            <${Card} class="speed__panel">
                <div class="speed-tiles">
                    <${Tile} label="延迟" value=${p.latencyMs != null ? String(p.latencyMs) : '—'} unit=" ms" />
                    <${Tile} label="下载" cls="is-down" value=${fmt(p.downloadMbps)} unit=" Mbps" peak=${p.downloadPeakMbps != null ? fmt(p.downloadPeakMbps) : ''} />
                    <${Tile} label="上传" cls="is-up" value=${DOWNLOAD_ONLY.has(p.source) && p.phase == 'done' ? '不测' : fmt(p.uploadMbps)} unit=" Mbps" peak=${p.uploadPeakMbps != null ? fmt(p.uploadPeakMbps) : ''} />
                </div>
                <${Chart} down=${p.downloadSamples ?? []} up=${p.uploadSamples ?? []} />
                <div class=${'speed__info' + (p.phase == 'failed' ? ' error-text' : '')}>${p.phase == 'failed' ? stripPeriod(p.error || '测速失败') : info || ' '}</div>
            <//>
            <${Card} class="speed__settings">
                <div class="speed__label">测速源</div>
                <${Segmented} full value=${source} onChange=${setSource} options=${SOURCES} disabled=${testing} />
                <div class="speed__label">线程</div>
                <${Segmented} full value=${multi ? 'm' : 's'} onChange=${(v) => setMulti(v == 'm')} options=${[['s', '单线程'], ['m', '多线程（8）']]} disabled=${testing} />
                <p class="hint">测的是 NAS 经所选节点出去的速度，不经分流规则（只能选节点或直连）。先测延迟，再下载、上传各 10 秒（Google、Microsoft 只测下载）。数据边收边丢，但会照实消耗流量。</p>
            <//>
        </div>
    </div>
    <${History} records=${history} onClear=${async () => {
        if (confirm('清空全部测速记录？')) {
            await del('/speedtest/history');
            setHistory([]);
        }
    }} />`;
}

function History({ records, onClear }) {
    if (!records || records.length == 0) {
        return null;
    }
    return html`<${Card} title="测速记录" actions=${html`<${Button} small kind="danger" onClick=${onClear}>清空<//>`}>
        <div class="list">${records.map((r) => html`<div class="item" key=${r.at + r.via}>
            <div class="item__main">
                <div class="item__name">${r.via == DIRECT ? '直连' : r.via}<span class="badge is-muted" style="margin-left:8px">${sourceTitle(r.source)}${r.multi ? ' · 多线程' : ''}</span></div>
                <div class=${'item__meta' + (r.error ? ' error-text' : '')}>${r.error
            ? `失败：${stripPeriod(r.error)}`
            : [r.download_mbps != null && `↓ ${fmt(r.download_mbps)} Mbps`, r.upload_mbps != null && `↑ ${fmt(r.upload_mbps)} Mbps`, r.latency_ms != null && `${r.latency_ms} ms`, r.exit]
                .filter(Boolean).join(' · ')}</div>
            </div>
            <span class="muted" style="white-space:nowrap">${relativeTime(r.at)}</span>
        </div>`)}</div>
    <//>`;
}
