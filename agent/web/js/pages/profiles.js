// 订阅：本机订阅列表（添加、上传、编辑、刷新、切换、删除）、主控账户（登录、账户下的订阅、退出）。po0 / GGY 加白在「辅助功能」页
import { html, useState, useEffect, useRef } from '../lib.js';
import { get, post, patch, del, request, seg } from '../api.js';
import { useStore, refreshProfiles, refreshPanel, refreshStatus, toast } from '../store.js';
import { Card, Button, Field, TextInput, Dialog, Usage } from '../ui.js';
import { profileName, stripPeriod, isDownloaded, formatBytes } from '../util.js';

// 刷新一份订阅并按结果提示：下载 / 校验没过（旧的保留）、下载成功但套用失败（运行中的保持不动）、更新成功，分开说
export async function refreshProfile(id) {
    try {
        const res = await post(`/profiles/${seg(id)}/update`);
        if (res?.error) {
            if (!res.profile?.id) {
                toast(stripPeriod(res.error), 'warn');
            } else if (res.profile.error) {
                toast(`订阅更新失败：${stripPeriod(res.error)}。用的仍是上次下载成功的那份。`, 'warn');
            } else {
                toast(`订阅已更新，但套用失败：${stripPeriod(res.error)}。运行中的配置保持不变。`, 'warn');
            }
        } else {
            toast('订阅已更新');
        }
    } finally {
        await refreshProfiles();
        refreshStatus();
    }
}

// 「粘贴订阅链接 → 添加」：名字下载后自动取。use = 添加后直接用它（还没有在用的订阅时控制服务也会自动切过去）
export function QuickAdd({ use }) {
    const [url, setUrl] = useState('');
    const [invalid, setInvalid] = useState(false);
    const add = async () => {
        const u = url.trim();
        if (!/^https?:\/\/\S+$/.test(u)) {
            setInvalid(true);
            toast('订阅链接要以 http:// 或 https:// 开头', 'warn');
            return;
        }
        setInvalid(false);
        try {
            const res = await post('/profiles', { url: u, use: !!use });
            setUrl('');
            if (res?.error) {
                toast(`订阅已添加，但启用失败：${stripPeriod(res.error)}`, 'warn');
            } else {
                toast(`已添加订阅「${profileName(res.profile)}」`);
            }
        } catch (err) {
            toast(`添加失败：${stripPeriod(err.message)}`, 'warn');
        } finally {
            refreshProfiles();
            refreshStatus();
        }
    };
    return html`<form class="inline" onSubmit=${(e) => { e.preventDefault(); }}>
        <input class=${'input' + (invalid ? ' is-invalid' : '')} type="url" placeholder="粘贴订阅链接" spellcheck="false"
            value=${url} onInput=${(e) => setUrl(e.currentTarget.value)}
            onKeyDown=${(e) => { if (e.key == 'Enter') { e.preventDefault(); e.currentTarget.nextElementSibling?.click(); } }} />
        <${Button} kind="primary" onClick=${add}>添加订阅<//>
    </form>`;
}

// 上传配置文件（YAML）
function UploadButton() {
    const input = useRef();
    const [busy, setBusy] = useState(false);
    const upload = async (e) => {
        const file = e.currentTarget.files?.[0];
        e.currentTarget.value = '';
        if (!file) {
            return;
        }
        setBusy(true);
        try {
            const text = await file.text();
            const name = file.name.replace(/\.(ya?ml|txt|conf)$/i, '');
            const res = await request('POST', `/profiles/upload?name=${encodeURIComponent(name)}`, text, { raw: true });
            if (res?.error) {
                toast(`已上传，但启用失败：${stripPeriod(res.error)}`, 'warn');
            } else {
                toast(`已上传「${profileName(res.profile)}」`);
            }
        } catch (err) {
            toast(`上传失败：${stripPeriod(err.message)}`, 'warn');
        } finally {
            setBusy(false);
            refreshProfiles();
            refreshStatus();
        }
    };
    return html`<span>
        <input type="file" accept=".yaml,.yml,.txt,.conf,text/yaml" hidden ref=${input} onChange=${upload} />
        <button type="button" class=${'btn' + (busy ? ' is-busy' : '')} onClick=${() => input.current.click()}>上传配置文件</button>
    </span>`;
}

function EditDialog({ profile: p, onClose }) {
    const [form, setForm] = useState({ name: p.name || '', url: p.url || '', info_url: p.info_url || '', user_agent: p.user_agent || '' });
    const set = (k) => (v) => setForm({ ...form, [k]: v });
    const save = async () => {
        const body = { name: form.name, info_url: form.info_url, user_agent: form.user_agent };
        if (p.url) {
            if (!/^https?:\/\/\S+$/.test(form.url.trim())) {
                toast('订阅链接要以 http:// 或 https:// 开头', 'warn');
                return;
            }
            body.url = form.url;
        }
        await patch(`/profiles/${seg(p.id)}`, body);
        await refreshProfiles();
        toast('已保存');
        onClose();
    };
    return html`<${Dialog} title="编辑订阅" onClose=${onClose} actions=${html`
        <${Button} onClick=${onClose}>取消<//>
        <${Button} kind="primary" onClick=${save}>保存<//>`}>
        <${Field} label="订阅名称" wide><${TextInput} value=${form.name} onChange=${set('name')} placeholder="留空则自动使用订阅提供的名称" autofocus /><//>
        ${p.url && html`<${Field} label="订阅链接" wide><${TextInput} type="url" value=${form.url} onChange=${set('url')} /><//>`}
        ${p.url && html`<${Field} label="订阅信息链接" desc="单独取用量 / 到期的地址，一般不用填" wide><${TextInput} type="url" value=${form.info_url} onChange=${set('info_url')} placeholder="可选" /><//>`}
        ${p.url && html`<${Field} label="用户代理（UA）" desc="留空 = MeowX 官方客户端（主控会给官方内核才支持的节点）" wide>
            <${TextInput} value=${form.user_agent} onChange=${set('user_agent')} placeholder="meowx" list="mx-ua" />
            <datalist id="mx-ua"><option value="meowx">MeowX（官方客户端）</option><option value="clash" /><option value="clash.meta" /><option value="mihomo" /></datalist>
        <//>`}
        ${!p.url && html`<p class="hint">这是上传的配置文件，没有订阅链接，不能刷新；要换内容请重新上传。</p>`}
    <//>`;
}

function ProfileRow({ p, active }) {
    const [editing, setEditing] = useState(false);
    const use = async () => {
        try {
            await post(`/profiles/${seg(p.id)}/use`);
            toast(`已切换到「${profileName(p)}」`);
        } catch (err) {
            toast(`切换失败：${stripPeriod(err.message)}，仍在用原来的订阅`, 'warn');
        } finally {
            refreshProfiles();
            refreshStatus();
        }
    };
    const remove = async () => {
        const ask = active
            ? `「${profileName(p)}」正在使用，删除后代理会停止。确定删除？`
            : `删除订阅「${profileName(p)}」？`;
        if (!confirm(ask)) {
            return;
        }
        await del(`/profiles/${seg(p.id)}`);
        toast('已删除');
        await refreshProfiles();
        refreshStatus();
    };
    return html`<div class=${'item can-wrap' + (active ? ' is-current' : '')}>
        <div class="item__main">
            <div class="item__title">
                <span class="item__name">${profileName(p)}</span>
                ${active && html`<span class="badge">使用中</span>`}
                ${p.from_panel && html`<span class="badge is-muted">主控</span>`}
                ${!p.url && html`<span class="badge is-muted">本地文件</span>`}
            </div>
            <${Usage} profile=${p} onRefresh=${p.url ? () => refreshProfile(p.id) : null} />
            ${p.error && html`<div class="error-text" style="margin-top:4px">上次更新失败：${stripPeriod(p.error)}</div>`}
        </div>
        <div class="item__actions">
            ${!active && html`<${Button} small kind="primary" onClick=${use}>使用<//>`}
            <${Button} small kind="on-card" onClick=${() => setEditing(true)}>编辑<//>
            <${Button} small kind="danger" onClick=${remove}>删除<//>
        </div>
        ${editing && html`<${EditDialog} profile=${p} onClose=${() => setEditing(false)} />`}
    </div>`;
}

// 账户下的订阅：没下载到本机的给「导入」（下载并切过去），已在本机的标出来
export function AccountSubscriptions() {
    const { profiles } = useStore();
    const [subs, setSubs] = useState(null);
    const [error, setError] = useState('');
    const load = () => get('/panel/subscriptions').then((r) => { setSubs(r?.subscriptions ?? []); setError(''); }, (err) => setError(err.message));
    useEffect(() => { load(); }, []);
    const localUrls = (profiles?.profiles ?? []).map((p) => p.url).filter(Boolean);
    const importSub = async (sub) => {
        try {
            const res = await post('/panel/import', { url: sub.url, name: sub.name });
            if (res?.error) {
                toast(`已导入，但启用失败：${stripPeriod(res.error)}`, 'warn');
            } else {
                toast(`已导入并切换到「${sub.name}」`);
            }
        } catch (err) {
            toast(`导入失败：${stripPeriod(err.message)}`, 'warn');
        } finally {
            refreshProfiles();
            refreshStatus();
        }
    };
    if (error) {
        return html`<p class="error-text">取账户订阅失败：${stripPeriod(error)}</p>`;
    }
    if (!subs) {
        return html`<p class="hint">正在获取账户订阅…</p>`;
    }
    if (subs.length == 0) {
        return html`<p class="hint">账户下还没有订阅。</p>`;
    }
    return html`<div class="section-title">账户下的订阅</div>
        <div class="list">${subs.map((sub) => {
        const meta = [];
        const used = +sub.traffic_used, total = +sub.traffic_total;
        if (total > 0) {
            meta.push(`${formatBytes(used || 0)} / ${formatBytes(total)}`);
        }
        const expire = sub.expire_at ? Date.parse(sub.expire_at) : NaN;
        if (isFinite(expire)) {
            const days = Math.floor((expire - Date.now()) / 86400000);
            meta.push(days < 0 ? '已过期' : `${days} 天后到期`);
        }
        const have = isDownloaded(sub, localUrls);
        return html`<div class="item">
                <div class="item__main">
                    <div class="item__name">${sub.name}</div>
                    ${meta.length > 0 && html`<div class="item__meta">${meta.join(' · ')}</div>`}
                </div>
                ${have ? html`<span class="badge is-muted">已在本机</span>` : html`<${Button} small kind="primary" onClick=${() => importSub(sub)}>导入<//>`}
            </div>`;
    })}</div>`;
}

// 主控账户卡（同客户端「我的」顶部账户卡）：没登录时粘贴主控「扫码登录」里复制的登录链接
function AccountCard() {
    const { panel } = useStore();
    const [link, setLink] = useState('');
    const [error, setError] = useState('');
    const [key, setKey] = useState(0);
    if (!panel) {
        return html`<${Card} title="主控账户"><p class="muted">正在载入…</p><//>`;
    }
    const loginNow = async () => {
        const l = link.trim();
        if (!/^miaomiaowu:\/\/login\?/.test(l)) {
            setError('这不是妙妙屋的登录链接');
            return;
        }
        setError('');
        try {
            const res = await post('/panel/login', { link: l });
            setLink('');
            toast(res?.imported ? `已登录，并导入了订阅「${res.imported}」` : '已登录');
        } catch (err) {
            setError(stripPeriod(err.message) || '登录失败');
        }
        await refreshPanel();
        refreshProfiles();
    };
    const logout = async () => {
        if (!confirm('确定退出登录？从这个主控导入的订阅会从 NAS 上删除。')) {
            return;
        }
        await post('/panel/logout');
        toast('已退出登录');
        await refreshPanel();
        refreshProfiles();
        refreshStatus();
    };
    if (!panel.logged_in) {
        return html`<${Card} title="主控账户" desc="在主控网页打开「扫码登录」，点「复制登录链接」，粘贴到这里。链接只能用一次，5 分钟内有效。">
            <form class="inline" onSubmit=${(e) => e.preventDefault()}>
                <input class=${'input' + (error ? ' is-invalid' : '')} type="text" placeholder="粘贴登录链接" spellcheck="false" autocomplete="off"
                    value=${link} onInput=${(e) => setLink(e.currentTarget.value)}
                    onKeyDown=${(e) => { if (e.key == 'Enter') { e.preventDefault(); e.currentTarget.nextElementSibling?.click(); } }} />
                <${Button} kind="primary" onClick=${loginNow}>登录<//>
            </form>
            ${error && html`<p class="error-text">${error}</p>`}
        <//>`;
    }
    const host = String(panel.base || '').replace(/^https?:\/\//, '');
    const name = panel.username || '主控账户';
    return html`<${Card} title="主控账户">
        <div class="account">
            <span class="avatar">${panel.avatar
            ? html`<img src=${panel.avatar} alt="" referrerpolicy="no-referrer" onError=${(e) => e.currentTarget.remove()} />`
            : name.slice(0, 1).toUpperCase()}</span>
            <div class="account__main">
                <div class="account__name">${name}</div>
                <div class="account__host">${host}</div>
            </div>
            <div class="card__actions">
                <${Button} small onClick=${async () => { await refreshPanel(); setKey(key + 1); }}>刷新<//>
                <${Button} small kind="danger" onClick=${logout}>退出登录<//>
            </div>
        </div>
        <${AccountSubscriptions} key=${key} />
    <//>`;
}

export function Profiles({ active }) {
    const { profiles: data } = useStore();
    useEffect(() => {
        if (active) {
            refreshProfiles().catch(() => { });
            refreshPanel();
        }
    }, [active]);
    const list = data?.profiles ?? [];
    return html`<div>
        <${Card} title="订阅" desc="订阅链接按「设置」里的间隔自动更新；点「更新于 …」立刻更新。" i=${0}>
            ${!data
            ? html`<p class="muted">正在载入…</p>`
            : list.length == 0
                ? html`<div class="empty">还没有订阅</div>`
                : html`<div class="list">${list.map((p) => html`<${ProfileRow} key=${p.id} p=${p} active=${p.id == data.active} />`)}</div>`}
            <${QuickAdd} />
            <div class="inline"><${UploadButton} /></div>
        <//>
        <${AccountCard} />
    </div>`;
}
