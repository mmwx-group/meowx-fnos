// 节点：代理组（经内核 API），手动选择组可点选成员；单个节点与整组测延迟
import { html, useState, useEffect } from '../lib.js';
import { get, put, seg } from '../api.js';
import { useStore, toast } from '../store.js';
import { Card, Button } from '../ui.js';
import { delayClass, stripPeriod } from '../util.js';

const DEFAULT_TEST_URL = 'https://www.gstatic.com/generate_204';
const TIMEOUT = 5000;

const typeNames = {
    Selector: '手动选择', URLTest: '自动选择', Fallback: '故障转移', LoadBalance: '负载均衡', Relay: '链式代理',
    Direct: '直连', Reject: '拒绝', RejectDrop: '拒绝', Pass: '跳过', Compatible: '兼容',
};

// 最近一次延迟：组写了测速地址的用该地址的记录，否则用通用记录。没测过 = null，0 = 超时 / 失败
function lastDelay(proxy, testUrl) {
    const hist = (testUrl && proxy?.extra?.[testUrl]?.history?.length ? proxy.extra[testUrl].history : proxy?.history) ?? [];
    return hist.length ? hist[hist.length - 1].delay : null;
}

function Group({ group, proxies, open, onToggle, onReload, overrides, setOverride }) {
    const [testing, setTesting] = useState(false);
    const [busyNode, setBusyNode] = useState('');
    const testUrl = group.testUrl || DEFAULT_TEST_URL;
    const selectable = group.type == 'Selector';
    const delayOf = (name) => (name in overrides ? overrides[name] : lastDelay(proxies[name], group.testUrl));

    const pick = async (name) => {
        if (!selectable || name == group.now) {
            return;
        }
        try {
            await put(`/core/proxies/${seg(group.name)}`, { name });
        } catch (err) {
            toast(`切换失败：${stripPeriod(err.message)}`, 'warn');
        }
        onReload();
    };
    const testOne = async (name) => {
        setBusyNode(name);
        try {
            const r = await get(`/core/proxies/${seg(name)}/delay?url=${encodeURIComponent(testUrl)}&timeout=${TIMEOUT}`);
            setOverride(name, r?.delay ?? 0);
        } catch {
            setOverride(name, 0);
        } finally {
            setBusyNode('');
        }
    };
    const testAll = async () => {
        setTesting(true);
        try {
            const r = await get(`/core/group/${seg(group.name)}/delay?url=${encodeURIComponent(testUrl)}&timeout=${TIMEOUT}`);
            // 回包只有测通的节点：其余记成超时
            for (const name of group.all) {
                setOverride(name, r?.[name] ?? 0);
            }
        } catch (err) {
            toast(`测速失败：${stripPeriod(err.message)}`, 'warn');
        } finally {
            setTesting(false);
            onReload();
        }
    };

    const nowDelay = group.now ? delayOf(group.now) : null;
    return html`<section class=${'card group' + (open ? ' is-open' : '')}>
        <div class="card__head" style="margin:0">
            <button type="button" class="group__head" aria-expanded=${open} onClick=${onToggle}>
                <span class="group__chev"></span>
                <span style="min-width:0;flex:1">
                    <span class="group__name">${group.name}</span>
                    <span class="badge is-muted" style="margin-left:8px">${typeNames[group.type] || group.type}</span>
                    <div class="group__now">${group.now ? `当前：${group.now}` : ''}${nowDelay > 0 ? ` · ${nowDelay} ms` : ''} · ${group.all.length} 个成员</div>
                </span>
            </button>
            <div class="card__actions"><${Button} small kind="soft" onClick=${testAll} disabled=${testing}>${testing ? '测速中…' : '测速'}<//></div>
        </div>
        ${open && html`<div class="nodes">${group.all.map((name, i) => {
        const p = proxies[name];
        const d = delayOf(name);
        const on = name == group.now;
        return html`<div key=${name} class=${'node' + (on ? ' is-on' : '') + (selectable ? ' can-pick' : '')} style=${`--i:${Math.min(i, 12)}`}
                role=${selectable ? 'button' : null} tabindex=${selectable ? 0 : null} aria-pressed=${selectable ? on : null}
                onClick=${() => pick(name)} onKeyDown=${(e) => (e.key == 'Enter' || e.key == ' ') && (e.preventDefault(), pick(name))}>
                <div class="node__name" title=${name}>${name}</div>
                <div class="node__foot">
                    <span class="node__type">${typeNames[p?.type] || p?.type || ''}${p?.udp ? ' · UDP' : ''}</span>
                    <button type="button" class=${'delay ' + delayClass(d) + (busyNode == name || testing ? ' is-busy' : '')} title="测延迟"
                        onClick=${(e) => { e.stopPropagation(); testOne(name); }}>${d == null ? '测速' : d > 0 ? `${d} ms` : '超时'}</button>
                </div>
            </div>`;
    })}</div>`}
    </section>`;
}

export function Nodes({ active, go }) {
    const { status } = useStore();
    const running = !!status?.core?.running;
    const [data, setData] = useState(null);
    const [mode, setMode] = useState('');
    const [error, setError] = useState('');
    const [open, setOpen] = useState({});
    const [overrides, setOverrides] = useState({});

    const load = async () => {
        try {
            const [r, c] = await Promise.all([get('/core/proxies'), get('/core/configs').catch(() => null)]);
            setData(r?.proxies ?? {});
            setOverrides({});   // 重新取到的记录里已经有刚测的结果
            setMode(String(c?.mode || '').toLowerCase());
            setError('');
        } catch (err) {
            setError(err.message);
        }
    };
    useEffect(() => {
        if (active && running) {
            load();
        }
    }, [active, running, status?.core?.started_at]);

    if (!running) {
        return html`<${Card} title="节点">
            <div class="empty">内核没在运行，启动后才能查看与切换节点。<br /><br />
                <${Button} kind="primary" onClick=${() => go('overview')}>去概览启动<//></div>
        <//>`;
    }
    if (error && !data) {
        return html`<${Card} title="节点"><p class="error-text">${stripPeriod(error)}</p><${Button} onClick=${load}>重试<//><//>`;
    }
    if (!data) {
        return html`<${Card} title="节点"><p class="muted">正在载入…</p><//>`;
    }

    // 组的顺序照 GLOBAL 的成员顺序（即配置里的顺序）；隐藏的组不列；GLOBAL 只在全局模式下列（放最前）
    const isGroup = (p) => p && Array.isArray(p.all);
    const order = data.GLOBAL?.all ?? Object.keys(data);
    const groups = order.map((n) => data[n]).filter((p) => isGroup(p) && !p.hidden && p.name != 'GLOBAL');
    for (const p of Object.values(data)) {
        if (isGroup(p) && !p.hidden && p.name != 'GLOBAL' && !groups.includes(p)) {
            groups.push(p);
        }
    }
    if (mode == 'global' && data.GLOBAL) {
        groups.unshift(data.GLOBAL);
    }
    const setOverride = (name, d) => setOverrides((o) => ({ ...o, [name]: d }));
    const reload = () => {
        load();
    };

    return html`<div>
        <div class="toolbar" style="margin-bottom:12px">
            <span class="muted">${groups.length} 个代理组${mode ? ` · 当前${{ rule: '规则', global: '全局', direct: '直连' }[mode] || mode}模式` : ''}${mode == 'direct' ? '（直连模式下不走任何节点）' : ''}</span>
            <span style="flex:1"></span>
            <${Button} small onClick=${() => setOpen(Object.fromEntries(groups.map((g) => [g.name, true])))}>全部展开<//>
            <${Button} small onClick=${() => setOpen({})}>全部收起<//>
            <${Button} small onClick=${load}>刷新<//>
        </div>
        ${groups.length == 0 && html`<${Card}><div class="empty">配置里没有代理组。</div><//>`}
        ${groups.map((g) => html`<${Group} key=${g.name} group=${g} proxies=${data} open=${!!open[g.name]}
            onToggle=${() => setOpen((o) => ({ ...o, [g.name]: !o[g.name] }))} onReload=${reload}
            overrides=${overrides} setOverride=${setOverride} />`)}
    </div>`;
}
