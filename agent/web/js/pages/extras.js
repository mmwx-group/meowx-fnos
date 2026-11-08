// 辅助功能：不属于代理配置本身的小工具。现在只有「加白」一组——po0 加白、GGY 加白（以前在「订阅」页）、AkileCDN 白名单
// （同客户端「辅助功能」，AkileCDN 白名单与 iOS / macOS / meowC / OpenWrt 同一份规格）
import { html, useState, useEffect, useRef } from '../lib.js';
import { post } from '../api.js';
import { useStore } from '../store.js';
import { Card, Button, Switch, Dialog } from '../ui.js';
import { stripPeriod, relativeTime, same } from '../util.js';

// po0 加白卡（同客户端「流量控制 → po0 加白」+「自定义 po0 服务器」）：每次操作后用回包原地重画；token 只显示脱敏写法
function Po0Card() {
    const { panel } = useStore();
    const [st, setSt] = useState(null);
    const [error, setError] = useState('');
    const [address, setAddress] = useState('');
    const [token, setToken] = useState('');
    const [bad, setBad] = useState({});
    const act = async (body) => {
        setError('');
        try {
            setSt(await post('/panel/po0', body));
        } catch (err) {
            setError(stripPeriod(err.message) || '操作失败');
        }
    };
    useEffect(() => { act({ action: 'status' }); }, []);
    if (!st) {
        return html`<${Card} title="po0 加白">${error ? html`<p class="error-text">${error}</p>` : html`<p class="muted">正在载入…</p>`}<//>`;
    }
    const add = async () => {
        const a = address.trim(), t = token.trim();
        const badToken = !/^pgnfw_[A-Za-z0-9_-]{4,200}(@[0-9]{1,2})?$/.test(t);
        setBad({ address: !a, token: badToken });
        if (!a) {
            setError('请填写 po0 的加白服务器地址');
            return;
        }
        if (badToken) {
            setError('token 不对：应为 pgnfw_ 开头的那一整串，可带 @槽位');
            return;
        }
        await act({ action: 'add', address: a, token: t });
        setAddress('');
        setToken('');
    };
    const toggle = html`<${Switch} label="po0 加白" checked=${st.enabled} onChange=${(on) => act({ action: on ? 'enable' : 'disable' })} />`;
    const panelList = st.panel ?? [];
    const custom = st.custom ?? [];
    return html`<${Card} title="po0 加白" actions=${toggle}
        desc="把 NAS 的出口 IP 上报到 po0 的加白服务器（主控登记的与自己添加的），内核运行时每 10 分钟自动加白；访问这些 po0 的流量一律直连。">
        ${st.enabled && html`
            <div class="section-title">主控登记（${panelList.length}）</div>
            ${!panel?.logged_in
                ? html`<p class="hint">登录主控账户后自动获取。</p>`
                : panelList.length == 0
                    ? html`<p class="hint">这个账户下没有 po0 服务器。</p>`
                    : html`<div class="list">${panelList.map((s) => html`<div class="item"><div class="item__main">
                        <div class="item__mono">${s.name || s.host}</div>
                        ${s.name && html`<div class="item__meta mono">${s.host}</div>`}
                    </div></div>`)}</div>`}`}
        <div class="section-title">自定义 po0 服务器（${custom.length}）</div>
        ${custom.length > 0 && html`<div class="list">${custom.map((s, i) => html`<div class="item">
            <div class="item__main">
                <div class="item__mono">${s.address}</div>
                <div class="item__meta mono">${s.token}</div>
            </div>
            <${Button} small kind="danger" onClick=${() => act({ action: 'remove', index: i })}>删除<//>
        </div>`)}</div>`}
        ${!st.enabled && custom.length > 0 && html`<p class="hint">「po0 加白」关着，这些服务器暂时不上报。</p>`}
        <form class="inline wrap" onSubmit=${(e) => e.preventDefault()}>
            <input class=${'input is-full' + (bad.address ? ' is-invalid' : '')} placeholder="加白服务器地址：IP 或域名，可带端口" spellcheck="false" autocomplete="off"
                value=${address} onInput=${(e) => setAddress(e.currentTarget.value)} />
            <input class=${'input' + (bad.token ? ' is-invalid' : '')} type="password" placeholder="token，pgnfw_ 开头（可带 @槽位）" autocomplete="new-password"
                value=${token} onInput=${(e) => setToken(e.currentTarget.value)} />
            <${Button} kind="primary" onClick=${add}>添加<//>
        </form>
        ${error && html`<p class="error-text">${error}</p>`}
        <p class="hint">地址填 po0 的加白服务器地址（IP 或域名，可带端口，如 1.2.3.4 或 fw.example.com:8443），不一定是节点地址；token 在 po0 后台复制。</p>
    <//>`;
}

// GGY 的输入框接受裸 token 或整条分享链接（带 ? 的当链接，取查询串里 token 的值）；合法 = A–Z a–z 0–9 _ -，8–200 位（同 meowx-core）
function parseGgyToken(input) {
    let s = String(input ?? '').trim();
    const q = s.indexOf('?');
    if (q >= 0) {
        s = (new URLSearchParams(s.slice(q + 1).split('#')[0]).get('token') ?? '').trim();
    }
    return /^[A-Za-z0-9_-]{8,200}$/.test(s) ? s : null;
}

// GGY 加白卡（同客户端「GGY 加白」，紧跟 po0 加白卡）：开关 + token 列表（脱敏写法 + 上一次的结果）
function GgyCard() {
    const [st, setSt] = useState(null);
    const [error, setError] = useState('');
    const [token, setToken] = useState('');
    const [bad, setBad] = useState(false);
    const act = async (body) => {
        setError('');
        try {
            setSt(await post('/panel/ggy', body));
        } catch (err) {
            setError(stripPeriod(err.message) || '操作失败');
        }
    };
    useEffect(() => { act({ action: 'status' }); }, []);
    if (!st) {
        return html`<${Card} title="GGY 加白">${error ? html`<p class="error-text">${error}</p>` : html`<p class="muted">正在载入…</p>`}<//>`;
    }
    const add = async () => {
        const t = parseGgyToken(token);
        setBad(!t);
        if (!t) {
            setError(token.trim() ? 'token 格式不对' : '请填写 token');
            return;
        }
        await act({ action: 'add', token: t });
        setToken('');
    };
    // 上一次的结果：成功 / 失败原因 / 还没报过
    const result = (r) => {
        if (!r) {
            return html`<div class="item__meta">还没报过</div>`;
        }
        const when = +r.at > 0 ? ' · ' + relativeTime(r.at) : '';
        return r.ok
            ? html`<div class="item__meta ok-text">成功${when}</div>`
            : html`<div class="item__meta error-text" title=${r.error || ''}>失败：${r.error || '操作失败'}${when}</div>`;
    };
    const tokens = st.tokens ?? [];
    const toggle = html`<${Switch} label="GGY 加白" checked=${st.enabled} onChange=${(on) => act({ action: on ? 'enable' : 'disable' })} />`;
    return html`<${Card} title="GGY 加白" actions=${toggle}
        desc="把 NAS 的出口 IP 上报到 GGY（咕咕云）服务器的防火墙白名单：填分享链接里的 token（也可以直接粘贴整条链接），每 10 分钟自动加白；上报走直连。">
        <div class="section-title">token（${tokens.length}）</div>
        ${tokens.length > 0 && html`<div class="list">${tokens.map((item, i) => html`<div class="item">
            <div class="item__main">
                <div class="item__mono">${item.token}</div>
                ${result(item.result)}
            </div>
            <${Button} small kind="danger" onClick=${() => act({ action: 'remove', index: i })}>删除<//>
        </div>`)}</div>`}
        ${tokens.some((item) => item.result?.invalid) && html`<p class="hint">有 token 无效或已失效：请到 GGY 重新生成分享链接，添加新的 token 后删掉旧的。</p>`}
        ${!st.enabled && tokens.length > 0 && html`<p class="hint">「GGY 加白」开关关着，添加的 token 暂不上报。</p>`}
        <form class="inline" onSubmit=${(e) => e.preventDefault()}>
            <input class=${'input' + (bad ? ' is-invalid' : '')} type="password" placeholder="token，也可以粘贴整条分享链接" autocomplete="new-password"
                value=${token} onInput=${(e) => setToken(e.currentTarget.value)}
                onKeyDown=${(e) => { if (e.key == 'Enter') { e.preventDefault(); e.currentTarget.nextElementSibling?.click(); } }} />
            <${Button} kind="primary" onClick=${add}>添加<//>
        </form>
        ${error && html`<p class="error-text">${error}</p>`}
    <//>`;
}

// API Key：去掉首尾空白与开头的「X-API-Key:」/「Bearer 」（不分大小写）；合法 = ak_ 开头、后面 8–200 位 A–Z a–z 0–9 _ -（同 meowx-core）
function parseAkileKey(input) {
    let s = String(input ?? '').trim();
    if (/^x-api-key:/i.test(s)) {
        s = s.slice(10).trim();
    } else if (/^bearer /i.test(s)) {
        s = s.slice(7).trim();
    }
    return /^ak_[A-Za-z0-9_-]{8,200}$/.test(s) ? s : null;
}

const akileDesc = '把 NAS 的直连出口 IPv4 写进所选 AkileCDN 规则的入口白名单。控制服务启动、打开开关、添加节点时立刻更新，每 10 分钟检查一次出口 IP、变了才更新。一条规则只能放一个 IP，多台设备都开会互相覆盖。';

// 上一次的结果：已更新为 / 已是（绿）、失败原因（红）、还没更新过（灰）
function AkileResult({ r }) {
    if (!r) {
        return html`<div class="item__meta">还没更新过</div>`;
    }
    const when = +r.at > 0 ? ' · ' + relativeTime(r.at) : '';
    if (r.ok) {
        return html`<div class="item__meta ok-text">${r.kind == 'updated' ? '已更新为' : '已是'} ${r.ip}${when}</div>`;
    }
    return html`<div class="item__meta error-text" title=${r.error || ''}>${r.error || '操作失败'}${when}</div>`;
}

// 「从订阅添加节点」：选节点（带搜索）→ 同一端口有多条规则时选规则
function AkilePicker({ added, onClose, onAdded }) {
    const [nodes, setNodes] = useState(null);
    const [query, setQuery] = useState('');
    const [rules, setRules] = useState(null);   // { node, list }
    const [error, setError] = useState('');
    useEffect(() => {
        post('/panel/akile', { action: 'nodes' }).then((res) => setNodes(res.nodes ?? []), (err) => {
            setNodes([]);
            setError(stripPeriod(err.message) || '操作失败');
        });
    }, []);
    const add = async (node, rule) => {
        setError('');
        try {
            onAdded(await post('/panel/akile', { action: 'add', rule_id: rule.id, port: node.port, name: node.name, label: rule.summary ?? '' }));
        } catch (err) {
            setError(stripPeriod(err.message) || '操作失败');
        }
    };
    // 点了一个节点：按它的端口查规则，0 条报原因、1 条直接加、多条让用户选
    const pick = async (node) => {
        setError('');
        let res;
        try {
            res = await post('/panel/akile', { action: 'rules', port: node.port });
        } catch (err) {
            setError(stripPeriod(err.message) || '操作失败');
            return;
        }
        const list = res.rules ?? [];
        if (list.length == 0) {
            setError(`没有监听端口 ${node.port} 的规则：确认这个节点是 AkileCDN 的转发、API Key 属于这条规则的账号`);
        } else if (list.length == 1) {
            await add(node, list[0]);
        } else {
            setRules({ node, list });
        }
    };
    let body;
    if (rules) {
        body = html`
            <p class="hint">${rules.node.name} · ${rules.node.server}:${rules.node.port}</p>
            <div class="list akile-options">${rules.list.map((rule) => {
                const done = added.includes(rule.id);
                const parts = [`规则 #${rule.id}`];
                if (rule.summary) {
                    parts.push(rule.summary);
                }
                if (rule.node_id) {
                    parts.push(`节点 ${rule.node_id}`);
                }
                parts.push(`当前白名单 ${rule.entry_whitelist || '未设置'}`);
                return html`<div class="item">
                    <div class="item__main">
                        <div class="item__name akile-wrap">${parts.join(' · ')}</div>
                        ${done && html`<div class="item__meta">这条规则已经添加过</div>`}
                    </div>
                    <${Button} small kind="primary" disabled=${done} onClick=${() => add(rules.node, rule)}>选这条<//>
                </div>`;
            })}</div>`;
    } else if (!nodes) {
        body = html`<p class="muted">正在载入…</p>`;
    } else if (nodes.length == 0) {
        body = !error && html`<div class="empty">当前订阅里没有可选的节点</div>`;
    } else {
        const q = query.trim().toLowerCase();
        const shown = nodes.filter((n) => !q || String(n.name).toLowerCase().includes(q) || `${n.server}:${n.port}`.toLowerCase().includes(q));
        body = html`
            <input class="input" type="search" placeholder="搜索节点" spellcheck="false" autocomplete="off" autofocus
                value=${query} onInput=${(e) => setQuery(e.currentTarget.value)} />
            <div class="list akile-options">${shown.length == 0
                ? html`<div class="empty">没有匹配的节点</div>`
                : shown.map((n) => html`<div class="item">
                    <div class="item__main">
                        <div class="item__name">${n.name}</div>
                        <div class="item__meta mono">${n.server}:${n.port}${n.type ? ' · ' + n.type : ''}</div>
                    </div>
                    <${Button} small kind="primary" onClick=${() => pick(n)}>选择<//>
                </div>`)}</div>`;
    }
    return html`<${Dialog} title=${rules ? '选择要加白的规则' : '从当前订阅里选择节点'} onClose=${onClose} actions=${html`
        ${rules && html`<${Button} onClick=${() => setRules(null)}>返回<//>`}
        <${Button} onClick=${onClose}>取消<//>`}>
        ${body}
        ${error && html`<p class="error-text">${error}</p>`}
    <//>`;
}

// AkileCDN 白名单卡（同客户端「AkileCDN 白名单」+「AkileCDN 节点」子页）：开关、API Key（只显示脱敏写法，完整值只在输入框里）、
// 节点列表与每个节点上一次的结果、「从订阅添加节点」。打开开关、换 Key、开着时添加当场写一次，回包里带着结果；
// 页面开着、开关开着时每 3 秒刷新一次结果
function AkileCard({ active }) {
    const [st, setSt] = useState(null);
    const [error, setError] = useState('');
    const [key, setKey] = useState('');
    const [badKey, setBadKey] = useState(false);
    const [changing, setChanging] = useState(false);
    const [picking, setPicking] = useState(false);
    const working = useRef(0);
    const act = async (body) => {
        setError('');
        working.current++;
        try {
            const res = await post('/panel/akile', body);
            setSt(res);
            return res;
        } catch (err) {
            setError(stripPeriod(err.message) || '操作失败');
            throw null;   // 已经显示在卡片里，按钮不再弹提示
        } finally {
            working.current--;
        }
    };
    useEffect(() => { act({ action: 'status' }).catch(() => { }); }, []);
    const polling = !!(active && st?.enabled && st?.nodes?.length);
    useEffect(() => {
        if (!polling) {
            return;
        }
        const t = setInterval(() => {
            if (document.hidden || working.current > 0) {
                return;
            }
            post('/panel/akile', { action: 'status' }).then((res) => {
                if (working.current == 0) {
                    setSt((cur) => (same(cur, res) ? cur : res));
                }
            }, () => { });
        }, 3000);
        return () => clearInterval(t);
    }, [polling]);
    if (!st) {
        return html`<${Card} title="AkileCDN 白名单">${error ? html`<p class="error-text">${error}</p>` : html`<p class="muted">正在载入…</p>`}<//>`;
    }
    const saveKey = async () => {
        const k = parseAkileKey(key);
        setBadKey(!k);
        if (!k) {
            setError(key.trim() ? 'API Key 应以 ak_ 开头' : '先填写 API Key');
            return;
        }
        await act({ action: 'setkey', key: k });
        setKey('');
        setChanging(false);
    };
    const nodes = st.nodes ?? [];
    const max = +st.max || 20;
    const blocked = !st.has_key ? '先填写 API Key' : nodes.length >= max ? `最多 ${max} 个节点` : '';
    const toggle = html`<${Switch} label="AkileCDN 白名单" checked=${st.enabled} onChange=${(on) => act({ action: on ? 'enable' : 'disable' }).catch(() => { })} />`;
    return html`<${Card} title="AkileCDN 白名单" actions=${toggle} desc=${akileDesc}>
        <div class="section-title">API Key</div>
        ${st.has_key && !changing
            ? html`<div class="list"><div class="item">
                <div class="item__main"><div class="item__mono">${st.key}</div></div>
                <div class="item__actions">
                    <${Button} small onClick=${() => { setChanging(true); setError(''); }}>更换<//>
                    <${Button} small kind="danger" onClick=${() => act({ action: 'clearkey' })}>删除<//>
                </div>
            </div></div>`
            : html`<form class="inline" onSubmit=${(e) => e.preventDefault()}>
                <input class=${'input' + (badKey ? ' is-invalid' : '')} type="password" placeholder="API Key，ak_ 开头" autocomplete="new-password"
                    value=${key} onInput=${(e) => setKey(e.currentTarget.value)}
                    onKeyDown=${(e) => { if (e.key == 'Enter') { e.preventDefault(); e.currentTarget.nextElementSibling?.click(); } }} />
                <${Button} kind="primary" onClick=${saveKey}>保存<//>
                ${changing && html`<${Button} onClick=${() => { setChanging(false); setKey(''); setBadKey(false); setError(''); }}>取消<//>`}
            </form>
            <p class="hint">在 AkileCDN 后台「开放接口」里创建 API Key（ak_ 开头）。Key 等同账号权限，只保存在本机。</p>`}
        <div class="section-title">AkileCDN 节点（${nodes.length}）</div>
        ${nodes.length > 0 && html`<div class="list">${nodes.map((n, i) => html`<div class="item">
            <div class="item__main">
                <div class="item__name">${n.name}</div>
                <div class="item__meta">${[`规则 #${n.rule_id}`].concat(n.label ? [n.label] : [], [`端口 ${n.port}`]).join(' · ')}</div>
                <${AkileResult} r=${n.result} />
            </div>
            <${Button} small kind="danger" onClick=${() => act({ action: 'remove', index: i })}>删除<//>
        </div>`)}</div>`}
        <div class="inline akile-add">
            <${Button} kind="primary" disabled=${!!blocked} onClick=${() => { setError(''); setPicking(true); }}>从订阅添加节点<//>
            ${blocked && html`<span class="hint">${blocked}</span>`}
        </div>
        ${error && html`<p class="error-text">${error}</p>`}
        ${!st.enabled && html`<p class="hint">「AkileCDN 白名单」开关关着，添加的节点暂不更新。</p>`}
        ${picking && html`<${AkilePicker} added=${nodes.map((n) => n.rule_id)} onClose=${() => setPicking(false)}
            onAdded=${(res) => { setSt(res); setPicking(false); }} />`}
    <//>`;
}

// 「加白」组标题：说明收进标题旁的 ⓘ（点开 / 收起）
function WhitelistHead() {
    const [open, setOpen] = useState(false);
    return html`<div class="group enter" style="--i:0">
        <div class="group__head">
            <h2 class="group__title">加白</h2>
            <button type="button" class=${'group__info' + (open ? ' is-open' : '')} aria-label="关于加白" aria-expanded=${open}
                onClick=${() => setOpen(!open)}>ⓘ</button>
        </div>
        ${open && html`<p class="group__tip">把 NAS 的直连出口 IP 告诉节点那一端的防火墙：po0、GGY 服务器把发起请求的 IP 加白，AkileCDN 把它写进规则的入口白名单。
            内核运行时每 10 分钟上报一次（AkileCDN 只在出口 IP 变了时才写，控制服务启动后的第一轮一定写）；上报一律直连，不经代理。</p>`}
    </div>`;
}

export function Extras({ active }) {
    return html`<div>
        <${WhitelistHead} />
        <div class="grid-2">
            <div><${Po0Card} /><${GgyCard} /></div>
            <div><${AkileCard} active=${active} /></div>
        </div>
    </div>`;
}
