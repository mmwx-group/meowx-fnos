// 网络测速：经内核的测速接口（/api/core/meowx/speedtest，测法与客户端同一个 component/speedtest），从 NAS 经所选节点
// 或直连测延迟 → 下载 → 上传。宽屏左右两栏（左：节点 + 码表 + 开始按钮；右：三格读数 + 逐秒曲线 + 设置），窄屏单列。
// 测完 / 失败的结果记到 NAS 上（/api/speedtest/history），换浏览器也看得到。
// 测速榜（../speedboard.js）：第一次打开问一次要不要上传，同意了测完自动传，结果链接与删除凭据写回那条记录。
import { html, useState, useEffect, useRef } from '../lib.js';
import { get, post, put, patch, del, seg } from '../api.js';
import { useStore, toast } from '../store.js';
import { Card, Button, Segmented, Select, Switch, Dialog } from '../ui.js';
import { stripPeriod, relativeTime } from '../util.js';
import { BOARD_PAGE, blocker, copyText, deleteRecord, downsample, protocolCode, uploadRecord } from '../speedboard.js';

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
    const [board, setBoard] = useState(null);           // 测速榜：{asked, upload, install_id}
    const [askBoard, setAskBoard] = useState(false);    // 同意弹窗
    const [uploading, setUploading] = useState(() => new Set());
    const [currentId, setCurrentId] = useState('');     // 本页这一次测完记下的记录：码表下面显示它在测速榜上的状态
    const lastRun = useRef(0);   // 已经记过的 runID：测完的结果只记一次
    const runInfo = useRef(null); // 开始时定下来的线程数与节点协议（测完时下拉框与订阅都可能变了）
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

    // 测速榜设置；没问过就弹一次同意框
    useEffect(() => {
        if (!active || board) {
            return;
        }
        get('/speedtest/board').then((b) => {
            setBoard(b);
            if (!b?.asked) {
                setAskBoard(true);
            }
        }, () => { });
    }, [active]);

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
        const done = prog.phase == 'done';
        const info = runInfo.current ?? { multi, protocol: null };
        // 采样压到 40 个点（详情与上传正文都够用），出口不记 IP
        const record = {
            id: `${Date.now().toString(16)}-${Math.floor(Math.random() * 0xffffffff).toString(16)}`,
            at: Math.floor(Date.now() / 1000), via: prog.via, source: prog.source, server: prog.server || '',
            multi: info.multi, protocol: info.protocol, latency_ms: prog.latencyMs ?? null, download_mbps: prog.downloadMbps ?? null, upload_mbps: prog.uploadMbps ?? null,
            down_peak: prog.downloadPeakMbps ?? null, up_peak: prog.uploadPeakMbps ?? null,
            down_samples: downsample(prog.downloadSamples ?? []), up_samples: downsample(prog.uploadSamples ?? []),
            duration_ms: done ? (prog.durationMs ?? null) : null,
            bytes: prog.bytesUsed || 0, error: done ? '' : (prog.error || '测速失败'),
            exit: prog.exit ? [prog.exit.org, prog.exit.country].filter(Boolean).join(' · ') : '',
            claimed_exit: prog.exit ? { asn: prog.exit.asn ?? 0, org: prog.exit.org ?? '', country: prog.exit.country ?? '' } : null,
        };
        setCurrentId(done ? record.id : '');
        post('/speedtest/history', record).then((r) => {
            setHistory(r?.records ?? []);
            if (done && board?.upload && !blocker(record)) {
                upload(record);
            }
        }, () => { });
    }, [prog?.runID, prog?.phase]);

    // ---- 测速榜 ----
    const setUploadingId = (id, on) => setUploading((cur) => {
        const next = new Set(cur);
        on ? next.add(id) : next.delete(id);
        return next;
    });
    const saveBoard = (id, value) => patch(`/speedtest/history/${seg(id)}`, { board: value }).then((r) => setHistory(r?.records ?? []));
    const upload = async (rec) => {
        if (uploading.has(rec.id)) {
            return;
        }
        setUploadingId(rec.id, true);
        let result;
        try {
            result = await uploadRecord(rec, { installId: board?.install_id, platform: status?.platform || 'fnos', version: status?.version || '' });
        } catch (err) {
            result = { status: 'failed', error: err?.message || String(err), retryable: true };
        }
        try {
            await saveBoard(rec.id, result);
        } catch (err) {
            toast(stripPeriod(err.message) || '保存测速记录失败', 'warn');
        } finally {
            setUploadingId(rec.id, false);
        }
    };
    const removeFromBoard = async (rec) => {
        if (!confirm('从测速榜删除这条结果？结果页将无法打开，NAS 上的记录保留。')) {
            return;
        }
        const res = await deleteRecord(rec);
        if (!res.ok) {
            toast(res.message, 'warn');
            return;
        }
        await saveBoard(rec.id, null);
    };
    const setBoardUpload = async (on) => {
        setAskBoard(false);
        setBoard((b) => ({ ...(b ?? {}), asked: true, upload: on }));
        try {
            setBoard(await put('/speedtest/board', { upload: on }));
        } catch (err) {
            toast(stripPeriod(err.message) || '保存失败', 'warn');
        }
    };
    const resetIdentity = async () => {
        if (!confirm('重置这台 NAS 在测速榜上的身份？之后上传的结果算一台新设备，已经上传的结果保留。')) {
            return;
        }
        setBoard(await post('/speedtest/board/reset'));
        toast('已重置测速榜身份');
    };
    const boardLine = (rec) => html`<${BoardLine} rec=${rec} enabled=${!!board?.upload} uploading=${uploading.has(rec.id)}
        onUpload=${() => upload(rec)} onDelete=${() => removeFromBoard(rec)} />`;

    const nodes = proxies ? nodeOptions(proxies) : [];
    const chosen = node && (node == DIRECT || nodes.includes(node)) ? node : (proxies ? defaultNode(proxies, mode, nodes) : '');

    const start = async () => {
        if (!confirm('测速会在十几秒里尽量占满带宽，局域网里其它设备会变慢，并会照实消耗流量（可能数百 MB）。开始测速？')) {
            return;
        }
        runInfo.current = { multi, protocol: chosen == DIRECT ? null : protocolCode(proxies?.[chosen]?.type) };
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
    const current = currentId && !testing ? history?.find((r) => r.id == currentId) : null;

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
            ${current && boardLine(current)}
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
            <${Card} class="speed__settings">
                <div class="speed-board__head">
                    <div>
                        <div class="speed__label">测速榜</div>
                        <p class="hint">把测完的结果上传到 speed.meowx.net，每次有一个结果页；不上传节点名与地址。NAS 登录了主控账户时，结果归到那个主控的许可证名下。</p>
                    </div>
                    <${Switch} label="上传到测速榜" checked=${!!board?.upload} disabled=${!board} onChange=${setBoardUpload} />
                </div>
                <div class="speed-board__actions">
                    <a class="btn small" href=${BOARD_PAGE} target="_blank" rel="noopener">打开测速榜</a>
                    <${Button} small onClick=${resetIdentity} disabled=${!board}>重置身份<//>
                </div>
            <//>
        </div>
    </div>
    <${History} records=${history} boardLine=${boardLine} onClear=${async () => {
        const uploaded = (history ?? []).some((r) => r.board?.status == 'uploaded');
        if (confirm(uploaded ? '清空全部测速记录？已经上传到测速榜的结果会留在那里，清空后就无法从这里删除了。' : '清空全部测速记录？')) {
            await del('/speedtest/history');
            setHistory([]);
            setCurrentId('');
        }
    }} />
    ${askBoard && html`<${Dialog} title="把测速结果上传到 MeowX 测速榜？" onClose=${() => setAskBoard(false)} actions=${html`
        <${Button} onClick=${() => setBoardUpload(false)}>不上传<//>
        <${Button} kind="primary" onClick=${() => setBoardUpload(true)}>上传<//>`}>
        <div class="speed-board__consent">
            <p>测完的结果可以上传到公开的 MeowX 测速榜（speed.meowx.net）：每次测速有一个结果页，还能看到它超越了多少其他测速（「超越了 x%」）；测速榜按服务商排名。</p>
            <p>上传的内容：测速源、线程数、经节点还是直连、节点协议（如 VLESS）、延迟、速率、用量，以及这台 NAS 的一个随机身份。测速榜按上传请求的来源地址（经测速用的节点发出）认出运营商，不保存完整 IP。</p>
            <p>不上传：节点名、服务器地址、密码、订阅与账户。随时可以在本页的「测速榜」里改。</p>
        </div>
    <//>`}`;
}

// 一条记录在测速榜上的状态（码表下面与测速记录每一行共用）：超越比例 + 结果页 / 复制链接 / 删除，或失败原因 + 重试
function BoardLine({ rec, enabled, uploading, onUpload, onDelete }) {
    const [copied, setCopied] = useState(false);
    const b = rec.board;
    if (uploading) {
        return html`<div class="speed-board"><span class="speed-board__note">正在上传到测速榜…</span></div>`;
    }
    if (!b) {
        if (!enabled || blocker(rec)) {
            return null;
        }
        return html`<div class="speed-board"><div class="speed-board__actions"><${Button} small onClick=${onUpload}>上传到测速榜<//></div></div>`;
    }
    if (b.status == 'uploaded') {
        const p = b.percentile;
        const sub = [p?.scope, p ? `${Number(p.samples).toLocaleString()} 次测速` : '', b.provider, b.attributed ? '已归到主控账户' : '']
            .filter(Boolean).join(' · ');
        const copy = () => copyText(b.url).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
        }, () => prompt('复制链接', b.url));
        return html`<div class="speed-board">
            <div class="speed-board__text">
                <b class="speed-board__pct">${p ? `超越了 ${p.value}% 的测速` : '已上传到测速榜'}</b>
                ${sub && html`<span class="speed-board__note">${sub}</span>`}
            </div>
            <div class="speed-board__actions">
                <a class="btn small" href=${b.url} target="_blank" rel="noopener">结果页</a>
                <${Button} small onClick=${copy}>${copied ? '已复制' : '复制链接'}<//>
                <${Button} small kind="danger" onClick=${onDelete}>删除<//>
            </div>
        </div>`;
    }
    const wait = b.retryAt && b.retryAt > Date.now();
    return html`<div class="speed-board">
        <span class="speed-board__note error-text">没有上传：${stripPeriod(b.error || '未知原因')}</span>
        ${b.retryable !== false && html`<div class="speed-board__actions">
            <${Button} small onClick=${onUpload} disabled=${wait}>${wait ? `${new Date(b.retryAt).toLocaleTimeString()} 之后重试` : '重试'}<//>
        </div>`}
    </div>`;
}

function History({ records, boardLine, onClear }) {
    if (!records || records.length == 0) {
        return null;
    }
    return html`<${Card} title="测速记录" actions=${html`<${Button} small kind="danger" onClick=${onClear}>清空<//>`}>
        <div class="list">${records.map((r) => html`<div class="item speed-hist" key=${r.id || r.at + r.via}>
            <div class="item__main">
                <div class="item__name">${r.via == DIRECT ? '直连' : r.via}<span class="badge is-muted" style="margin-left:8px">${sourceTitle(r.source)}${r.multi ? ' · 多线程' : ''}</span></div>
                <div class=${'item__meta' + (r.error ? ' error-text' : '')}>${r.error
            ? `失败：${stripPeriod(r.error)}`
            : [r.download_mbps != null && `↓ ${fmt(r.download_mbps)} Mbps`, r.upload_mbps != null && `↑ ${fmt(r.upload_mbps)} Mbps`, r.latency_ms != null && `${r.latency_ms} ms`, r.exit]
                .filter(Boolean).join(' · ')}</div>
            </div>
            <span class="muted" style="white-space:nowrap">${relativeTime(r.at)}</span>
            ${boardLine(r)}
        </div>`)}</div>
    <//>`;
}
