// 连接：内核当前的连接（2 秒刷新），可关闭单条 / 全部
import { html, useState, useEffect, useRef } from '../lib.js';
import { get, del, seg } from '../api.js';
import { useStore, toast } from '../store.js';
import { Card, Button } from '../ui.js';
import { formatBytes, formatSpeed, formatDuration, stripPeriod } from '../util.js';

function target(m) {
    const host = m.host || m.sniffHost || m.destinationIP || '';
    const port = m.destinationPort ? `:${m.destinationPort}` : '';
    return host.includes(':') && !m.host ? `[${host}]${port}` : host + port;
}

export function Connections({ active, go }) {
    const { status } = useStore();
    const running = !!status?.core?.running;
    const [data, setData] = useState(null);
    const [error, setError] = useState('');
    const [query, setQuery] = useState('');
    const prev = useRef({ at: 0, bytes: {} });
    const [speed, setSpeed] = useState({});

    const load = async () => {
        try {
            const r = await get('/core/connections');
            // 按两次之间的差算每条的速度
            const now = Date.now();
            const dt = (now - prev.current.at) / 1000;
            const bytes = {}, sp = {};
            for (const c of r?.connections ?? []) {
                bytes[c.id] = [c.download, c.upload];
                const old = prev.current.bytes[c.id];
                if (old && dt > 0 && dt < 10) {
                    sp[c.id] = [Math.max(0, (c.download - old[0]) / dt), Math.max(0, (c.upload - old[1]) / dt)];
                }
            }
            prev.current = { at: now, bytes };
            setSpeed(sp);
            setData(r);
            setError('');
        } catch (err) {
            setError(err.message);
        }
    };
    useEffect(() => {
        if (!active || !running) {
            return;
        }
        load();
        const t = setInterval(() => !document.hidden && load(), 2000);
        return () => clearInterval(t);
    }, [active, running]);

    if (!running) {
        return html`<${Card} title="连接">
            <div class="empty">内核没在运行。<br /><br /><${Button} kind="primary" onClick=${() => go('overview')}>去概览启动<//></div>
        <//>`;
    }

    const close = async (id) => {
        await del(`/core/connections/${seg(id)}`);
        load();
    };
    const closeAll = async () => {
        if (!confirm('关闭全部连接？正在进行的下载、通话等会断开重连。')) {
            return;
        }
        await del('/core/connections');
        toast('已关闭全部连接');
        load();
    };

    const all = (data?.connections ?? []).slice().sort((a, b) => Date.parse(b.start) - Date.parse(a.start));
    const q = query.trim().toLowerCase();
    const list = q ? all.filter((c) => {
        const m = c.metadata || {};
        return [target(m), m.sourceIP, m.process, c.rule, c.rulePayload, ...(c.chains || [])].some((s) => String(s || '').toLowerCase().includes(q));
    }) : all;
    const now = Date.now();

    return html`<div>
        <${Card} i=${0}>
            <div class="toolbar">
                <input class="input" type="search" placeholder="搜索主机、来源 IP、规则、节点" value=${query} onInput=${(e) => setQuery(e.currentTarget.value)} />
                <${Button} kind="danger" onClick=${closeAll} disabled=${all.length == 0}>全部关闭<//>
            </div>
            <div class="stats" style="margin-top:10px">
                <span>连接 <b>${all.length}</b>${q ? ` · 匹配 ${list.length}` : ''}</span>
                <span>累计下载 <b>${formatBytes(data?.downloadTotal ?? 0)}</b></span>
                <span>累计上传 <b>${formatBytes(data?.uploadTotal ?? 0)}</b></span>
            </div>
            ${error && html`<p class="error-text">${stripPeriod(error)}</p>`}
        <//>
        ${!data
            ? html`<p class="muted">正在载入…</p>`
            : list.length == 0
                ? html`<${Card}><div class="empty">${q ? '没有匹配的连接' : '现在没有连接'}</div><//>`
                : html`<div class="list">${list.map((c) => {
                    const m = c.metadata || {};
                    const chain = (c.chains || []).slice().reverse().join(' → ');
                    const rule = c.rule ? (c.rulePayload ? `${c.rule}(${c.rulePayload})` : c.rule) : '';
                    const sp = speed[c.id];
                    const meta = [chain, rule, m.sourceIP && `来自 ${m.sourceIP}`, m.process, formatDuration((now - Date.parse(c.start)) / 1000)].filter(Boolean);
                    return html`<div class="conn" key=${c.id}>
                        <div class="conn__main">
                            <div class="conn__host"><span title=${target(m)}>${target(m) || '（未知）'}</span>
                                <span class="badge is-muted">${String(m.network || '').toUpperCase()}</span>
                                ${m.type && html`<span class="badge is-muted">${m.type}</span>`}
                            </div>
                            <div class="conn__meta">${meta.join(' · ')}</div>
                        </div>
                        <div class="conn__traffic">
                            <div>↓ <b>${formatBytes(c.download)}</b>${sp && sp[0] > 0 ? ` · ${formatSpeed(sp[0])}` : ''}</div>
                            <div>↑ <b>${formatBytes(c.upload)}</b>${sp && sp[1] > 0 ? ` · ${formatSpeed(sp[1])}` : ''}</div>
                        </div>
                        <button type="button" class="close-x" title="关闭这条连接" onClick=${() => close(c.id).catch((err) => toast(err.message, 'warn'))}>×</button>
                    </div>`;
                })}</div>`}
    </div>`;
}
