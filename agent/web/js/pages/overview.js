// 概览：主卡（运行状态、开关）、当前订阅、出站模式、代理端口
import { html, useState, useEffect } from '../lib.js';
import { get, post, patch, seg } from '../api.js';
import { useStore, refreshStatus, refreshProfiles, saveSettings, toast } from '../store.js';
import { Card, Button, Segmented, Usage, Logo, describeStatus } from '../ui.js';
import { formatDuration, stripPeriod, profileName, copyText } from '../util.js';
import { QuickAdd, AccountSubscriptions, refreshProfile } from './profiles.js';

function Hero({ status }) {
    const running = !!status.core?.running;
    const s = describeStatus(status);
    const meta = [];
    if (running && status.core.started_at) {
        meta.push(`已运行 ${formatDuration(Date.now() / 1000 - status.core.started_at)}`);
    }
    if (status.core?.restarts > 0) {
        meta.push(`内核意外退出过 ${status.core.restarts} 次`);
    }
    const service = (action) => async () => {
        try {
            await post('/service', { action });
            toast({ start: '已启动', stop: '已停止', restart: '已重启' }[action]);
        } catch (err) {
            toast(`${{ start: '启动', stop: '停止', restart: '重启' }[action]}失败：${stripPeriod(err.message)}`, 'warn');
        }
        await refreshStatus();
    };
    return html`<div class=${'hero enter' + (running ? ' is-running' : '')}>
        <${Logo} />
        <div class="hero__main">
            <div class="hero__title">MeowX</div>
            <span class=${'status ' + s.cls}><span class="status__dot"></span><span>${s.text}</span></span>
            ${meta.length > 0 && html`<div class="hero__meta">${meta.join(' · ')}</div>`}
            ${status.error && html`<div class="hero__error">上次启动或应用设置没成功：${stripPeriod(status.error)}</div>`}
            ${status.core?.error && status.core.error != status.error && html`<div class="hero__error">内核：${stripPeriod(status.core.error)}</div>`}
        </div>
        <div class="hero__actions">
            ${status.enabled
            ? html`<${Button} onClick=${service('restart')}>重启<//><${Button} kind="primary" onClick=${service('stop')}>停止<//>`
            : html`<${Button} kind="primary" onClick=${service('start')}>启动<//>`}
        </div>
    </div>`;
}

// 当前订阅（同客户端首页订阅条）：名字 + 用量 + 到期，「更新于 …」点了直接刷新；「切换订阅」弹出菜单
function SubscriptionCard({ go }) {
    const { profiles: data, panel } = useStore();
    const [open, setOpen] = useState(false);
    const [busy, setBusy] = useState(false);
    useEffect(() => {
        if (!open) {
            return;
        }
        const close = () => setOpen(false);
        document.addEventListener('click', close);
        return () => document.removeEventListener('click', close);
    }, [open]);

    if (!data) {
        return html`<${Card} title="订阅" i=${1}><div class="muted">正在载入…</div><//>`;
    }
    const list = data.profiles;
    const current = list.find((p) => p.id == data.active);

    const use = async (id) => {
        setOpen(false);
        if (id == data.active) {
            return;
        }
        setBusy(true);
        try {
            await post(`/profiles/${seg(id)}/use`);
            toast('已切换订阅');
        } catch (err) {
            toast(`切换失败：${stripPeriod(err.message)}，仍在用原来的订阅`, 'warn');
        } finally {
            setBusy(false);
            refreshProfiles();
            refreshStatus();
        }
    };

    if (list.length == 0) {
        return html`<${Card} title="订阅" i=${1}>
            <div class="muted">还没有订阅。粘贴订阅链接添加，或在「订阅」页上传配置文件。</div>
            ${panel?.logged_in && html`<${AccountSubscriptions} />`}
            <${QuickAdd} use />
        <//>`;
    }

    const switcher = html`<div class="menu-wrap">
        <button type="button" class=${'switcher' + (busy ? ' is-busy' : '')} aria-haspopup="menu" disabled=${busy}
            onClick=${(e) => { e.stopPropagation(); setOpen(!open); }}>${busy ? '正在切换…' : current ? '切换订阅' : '选择订阅'}</button>
        ${open && html`<div class="menu" role="menu" onClick=${(e) => e.stopPropagation()}>
            ${list.map((p) => html`<button type="button" role="menuitemradio" aria-checked=${p.id == data.active}
                class=${'menu__item' + (p.id == data.active ? ' is-current' : '')} onClick=${() => use(p.id)}>${profileName(p)}</button>`)}
            <button type="button" class="menu__item is-link" onClick=${() => { setOpen(false); go('profiles'); }}>管理订阅</button>
        </div>`}
    </div>`;

    return html`<${Card} title="订阅" i=${1} actions=${switcher}>
        ${current
            ? html`<div class="sub__name">${profileName(current)}</div>
                ${current.error && html`<div class="error-text">上次更新失败：${stripPeriod(current.error)}（用的是上次下载成功的那份）</div>`}
                <${Usage} profile=${current} onRefresh=${current.url ? () => refreshProfile(current.id) : null} />`
            : html`<div class="muted">${data.active ? '在用的订阅已经不在了，请重新选择。' : '还没有选订阅。'}</div>`}
    <//>`;
}

// 出站模式：内核在跑时经内核 API 即时生效，同时写回设置（只存不重启）
function ModeCard({ status, settings, onSaved }) {
    const running = !!status.core?.running;
    const [coreMode, setCoreMode] = useState(null);
    const [pending, setPending] = useState(null);
    useEffect(() => {
        if (!running) {
            setCoreMode(null);
            return;
        }
        get('/core/configs').then((c) => setCoreMode(String(c?.mode || '').toLowerCase()), () => { });
    }, [running, status.core?.started_at]);

    const value = pending ?? coreMode ?? (settings?.mixin?.mode || 'rule');
    const change = async (mode) => {
        setPending(mode);
        try {
            if (running) {
                await patch('/core/configs', { mode });
                setCoreMode(mode);
            }
            await saveSettings((s) => { s.mixin.mode = mode; }, false);
            onSaved();
        } catch (err) {
            toast(`切换出站模式失败：${stripPeriod(err.message)}`, 'warn');
        } finally {
            setPending(null);
        }
    };
    return html`<${Card} title="出站模式" desc="规则：按订阅的分流规则走；全局：全部走「节点」页 GLOBAL 组选的节点；直连：全部不走代理。" i=${2}>
        <${Segmented} full value=${value} onChange=${change} options=${[['rule', '规则'], ['global', '全局'], ['direct', '直连']]} />
    <//>`;
}

// 一个账号：用户名 + 密码（默认遮住）+ 复制
function Account({ user }) {
    const [show, setShow] = useState(false);
    const copy = async (text) => {
        const ok = await copyText(text);
        toast(ok ? '已复制' : '复制失败，请手动选中复制', ok ? 'ok' : 'warn');
    };
    return html`<div class="kv">
        <span class="kv__k">用户名</span><span class="kv__v">${user.username}</span>
        <${Button} small onClick=${() => copy(user.username)}>复制<//>
        <span class="kv__k">密码</span><span class="kv__v">${user.password ? (show ? user.password : '••••••••') : html`<span class="muted">（空）</span>`}</span>
        <span style="display:flex;gap:6px">
            ${user.password && html`<${Button} small onClick=${() => setShow(!show)}>${show ? '隐藏' : '显示'}<//>`}
            ${user.password && html`<${Button} small onClick=${() => copy(user.password)}>复制<//>`}
        </span>
    </div>`;
}

function PortCard({ status, settings, go }) {
    if (!settings) {
        return null;
    }
    const m = settings.mixin;
    const host = location.hostname || 'NAS 的 IP';
    const users = (m.users ?? []).filter((u) => u.enabled && u.username);
    const auth = m.authentication && users.length > 0;
    const extra = [];
    if (m.http_port) {
        extra.push(`HTTP ${m.http_port}`);
    }
    if (m.socks_port) {
        extra.push(`SOCKS5 ${m.socks_port}`);
    }
    return html`<${Card} title="代理端口" desc="局域网里的电脑、手机、电视盒子把 HTTP / SOCKS5 代理填成下面的地址即可（同一个端口两种都认）。" i=${3}>
        ${m.mixed_port
            ? html`<div class="addr">
                <span class="addr__value">${host}:${m.mixed_port}</span>
                <${Button} small onClick=${async () => {
                    const ok = await copyText(`${host}:${m.mixed_port}`);
                    toast(ok ? '已复制' : '复制失败，请手动选中复制', ok ? 'ok' : 'warn');
                }}>复制<//>
            </div>`
            : html`<div class="muted">混合端口没开，在「混入设置 › 入站」里填一个端口。</div>`}
        ${extra.length > 0 && html`<p class="hint">另外开着：${extra.join('、')}</p>`}
        ${!status.core?.running && html`<p class="hint">内核没在运行时端口不可用。</p>`}
        ${!m.allow_lan && html`<p class="error-text">「允许局域网访问」关着：只有 NAS 本机能用这个端口。</p>`}
        ${auth
            ? html`<p class="hint">${m.skip_auth_lan
                ? '局域网与 NAS 本机连过来不要密码；从外网（经路由器端口转发）连进来要用下面的账号密码：'
                : '连接时要用下面的账号密码：'}</p>
                ${users.map((u) => html`<${Account} user=${u} />`)}`
            : html`<p class="hint">代理端口没设密码。只在可信的局域网里用；要开放到外网，先在「混入设置 › 入站」里打开身份验证。</p>`}
        <p class="hint"><a href="#mixin" onClick=${(e) => { e.preventDefault(); go('mixin'); }}>修改端口与账号</a></p>
    <//>`;
}

export function Overview({ active, go }) {
    const { status } = useStore();
    const [settings, setSettings] = useState(null);
    const loadSettings = () => get('/settings').then(setSettings, () => { });
    useEffect(() => {
        if (active) {
            loadSettings();
            refreshProfiles().catch(() => { });
        }
    }, [active]);
    return html`<div>
        <${Hero} status=${status} />
        <div class="grid-2">
            <div>
                <${SubscriptionCard} go=${go} />
                <${ModeCard} status=${status} settings=${settings} onSaved=${loadSettings} />
            </div>
            <div>
                <${PortCard} status=${status} settings=${settings} go=${go} />
            </div>
        </div>
    </div>`;
}
