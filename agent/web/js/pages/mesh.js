// 喵窝：NAS 用官方 tailscale 加入主控的喵窝（同 OpenWrt 版「喵窝」页），把局域网开放给同一管理员账号的其它设备。
// 开关 /「保存并应用」把设置交给控制服务入网（最多一两分钟，按钮转圈等它）；页面开着时每 5 秒取一次状态。
// 主控那边的信息（访问范围、设备上限、同网络的设备 / 站点、可经喵窝连接的服务器）与「白名单节点经喵窝连线」开关在 MeshPanel：
// 要向主控发请求，只在打开页面、应用完时取一次，不进 5 秒一次的轮询
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

// 访问范围（主控 status 的 access_scope；旧主控没有 = 不显示）
const scopeLabels = { servers: '访问范围：仅服务器', sites: '访问范围：服务器 + 管理员站点', full: '访问范围：完全互通' };

function MeshPanel({ info, onReload }) {
    const [busy, setBusy] = useState(false);
    if (!info) {
        return html`<${Card} title="主控" i=${2}><p class="muted">正在向主控读取喵窝信息…</p><//>`;
    }
    const view = info.view ?? {};
    if (view.success === false) {
        return html`<${Card} title="主控" i=${2}><p class="hint">没能从主控读到喵窝信息：${stripPeriod(view.error || '未知原因')}</p>
            <${Button} small onClick=${onReload}>重试<//><//>`;
    }
    const st = view.status ?? {};
    // 老内核的 status 没整理过、没有 in_list：照同一个规则现算（available，或原因不是 not_admin）
    const inList = st.in_list ?? (!!st.available || (!!st.reason && st.reason != 'not_admin'));
    if (!inList) {
        return st.reason == 'not_admin'
            ? html`<${Card} title="主控" i=${2}><p class="hint">这个账号不在主控的喵窝名单里。</p><//>`
            : null;
    }
    const meta = [scopeLabels[st.access_scope], st.joined_admin && `已加入 ${st.joined_admin} 的喵窝`,
        st.device_limit > 0 ? `设备上限：${st.device_limit} 台` : '设备上限：不限'].filter(Boolean);
    const servers = st.servers ?? [];
    const shared = view.devices?.shared_devices ?? [];
    const toggle = async (on) => {
        setBusy(true);
        try {
            await put('/mesh/whitelist', { on });
            toast(on ? '已打开白名单节点经喵窝连线' : '已关闭白名单节点经喵窝连线');
        } catch (err) {
            toast(`没改成功：${stripPeriod(err.message)}`, 'warn');
        } finally {
            setBusy(false);
            onReload();
        }
    };
    return html`<${Card} title="主控" i=${2} desc=${meta.join(' · ')}>
        <${Field} label="白名单节点经喵窝连线" desc="订阅里的白名单服务器节点改经喵窝连接，不占服务器的 IP 白名单名额。需要开着喵窝。">
            <${Switch} label="白名单节点经喵窝连线" checked=${info.whitelist_direct !== false} disabled=${busy} onChange=${toggle} />
        <//>
        ${info.direct
            ? html`<p class="hint">生效中：拉本主控的订阅时会告诉主控，主控为白名单服务器下发经喵窝连接的节点（节点页带「喵窝」小标）。</p>`
            : info.whitelist_direct !== false && html`<p class="hint">打开喵窝并入网成功后生效。</p>`}
        ${servers.length > 0 && html`
            <div class="section-title">可经喵窝连接的服务器（${servers.length}）</div>
            <div class="list">${servers.map((x) => html`<div class="item">
                <div class="item__main">
                    <div class="item__name">${x.name}</div>
                    <div class="item__meta mono">${x.mesh_ip}</div>
                </div>
            </div>`)}</div>`}
        ${shared.length > 0 && html`
            <div class="section-title">同网络的设备（${shared.length}）</div>
            <div class="list">${shared.map((d) => d.kind == 'site'
                ? html`<div class="item">
                    <div class="item__main">
                        <div class="item__name">站点 · ${d.username || d.name || '?'}</div>
                        <div class="item__meta mono">${(d.routes ?? []).join(', ')}</div>
                    </div>
                    <span class="badge">站点</span>
                </div>`
                : html`<div class="item">
                    <span class=${'peer-dot' + (d.online ? ' is-online' : '')}></span>
                    <div class="item__main">
                        <div class="item__name">${d.name || d.dns_name}</div>
                        <div class="item__meta">${[d.ipv4, (d.routes ?? []).join(', '), d.username].filter(Boolean).join(' · ')}</div>
                    </div>
                </div>`)}</div>`}
        ${view.devices_error && html`<p class="hint">没能读到设备列表：${stripPeriod(view.devices_error)}</p>`}
        ${!view.devices_error && view.devices?.error && html`<p class="hint">${stripPeriod(view.devices.error)}</p>`}
    <//>`;
}

const formOf = (s) => ({
    networks: s?.networks ?? [],
    force_map: !!s?.force_map,
    host_access: !!s?.host_access,
    hostname: s?.hostname ?? '',
});

export function Mesh({ active, go }) {
    const { panel, status } = useStore();
    const [data, setData] = useState(null);       // { mesh: View, lan: [] }
    const [error, setError] = useState('');
    const [form, setForm] = useState(null);
    const [extra, setExtra] = useState('');       // 候选以外的网段（手填，逗号分隔）
    const [applying, setApplying] = useState(false);
    const [leaving, setLeaving] = useState(false);
    const [panelInfo, setPanelInfo] = useState(null);   // GET /mesh/panel；null = 还在取
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

    const loadPanel = async () => {
        try {
            setPanelInfo(await get('/mesh/panel'));
        } catch (err) {
            setPanelInfo({ view: { success: false, error: err.message } });
        }
    };
    // 主控那边的信息：打开页面时取一次（不进轮询）
    useEffect(() => {
        if (active && (data?.mesh?.panel_logged_in ?? panel?.logged_in)) {
            loadPanel();
        }
    }, [active, data?.mesh?.panel_logged_in ?? panel?.logged_in]);

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
            loadPanel();
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
            loadPanel();
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
                <input class="input" placeholder=${v.hostname || (status?.platform && status.platform != 'fnos' ? 'meowx-nas' : 'fnos')} spellcheck="false" autocomplete="off"
                    value=${form.hostname} onInput=${(e) => setF({ hostname: e.currentTarget.value })} />
            <//>
            <div class="card__actions" style="justify-content:flex-start;margin-top:8px">
                <${Button} kind="primary" disabled=${applying} onClick=${() => save(!!s.enabled)}>
                    ${applying ? '正在应用…' : s.enabled ? '保存并应用' : '保存'}<//>
                ${touched.current && !applying && html`<${Button} onClick=${reset}>还原<//>`}
            </div>
            ${applying && html`<p class="hint">入网或重新申报网段最多要一两分钟，请稍等。</p>`}
        <//>`}

        ${loggedIn && html`<${MeshPanel} info=${panelInfo} onReload=${loadPanel} />`}

        ${loggedIn && s.enabled && v.backend == 'Running' && html`<${Card} title=${`其它设备（${peers.length}）`} i=${2}>
            ${peers.length == 0
                ? html`<p class="hint">还没有其它设备：在用同一管理员账号登录的 MeowX 里打开「喵窝」即可。</p>`
                : html`<div class="list">${peers.map((p) => html`<div class="item">
                    <span class=${'peer-dot' + (p.online ? ' is-online' : '')}></span>
                    <div class="item__main">
                        <div class="item__name">${p.name || p.dns_name}</div>
                        <div class="item__meta">${[p.ip, (p.routes ?? []).join(', '), p.os,
                        p.online ? (p.direct ? '直连' : !p.active ? '空闲' : p.relay ? `经中继 ${p.relay}` : '') : '离线'].filter(Boolean).join(' · ')}</div>
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
