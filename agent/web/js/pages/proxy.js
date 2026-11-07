// 透明代理（settings.proxy）：NAS 本机 / 其它容器走内核 TUN，旁路由（局域网设备）走 TPROXY。
// 打开或改了之后进试用期：到点没在顶部横条点「保留设置」，控制服务自动关掉透明代理（防止把 NAS 的网络弄断）
import { html, useState, useEffect } from '../lib.js';
import { get } from '../api.js';
import { useStore, toast } from '../store.js';
import { Card, Field, Switch, NumberInput, Select, ListInput, Button, SaveBar, useSettingsForm } from '../ui.js';
import { stripPeriod } from '../util.js';

// 局域网入口网卡：宿主机的物理网卡逐个勾选；都不选 = 全部物理网卡
function InterfacePicker({ value, onChange, host }) {
    const chosen = value ?? [];
    const physical = host?.Physical ?? [];
    // 设置里有、但这台机器上没找到的网卡也列出来（可以取消掉）
    const names = [...physical.map((i) => i.name), ...chosen.filter((n) => !physical.some((i) => i.name == n))];
    if (!host) {
        return html`<p class="hint">正在读取网卡…</p>`;
    }
    if (names.length == 0) {
        return html`<p class="hint">没找到有 IPv4 地址的物理网卡。</p>`;
    }
    const toggle = (name, on) => onChange(on ? [...chosen, name] : chosen.filter((n) => n != name));
    return html`<div class="rows">${names.map((name) => {
        const info = physical.find((i) => i.name == name);
        return html`<label class="rows__row" style="cursor:pointer">
            <input type="checkbox" class="switch" checked=${chosen.includes(name)} onChange=${(e) => toggle(name, e.currentTarget.checked)} />
            <span class="grow">
                <span class="mono" style="font-weight:700">${name}</span>
                ${info && !info.up && html` <span class="badge is-muted">未连接</span>`}
                ${!info && html` <span class="badge is-muted">没找到这块网卡</span>`}
                <div class="item__meta mono">${(info?.subnets ?? []).join('、')}</div>
            </span>
        </label>`;
    })}</div>`;
}

export function ProxyPage({ active }) {
    const { status } = useStore();
    const form = useSettingsForm('proxy',
        (s) => ({ proxy: s.proxy, mixin: { tproxy_port: s.mixin.tproxy_port, dns_enabled: s.mixin.dns.enabled } }),
        (s, v) => { s.proxy = v.proxy; },
        active);
    const [host, setHost] = useState(null);
    useEffect(() => {
        if (active) {
            get('/host').then(setHost, () => setHost({ Physical: [], Bridges: [] }));
        }
    }, [active]);
    const b = (path) => form.bind('proxy.' + path);
    const v = (path) => form.value('proxy.' + path);
    const onOff = (path, label) => html`<${Switch} label=${label} checked=${v(path)} onChange=${b(path).onChange} />`;

    const save = async () => {
        const res = await form.save(true);
        if (res?.error) {
            toast(`已保存，但应用失败：${stripPeriod(res.error)}`, 'warn');
        } else if (res?.trial?.pending) {
            toast('透明代理已生效。确认 NAS 与局域网都能正常上网后，点顶部的「保留设置」。');
        } else if (res?.settings?.proxy?.enabled && !res?.settings?.enabled) {
            toast('已保存。MeowX 没在运行，启动后透明代理才生效。');
        } else {
            toast('已保存并应用');
        }
    };

    if (!form.draft) {
        return html`<${Card} title="透明代理">${form.error
            ? html`<p class="error-text">${form.error}</p><${Button} onClick=${form.reload}>重试<//>`
            : html`<p class="muted">正在载入…</p>`}<//>`;
    }

    const enabled = v('enabled');
    const tun = v('host_proxy') || v('container_proxy');
    const lan = v('lan_proxy');
    const mixin = form.draft.mixin;

    return html`<div>
        <${Card} title="透明代理" i=${0}
            desc="不用在设备上填代理：NAS 本机、NAS 上的其它容器、把网关指向 NAS 的局域网设备，流量自动走 MeowX。"
            actions=${onOff('enabled', '启用透明代理')}>
            <p class="hint">打开或改了透明代理后有 ${v('keep_minutes') || 0} 分钟试用期：确认网络正常后点顶部的「保留设置」；没点的话到时间自动关掉，防止把 NAS 的网络（连带 SSH、这个网页）弄断。</p>
            ${enabled && !status?.enabled && html`<p class="error-text">MeowX 没在运行，启动后透明代理才生效。</p>`}
        <//>

        <${Card} title="接管哪些流量" i=${1}>
            <${Field} label="NAS 本机" desc="NAS 自己发起的连接（下载、拉 Docker 镜像、套件更新等）">${onOff('host_proxy', 'NAS 本机')}<//>
            <${Field} label="其它容器" desc="NAS 上其它 Docker 容器的流量">${onOff('container_proxy', '其它容器')}<//>
            ${v('container_proxy') && html`<p class="error-text" style="margin:0 0 6px">注意：容器发布到外面的端口（如 Nginx Proxy Manager、Gitea 等）可能受影响，打开后请检查这些服务还能不能从外面访问。</p>`}
            <${Field} label="旁路由（局域网设备）" desc="局域网设备把网关（和 DNS）改成 NAS 的 IP 后，它们的流量经 NAS 走代理。只代理 IPv4。">${onOff('lan_proxy', '旁路由')}<//>
        <//>

        ${tun && html`<${Card} title="NAS 本机与容器" desc="走内核的 TUN。" i=${2}>
            <${Field} label="TCP 模式" desc="Redirect = TCP 用 nftables 重定向给内核（auto-redirect），比全进 TUN 省 CPU；UDP 都走 TUN" wide>
                <${Select} ...${b('tcp_mode')} options=${[['tun', 'TUN 模式'], ['redirect', 'Redirect 模式']]} />
            <//>
            <${Field} label="IPv6" desc="NAS 本机与容器的 IPv6 流量也走代理">${onOff('ipv6', 'IPv6')}<//>
        <//>`}

        ${lan && html`<${Card} title="旁路由" desc="走 TPROXY。" i=${3}>
            ${!mixin.tproxy_port && html`<p class="error-text">「混入设置 › 入站」里的 TPROXY 端口是空的，旁路由不会生效。</p>`}
            <div class="section-title">局域网入口网卡（都不选 = 全部物理网卡）</div>
            <${InterfacePicker} ...${b('lan_interfaces')} host=${host} />
        <//>`}

        <${Card} title="DNS 与绕过" i=${4}>
            <${Field} label="DNS 劫持" desc="把 DNS 查询交给内核的 DNS（Fake-IP 要靠它）">${onOff('dns_hijack', 'DNS 劫持')}<//>
            ${v('dns_hijack') && lan && !mixin.dns_enabled && html`<p class="error-text" style="margin:0 0 6px">「混入设置 › DNS」没启用，旁路由的 DNS 不会被劫持。</p>`}
            <${Field} label="IPv4 保留地址" desc="发往这些地址的不代理（局域网、Docker 网段、组网段等）。NAS 网卡所在的网段总是直连。一行一个。" wide top>
                <${ListInput} ...${b('reserved_ip')} rows=${4} />
            <//>
            <${Field} label="IPv6 保留地址" desc="同上，开了 IPv6 时生效" wide top>
                <${ListInput} ...${b('reserved_ip6')} rows=${4} />
            <//>
        <//>

        <${Card} title="安全阀" i=${5}>
            <${Field} label="试用时长（分钟）" desc="打开或改了透明代理之后，这么多分钟内没点「保留设置」就自动关掉；0 = 不试用、立即生效（不建议）" wide>
                <${NumberInput} ...${b('keep_minutes')} placeholder="0" max="120" />
            <//>
        <//>

        <${SaveBar} form=${form} onSave=${save} />
    </div>`;
}
