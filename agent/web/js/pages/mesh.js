// 喵窝：NAS 用官方 tailscale 加入主控的喵窝（同 OpenWrt 版「喵窝」页），把局域网开放给同一管理员账号的其它设备。
// 开关 /「保存并应用」把设置交给控制服务入网（最多一两分钟，按钮转圈等它）；页面开着时每 5 秒取一次状态
import { html, useState, useEffect, useRef } from '../lib.js';
import { get, put, post } from '../api.js';
import { useStore, toast, refreshPanel } from '../store.js';
import { Card, Field, Switch, Button, Dialog } from '../ui.js';
import { same, stripPeriod } from '../util.js';

function stateLabel(v, applying) {
    if (applying) {
        return ['正在应用…', ''];
    }
    if (!v.settings?.enabled) {
        return ['已关闭', ''];
    }
    if (v.backend == 'Running' && v.self?.online !== false) {
        return ['已连接', 'is-running'];
    }
    if (v.backend == 'NeedsLogin' || v.backend == 'NeedsMachineAuth') {
        return ['未入网', ''];
    }
    if (v.backend == 'Starting') {
        return ['正在连接…', ''];
    }
    return ['未连接', v.state?.error ? 'is-error' : ''];
}

const formOf = (s) => ({
    networks: s?.networks ?? [],
    force_map: !!s?.force_map,
    host_access: !!s?.host_access,
    hostname: s?.hostname ?? '',
});

export function Mesh({ active, go }) {
    const { panel } = useStore();
    const [data, setData] = useState(null);       // { mesh: View, lan: [] }
    const [error, setError] = useState('');
    const [form, setForm] = useState(null);
    const [extra, setExtra] = useState('');       // 候选以外的网段（手填，逗号分隔）
    const [applying, setApplying] = useState(false);
    const [leaving, setLeaving] = useState(false);
    // 表单是否被改过：没改过才跟着取回的状态刷新（填了一半的网段不丢）
    const touched = useRef(false);

    const load = async () => {
        try {
            const r = await get('/mesh');
            setData(r);
            setError('');
            if (!touched.current) {
                const next = formOf(r.mesh?.settings);
                setForm(next);
                setExtra(next.networks.filter((c) => !(r.lan ?? []).includes(c)).join(', '));
            }
        } catch (err) {
            setError(err.message);
        }
    };

    useEffect(() => {
        if (!active) {
            return;
        }
        load();
        refreshPanel();
        const t = setInterval(() => !document.hidden && !applying && load(), 5000);
        return () => clearInterval(t);
    }, [active, applying]);

    if (!data || !form) {
        return html`<${Card} title="喵窝">${error
            ? html`<p class="error-text">${stripPeriod(error)}</p><${Button} onClick=${load}>重试<//>`
            : html`<p class="muted">正在载入…</p>`}<//>`;
    }

    const v = data.mesh ?? {};
    const s = v.settings ?? {};
    const lan = data.lan ?? [];
    const loggedIn = !!(v.panel_logged_in ?? panel?.logged_in);
    const [label, cls] = stateLabel(v, applying);
    const peers = v.peers ?? [];
    const sites = v.state?.sites ?? [];
    const self = v.self;

    const setF = (patch) => {
        touched.current = true;
        setForm({ ...form, ...patch });
    };
    const toggleNet = (cidr, on) => setF({ networks: on ? [...form.networks, cidr] : form.networks.filter((c) => c != cidr) });
    const collect = (enabled) => ({
        enabled,
        networks: [...new Set([
            ...form.networks.filter((c) => lan.includes(c)),
            ...extra.split(/[\s,，]+/).map((x) => x.trim()).filter(Boolean),
        ])],
        force_map: form.force_map,
        host_access: form.host_access,
        hostname: form.hostname.trim(),
    });
    const reset = () => {
        touched.current = false;
        load();
    };
    // 入网 / 退出要一两分钟：转圈等它跑完
    const save = async (enabled) => {
        setApplying(true);
        try {
            const res = await put('/mesh', collect(enabled));
            if (res?.error) {
                toast(`喵窝没应用成功：${stripPeriod(res.error)}`, 'warn');
            } else {
                toast(enabled ? '喵窝设置已应用' : '已关闭喵窝');
            }
        } catch (err) {
            toast(`喵窝没应用成功：${stripPeriod(err.message)}`, 'warn');
        } finally {
            touched.current = false;   // 应用完按新状态重新填表
            setApplying(false);
            await load();
        }
    };
    const leave = async () => {
        try {
            const res = await post('/mesh/leave');
            toast(res?.warning ? `已退出喵窝，但有一步没成功：${stripPeriod(res.warning)}` : '已退出喵窝', res?.warning ? 'warn' : 'ok');
        } finally {
            setLeaving(false);
            touched.current = false;
            await load();
        }
    };

    const toggle = html`<${Switch} label="喵窝" checked=${s.enabled} disabled=${!loggedIn || applying} onChange=${(on) => save(on)} />`;

    return html`<div>
        <${Card} title="喵窝" i=${0} actions=${toggle}
            desc="加入主控的喵窝（Tailscale 协议），把 NAS 所在的局域网开放给同一管理员账号的其它设备，例如在外面用 Mac 访问家里的 NAS。只对管理员账号开放。">
            ${loggedIn
            ? html`<span class=${'status ' + cls}><span class="status__dot"></span><span>${label}</span></span>`
            : html`<p class="hint">请先在「订阅」页登录主控账户（主控要开着喵窝，账号要是管理员）。</p>
                <div style="margin-top:10px"><${Button} kind="primary" onClick=${() => go('profiles')}>去登录<//></div>`}
            ${self?.ip && s.enabled && html`<div class="list" style="margin-top:12px"><div class="item">
                <div class="item__main">
                    <div class="item__mono">${self.ip}</div>
                    <div class="item__meta mono">${[self.dns_name || v.state?.magic_dns, self.relay && `中继 ${self.relay}`].filter(Boolean).join(' · ')}</div>
                </div>
                <span class="badge">本机</span>
            </div></div>`}
            ${v.state?.error && !applying && html`<p class="error-text">${stripPeriod(v.state.error)}</p>`}
            ${s.enabled && sites.length > 0 && html`
                <div class="section-title">开放的网段（${sites.length}）</div>
                <div class="list">${sites.map((x) => html`<div class="item">
                    <div class="item__main">
                        <div class="item__mono">${x.mapped ? x.advertised : x.original}</div>
                        <div class="item__meta">${x.mapped ? `由 ${x.original} 映射而来（这个网段在喵窝里已有人用，主机号不变）` : '原样开放'}</div>
                    </div>
                    ${x.mapped && html`<span class="badge">已映射</span>`}
                </div>`)}</div>`}
        <//>

        ${loggedIn && html`<${Card} title="设置" i=${1}>
            <div class="section-title">要开放的局域网网段</div>
            ${lan.length > 0 && html`<div class="rows">${lan.map((cidr) => html`<label class="rows__row" style="cursor:pointer">
                <input type="checkbox" class="switch" checked=${form.networks.includes(cidr)} onChange=${(e) => toggleNet(cidr, e.currentTarget.checked)} />
                <span class="grow mono" style="font-weight:700">${cidr}</span>
            </label>`)}</div>`}
            <input class="input mono" style="margin:4px 0 6px" placeholder="其它网段，用逗号分隔，例如 192.168.2.0/24" spellcheck="false"
                value=${extra} onInput=${(e) => { touched.current = true; setExtra(e.currentTarget.value); }} />
            <${Field} label="总是映射" desc="没有冲突也用主控映射池里的网段">
                <${Switch} label="总是映射" checked=${form.force_map} onChange=${(on) => setF({ force_map: on })} />
            <//>
            <${Field} label="喵窝里的设备能访问 NAS 本身" desc="NAS 的管理页面、SMB、SSH 等">
                <${Switch} label="喵窝里的设备能访问 NAS 本身" checked=${form.host_access} onChange=${(on) => setF({ host_access: on })} />
            <//>
            <${Field} label="设备名" desc="在喵窝里显示的名字" wide>
                <input class="input" placeholder=${v.hostname || 'fnos'} spellcheck="false" autocomplete="off"
                    value=${form.hostname} onInput=${(e) => setF({ hostname: e.currentTarget.value })} />
            <//>
            <div class="card__actions" style="justify-content:flex-start;margin-top:8px">
                <${Button} kind="primary" disabled=${applying} onClick=${() => save(!!s.enabled)}>
                    ${applying ? '正在应用…' : s.enabled ? '保存并应用' : '保存'}<//>
                ${touched.current && !applying && html`<${Button} onClick=${reset}>还原<//>`}
            </div>
            ${applying && html`<p class="hint">入网或重新申报网段最多要一两分钟，请稍等。</p>`}
        <//>`}

        ${loggedIn && s.enabled && v.backend == 'Running' && html`<${Card} title=${`其它设备（${peers.length}）`} i=${2}>
            ${peers.length == 0
                ? html`<p class="hint">还没有其它设备：在用同一管理员账号登录的 MeowX 里打开「喵窝」即可。</p>`
                : html`<div class="list">${peers.map((p) => html`<div class="item">
                    <span class=${'peer-dot' + (p.online ? ' is-online' : '')}></span>
                    <div class="item__main">
                        <div class="item__name">${p.name || p.dns_name}</div>
                        <div class="item__meta">${[p.ip, (p.routes ?? []).join(', '), p.os,
                        p.online ? (p.direct ? '直连' : p.relay ? `经中继 ${p.relay}` : '') : '离线'].filter(Boolean).join(' · ')}</div>
                    </div>
                </div>`)}</div>`}
        <//>`}

        ${loggedIn && html`<${Card} i=${3}>
            <div class="toolbar">
                <span class="hint" style="flex:1;margin:0;min-width:220px">其它设备经喵窝访问开放的网段；网段已被别的设备占用时会映射到主控映射池里的一段。直连要用 UDP，两端都在严格 NAT 后面时经主控选定的中继转发（端到端加密）。</span>
                ${(s.enabled || self?.ip) && html`<${Button} kind="danger" disabled=${applying} onClick=${() => setLeaving(true)}>退出喵窝<//>`}
            </div>
        <//>`}

        ${leaving && html`<${Dialog} title="退出喵窝？" onClose=${() => setLeaving(false)} actions=${html`
            <${Button} onClick=${() => setLeaving(false)}>取消<//>
            <${Button} kind="danger" onClick=${leave}>退出<//>`}>
            <p class="hint">这台 NAS 会从主控的喵窝里删掉，开放的网段一并撤销。重新打开开关即可再次入网。</p>
        <//>`}
    </div>`;
}
