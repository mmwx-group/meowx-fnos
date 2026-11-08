// 设置 / 关于：版本、网页访问密码、订阅自动更新间隔、启用前检查配置
import { html, useState, useEffect } from '../lib.js';
import { get, post } from '../api.js';
import { useStore, update, saveSettings, login, refreshStatus, refreshRelease, toast } from '../store.js';
import { Card, Field, Switch, NumberInput, Button, SaveBar, Logo, useSettingsForm } from '../ui.js';
import { stripPeriod } from '../util.js';

// 版本卡里的「检查更新 / 立即更新」：一键更新经飞牛应用中心原地升级（保留设置与订阅），用不了时给安装包下载地址
function UpdateRow() {
    const { release, upgrading } = useStore();
    const [, tick] = useState(0);
    useEffect(() => {
        if (!upgrading) {
            return;
        }
        const t = setInterval(() => tick((n) => n + 1), 10000);
        return () => clearInterval(t);
    }, [upgrading]);
    const check = async () => {
        const r = await refreshRelease(true);
        if (r && !r.error && !r.has_update) {
            toast('已是最新版本');
        }
    };
    const install = async () => {
        if (!confirm(`更新到 ${release.latest}？更新时 MeowX 会重启一次，代理与喵窝中断约一两分钟，设置与订阅都保留。`)) {
            return;
        }
        try {
            const res = await post('/update');
            update({ upgrading: { version: res.version, at: Date.now() } });
        } catch (err) {
            if (err.status === 0) {
                // 请求被重启打断：多半已经在更新
                update({ upgrading: { version: release.latest, at: Date.now() } });
                return;
            }
            toast(`更新失败：${stripPeriod(err.message)}`, 'warn');
            refreshRelease();
        }
    };
    if (upgrading) {
        const slow = Date.now() - upgrading.at > 10 * 60 * 1000;
        return html`<p class="hint">正在更新到 ${upgrading.version}：飞牛应用中心会停掉 MeowX、换上新版再启动，完成后页面自动刷新。</p>
            ${slow && html`<p class="error-text">等了很久还没更新好，请到飞牛「应用中心」看看 MeowX 的状态。</p>`}`;
    }
    if (!release) {
        return html`<p class="hint">正在检查更新…</p>`;
    }
    const last = release.last_error && html`<p class="error-text">上次一键更新失败：${stripPeriod(release.last_error)}</p>`;
    if (release.error) {
        return html`<div class="inline can-wrap"><span class="error-text">检查更新失败：${stripPeriod(release.error)}</span>
            <${Button} small onClick=${check}>重新检查<//></div>${last}`;
    }
    if (!release.has_update) {
        return html`<div class="inline can-wrap"><span class="muted">已是最新版本</span><${Button} small onClick=${check}>检查更新<//></div>${last}`;
    }
    return html`<div class="inline can-wrap">
            <span>新版本 <b>${release.latest}</b>${release.notes_url && html` · <a href=${release.notes_url} target="_blank" rel="noopener">更新内容</a>`}</span>
            ${release.can_install
            ? html`<${Button} small kind="primary" onClick=${install}>立即更新<//>`
            : release.download_url && html`<a class="btn small" href=${release.download_url} target="_blank" rel="noopener">下载安装包</a>`}
        </div>
        ${!release.can_install && release.reason && html`<p class="hint">${stripPeriod(release.reason)}。</p>`}
        ${last}`;
}

function PasswordCard() {
    const [has, setHas] = useState(null);
    const [pw, setPw] = useState('');
    const [pw2, setPw2] = useState('');
    const [error, setError] = useState('');
    useEffect(() => { get('/settings').then((s) => setHas(!!s.web?.password), () => { }); }, []);

    // 改了密码，旧会话随之失效：用新密码顺手登录一次，免得马上被请回登录框
    const apply = async (next) => {
        await saveSettings((s) => { s.web = { ...(s.web ?? {}), password: next }; }, false);
        if (next) {
            await login(next);
        }
        setHas(!!next);
        setPw('');
        setPw2('');
        await refreshStatus();
    };
    const set = async () => {
        if (!pw) {
            setError('请输入新密码');
            return;
        }
        if (pw != pw2) {
            setError('两次输入的密码不一样');
            return;
        }
        setError('');
        await apply(pw);
        toast('已设置访问密码');
    };
    const clear = async () => {
        if (!confirm('清除访问密码？之后能访问这个端口的人都能打开管理页面。')) {
            return;
        }
        await apply('');
        toast('已清除访问密码');
    };
    return html`<${Card} title="网页访问密码" i=${1}
        desc="设置后打开管理页面要先输入密码。飞牛自己的登录管不到这个端口：NAS 的端口开放到外网时一定要设。">
        <${Field} label="当前">${has == null ? '…' : has ? html`<span class="badge">已设置</span>` : html`<span class="badge is-muted">没有设置</span>`}<//>
        <form class="inline can-wrap" onSubmit=${(e) => e.preventDefault()}>
            <input class="input" type="password" autocomplete="new-password" placeholder=${has ? '新密码' : '设置密码'} value=${pw} onInput=${(e) => setPw(e.currentTarget.value)} />
            <input class="input" type="password" autocomplete="new-password" placeholder="再输一次" value=${pw2} onInput=${(e) => setPw2(e.currentTarget.value)} />
            <${Button} kind="primary" onClick=${set}>${has ? '修改' : '设置'}<//>
        </form>
        ${error && html`<p class="error-text">${error}</p>`}
        ${has && html`<p class="hint"><${Button} small kind="danger" onClick=${clear}>清除密码<//></p>`}
    <//>`;
}

export function SettingsPage({ active }) {
    const { status } = useStore();
    const [coreVersion, setCoreVersion] = useState('');
    const running = !!status?.core?.running;
    useEffect(() => {
        if (active && running) {
            get('/core/version').then((v) => setCoreVersion(v?.version || '未标版本'), () => { });
        }
    }, [active, running]);

    const form = useSettingsForm('settings',
        (s) => ({ auto_update_hours: s.auto_update_hours, test_profile: s.test_profile }),
        (s, v) => { s.auto_update_hours = v.auto_update_hours; s.test_profile = v.test_profile; },
        active);
    const save = async () => {
        await form.save(false);
        toast('已保存');
    };

    return html`<div>
        <${Card} i=${0}>
            <div class="account">
                <${Logo} />
                <div class="account__main">
                    <div class="account__name">MeowX 飞牛 NAS 版</div>
                    <div class="account__host mono">控制服务 ${status?.version || '-'} · 内核 ${coreVersion || (running ? '…' : '没在运行')}</div>
                </div>
            </div>
            <div style="margin-top:12px"><${UpdateRow} /></div>
        <//>
        <${Card} title="订阅与配置" i=${1}>
            ${!form.draft
            ? html`<p class=${form.error ? 'error-text' : 'muted'}>${form.error || '正在载入…'}</p>`
            : html`
                <${Field} label="订阅自动更新间隔" desc="小时。订阅自己建议了更短的间隔时按订阅的；0 = 只按订阅建议的间隔（没建议就不自动更新）" wide>
                    <${NumberInput} ...${form.bind('auto_update_hours')} placeholder="0" />
                <//>
                <${Field} label="检查配置文件" desc="启用新订阅或新设置前先让内核试读一遍，没通过就不换上去，运行中的保持不变">
                    <${Switch} label="检查配置文件" checked=${form.value('test_profile')} onChange=${form.bind('test_profile').onChange} />
                <//>`}
        <//>
        <${PasswordCard} />
        <${Card} title="第三方组件" i=${3}>
            <p class="hint" style="margin:0">界面用到 <a href="vendor/LICENSE-preact.txt" target="_blank">Preact</a>（MIT）与 <a href="vendor/LICENSE-htm.txt" target="_blank">htm</a>（Apache-2.0）。</p>
        <//>
        ${form.draft && html`<${SaveBar} form=${form} onSave=${save} label="保存" />`}
    </div>`;
}
