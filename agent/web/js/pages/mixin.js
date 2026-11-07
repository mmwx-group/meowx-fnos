// 混入设置：盖在订阅上的那部分配置（settings.mixin，同 OpenWrt 版「混入配置」）。保存 = 整份设置写回并重载内核
import { html, useState, useEffect } from '../lib.js';
import { toast } from '../store.js';
import { Card, Field, Switch, TextInput, NumberInput, Select, ListInput, Button, SaveBar, useSettingsForm } from '../ui.js';
import { same, stripPeriod } from '../util.js';

// 逗号分隔的一行（表格里的 DNS 服务器、IP）：正在输入的文本自己留着
function CsvInput({ value, onChange, placeholder }) {
    const list = value ?? [];
    const parse = (t) => t.split(/[\s,，]+/).map((s) => s.trim()).filter(Boolean);
    const [text, setText] = useState(list.join(', '));
    useEffect(() => {
        if (!same(parse(text), list)) {
            setText(list.join(', '));
        }
    }, [JSON.stringify(list)]);
    return html`<input class="input mono" value=${text} placeholder=${placeholder} spellcheck="false"
        onInput=${(e) => { const t = e.currentTarget.value; setText(t); onChange(parse(t)); }} />`;
}

// 表格：每行一组控件 + 删除，末尾「添加」
function Rows({ value, onChange, empty, make, addLabel, render }) {
    const rows = value ?? [];
    const setRow = (i, row) => onChange(rows.map((r, j) => (j == i ? row : r)));
    return html`<div class="rows">
        ${rows.map((row, i) => html`<div class="rows__row">
            ${render(row, (patch) => setRow(i, { ...row, ...patch }))}
            <button type="button" class="close-x" title="删除" onClick=${() => onChange(rows.filter((_, j) => j != i))}>×</button>
        </div>`)}
        ${rows.length == 0 && empty && html`<p class="hint" style="margin:0">${empty}</p>`}
        <div><${Button} small onClick=${() => onChange([...rows, make()])}>${addLabel}<//></div>
    </div>`;
}

const onOff = (bind, label) => html`<${Switch} label=${label} checked=${bind.value} onChange=${bind.onChange} />`;

export function Mixin({ active }) {
    const form = useSettingsForm('mixin', (s) => s.mixin, (s, v) => { s.mixin = v; }, active);
    const b = form.bind;
    const v = form.value;

    const save = async () => {
        const res = await form.save(true);
        if (res?.error) {
            toast(`已保存，但应用失败：${stripPeriod(res.error)}。运行中的配置保持不变。`, 'warn');
        } else {
            toast('已保存并应用');
        }
    };

    if (!form.draft) {
        return html`<${Card} title="混入设置">${form.error
            ? html`<p class="error-text">${form.error}</p><${Button} onClick=${form.reload}>重试<//>`
            : html`<p class="muted">正在载入…</p>`}<//>`;
    }

    return html`<div>
        <p class="hint" style="margin:0 4px 12px">这些设置盖在订阅上：留空的项保持订阅原样。保存后内核按新配置重新启动。</p>

        <${Card} title="全局" i=${0}>
            <${Field} label="日志级别" wide>
                <${Select} ...${b('log_level')} options=${[['', '不修改'], ['silent', 'silent'], ['error', 'error'], ['warning', 'warning'], ['info', 'info'], ['debug', 'debug']]} />
            <//>
            <${Field} label="出站模式" desc="内核启动时用的模式；运行中在「概览」里切换会即时生效并记到这里" wide>
                <${Select} ...${b('mode')} options=${[['', '不修改'], ['rule', '规则模式'], ['global', '全局模式'], ['direct', '直连模式']]} />
            <//>
            <${Field} label="IPv6" desc="内核处理 IPv6 流量">${onOff(b('ipv6'), 'IPv6')}<//>
            <${Field} label="阻止 QUIC" desc="拒绝发往 UDP 443 端口的流量（QUIC / HTTP/3），让应用回落到 TCP，同 MeowX 客户端的「阻止 QUIC」。适合走 QUIC 的应用落地地区和 TCP 不一样的情况。只在规则模式下生效。">${onOff(b('block_quic'), '阻止 QUIC')}<//>
            <${Field} label="统一延迟" desc="测延迟时去掉握手耗时，只算一次往返">${onOff(b('unified_delay'), '统一延迟')}<//>
            <${Field} label="TCP 并发" desc="同时连目标的全部 IP，用最先连上的">${onOff(b('tcp_concurrent'), 'TCP 并发')}<//>
            <${Field} label="保存节点 / 策略组选择" desc="内核重启后保持各代理组手动选的节点">${onOff(b('store_selected'), '保存节点选择')}<//>
            <${Field} label="定时更新 GeoX 文件">${onOff(b('geo_auto_update'), '定时更新 GeoX 文件')}<//>
            ${v('geo_auto_update') && html`<${Field} label="GeoX 文件更新间隔" desc="小时" wide><${NumberInput} ...${b('geo_update_interval')} placeholder="不修改" /><//>`}
        <//>

        <${Card} title="入站" desc="端口留空 = 不开（订阅里自带的端口不用）；局域网设备用「混合端口」上网（HTTP / SOCKS5 都认）。" i=${1}>
            <${Field} label="允许局域网访问" desc="关掉后代理端口只有 NAS 本机能用">${onOff(b('allow_lan'), '允许局域网访问')}<//>
            <${Field} label="混合端口" wide><${NumberInput} ...${b('mixed_port')} max="65535" placeholder="不开" /><//>
            <${Field} label="HTTP 端口" wide><${NumberInput} ...${b('http_port')} max="65535" placeholder="不开" /><//>
            <${Field} label="SOCKS 端口" wide><${NumberInput} ...${b('socks_port')} max="65535" placeholder="不开" /><//>
            <${Field} label="Redirect 端口" wide><${NumberInput} ...${b('redir_port')} max="65535" placeholder="不开" /><//>
            <${Field} label="TPROXY 端口" desc="透明代理的旁路由（局域网设备）走这个端口，开旁路由时不能留空" wide><${NumberInput} ...${b('tproxy_port')} max="65535" placeholder="不开" /><//>
            <${Field} label="代理端口要密码" desc="打开后连代理端口要用下面的账号密码">${onOff(b('authentication'), '代理端口要密码')}<//>
            ${v('authentication') && html`
                <${Field} label="局域网与本机免密" desc="只有从外网（经路由器端口转发）连进来的要密码">${onOff(b('skip_auth_lan'), '局域网与本机免密')}<//>
                <div class="section-title">账号</div>
                <${Rows} ...${b('users')} addLabel="添加账号" empty="没有账号时等于不要密码。"
                    make=${() => ({ enabled: true, username: '', password: '' })}
                    render=${(u, set) => html`
                        <${Switch} label="启用" checked=${u.enabled} onChange=${(on) => set({ enabled: on })} />
                        <input class="input grow" placeholder="用户名" spellcheck="false" autocomplete="off" value=${u.username} onInput=${(e) => set({ username: e.currentTarget.value })} />
                        <input class="input grow mono" placeholder="密码" spellcheck="false" autocomplete="off" value=${u.password} onInput=${(e) => set({ password: e.currentTarget.value })} />`} />`}
        <//>

        <${Card} title="TUN" desc="透明代理里「NAS 本机 / 其它容器」走内核 TUN 时用的参数；留空用缺省值。" i=${2}>
            <${Field} label="设备名称" wide><${TextInput} ...${b('tun.device')} placeholder="meowx" /><//>
            <${Field} label="栈" wide>
                <${Select} ...${b('tun.stack')} options=${[['', '缺省（Mixed）'], ['system', 'System'], ['gvisor', 'gVisor'], ['mixed', 'Mixed']]} />
            <//>
            <${Field} label="最大传输单元（MTU）" wide><${NumberInput} ...${b('tun.mtu')} placeholder="缺省" /><//>
            <${Field} label="DNS 劫持" desc="一行一个；留空用 tcp://any:53 与 udp://any:53" wide top><${ListInput} ...${b('tun.dns_hijack')} rows=${2} placeholder="udp://any:53" /><//>
        <//>

        <${Card} title="DNS" i=${3}>
            <${Field} label="启用">${onOff(b('dns.enabled'), '启用 DNS')}<//>
            <${Field} label="DNS 监听" desc="透明代理劫持的 DNS 交给这里" wide><${TextInput} ...${b('dns.listen')} mono placeholder="[::]:1053" /><//>
            <${Field} label="IPv6" desc="DNS 应答里给 IPv6 地址（AAAA）">${onOff(b('dns.ipv6'), 'DNS IPv6')}<//>
            <${Field} label="DNS 模式" wide>
                <${Select} ...${b('dns.mode')} options=${[['', '不修改'], ['fake-ip', 'Fake-IP'], ['redir-host', 'Redir-Host']]} />
            <//>
            <${Field} label="Fake-IP 范围" wide><${TextInput} ...${b('dns.fake_ip_range')} mono placeholder="198.18.0.1/16" /><//>
            <${Field} label="Fake-IP 过滤列表" desc="一行一个，这些域名拿真实 IP" wide top><${ListInput} ...${b('dns.fake_ip_filter')} placeholder="+.lan" /><//>
            <${Field} label="Fake-IP 过滤模式" wide>
                <${Select} ...${b('dns.fake_ip_filter_mode')} options=${[['', '不修改'], ['blacklist', '黑名单模式'], ['whitelist', '白名单模式'], ['rule', '规则模式']]} />
            <//>
            <${Field} label="Fake-IP 缓存" desc="内核重启后保留 Fake-IP 映射">${onOff(b('store_fake_ip'), 'Fake-IP 缓存')}<//>
            <${Field} label="遵循分流规则" desc="DNS 查询本身也按规则走代理或直连">${onOff(b('dns.respect_rules'), '遵循分流规则')}<//>

            <${Field} label="覆盖 DNS 服务器" desc="关着时用订阅里的上游">${onOff(b('dns.override_nameservers'), '覆盖 DNS 服务器')}<//>
            ${v('dns.override_nameservers') && html`
                <${Field} label="default-nameserver" desc="解析其它 DNS 服务器域名用，填 IP" wide top><${ListInput} ...${b('dns.default_nameserver')} rows=${2} placeholder="223.5.5.5" /><//>
                <${Field} label="nameserver" desc="缺省上游" wide top><${ListInput} ...${b('dns.nameserver')} rows=${2} placeholder="https://223.5.5.5/dns-query" /><//>
                <${Field} label="proxy-server-nameserver" desc="解析节点域名用" wide top><${ListInput} ...${b('dns.proxy_server_nameserver')} rows=${2} /><//>
                <${Field} label="direct-nameserver" desc="直连的域名用" wide top><${ListInput} ...${b('dns.direct_nameserver')} rows=${2} /><//>
                <${Field} label="fallback" wide top><${ListInput} ...${b('dns.fallback')} rows=${2} /><//>`}

            <${Field} label="覆盖 DNS 服务器查询策略" desc="nameserver-policy：指定域名用指定上游">${onOff(b('dns.override_policy'), '覆盖查询策略')}<//>
            ${v('dns.override_policy') && html`<${Rows} ...${b('dns.nameserver_policy')} addLabel="添加策略"
                make=${() => ({ enabled: true, matcher: '', nameserver: [] })}
                render=${(p, set) => html`
                    <${Switch} label="启用" checked=${p.enabled} onChange=${(on) => set({ enabled: on })} />
                    <input class="input grow mono" placeholder="匹配，如 geosite:cn" spellcheck="false" value=${p.matcher} onInput=${(e) => set({ matcher: e.currentTarget.value })} />
                    <span class="grow2"><${CsvInput} value=${p.nameserver} onChange=${(list) => set({ nameserver: list })} placeholder="DNS 服务器，逗号分隔" /></span>`} />`}

            <div class="section-title">Hosts</div>
            <${Rows} ...${b('dns.hosts')} addLabel="添加 Hosts" empty="没有 Hosts 时用订阅里的。"
                make=${() => ({ enabled: true, domain: '', ip: [] })}
                render=${(h, set) => html`
                    <${Switch} label="启用" checked=${h.enabled} onChange=${(on) => set({ enabled: on })} />
                    <input class="input grow mono" placeholder="域名" spellcheck="false" value=${h.domain} onInput=${(e) => set({ domain: e.currentTarget.value })} />
                    <span class="grow"><${CsvInput} value=${h.ip} onChange=${(list) => set({ ip: list })} placeholder="IP，逗号分隔" /></span>`} />
        <//>

        <${Card} title="嗅探" i=${4}>
            <${Field} label="启用" desc="从 TLS / HTTP 里读出域名，按域名分流">${onOff(b('sniffer.enabled'), '启用嗅探')}<//>
            <${Field} label="强制嗅探的域名" desc="一行一个" wide top><${ListInput} ...${b('sniffer.force_domain')} rows=${2} /><//>
            <${Field} label="忽略嗅探的域名" desc="一行一个" wide top><${ListInput} ...${b('sniffer.skip_domain')} rows=${2} /><//>
        <//>

        <${Card} title="自定义规则" desc="一行一条，排在订阅的规则前面（阻止 QUIC 与 po0 直连之后）。写法同 mihomo：类型,匹配,策略。" i=${5}>
            <${ListInput} ...${b('rules')} rows=${4} placeholder=${'DOMAIN-SUFFIX,example.com,DIRECT\nIP-CIDR,192.168.50.0/24,DIRECT,no-resolve'} />
        <//>

        <${Card} title="额外混入" desc="一段 YAML，最后合并进运行配置（同 OpenWrt 版的混入文件）。meowx-proxies / meowx-proxy-groups / meowx-rules 三个键插到对应列表最前面。" i=${6}>
            <textarea class="textarea" rows="10" spellcheck="false" placeholder=${'# 例如\nmeowx-rules:\n  - DOMAIN,example.com,DIRECT'}
                value=${v('extra') ?? ''} onInput=${(e) => form.set('extra', e.currentTarget.value)}></textarea>
        <//>

        <${SaveBar} form=${form} onSave=${save} />
    </div>`;
}
