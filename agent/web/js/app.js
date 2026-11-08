// 外框：顶栏、页签（地址里的 #锚点 记当前页，后退键与刷新都停在原页）、登录框、全局横条
import { html, render, useState, useEffect, useRef, useLayoutEffect } from './lib.js';
import { setUnauthorizedHandler, post } from './api.js';
import { useStore, update, refreshStatus, refreshProfiles, refreshPanel, refreshRelease, login, toast } from './store.js';
import { Button, Toasts, StatusPill, Logo } from './ui.js';
import { Overview } from './pages/overview.js';
import { Profiles } from './pages/profiles.js';
import { Nodes } from './pages/nodes.js';
import { Mixin } from './pages/mixin.js';
import { ProxyPage } from './pages/proxy.js';
import { Mesh } from './pages/mesh.js';
import { Connections } from './pages/connections.js';
import { Logs } from './pages/logs.js';
import { Extras } from './pages/extras.js';
import { SettingsPage } from './pages/settings.js';

const pages = [
    { id: 'overview', title: '概览', Page: Overview },
    { id: 'profiles', title: '订阅', Page: Profiles },
    { id: 'nodes', title: '节点', Page: Nodes },
    { id: 'mixin', title: '混入设置', Page: Mixin },
    { id: 'proxy', title: '透明代理', Page: ProxyPage },
    { id: 'mesh', title: '喵窝', Page: Mesh },
    { id: 'connections', title: '连接', Page: Connections },
    { id: 'logs', title: '日志', Page: Logs },
    { id: 'extras', title: '辅助功能', Page: Extras },
    { id: 'settings', title: '设置', Page: SettingsPage },
];

const pageFromHash = () => {
    const id = location.hash.replace(/^#/, '');
    return pages.some((p) => p.id == id) ? id : pages[0].id;
};

function Tabs({ current, onPick }) {
    const bar = useRef();
    const thumb = useRef();
    const first = useRef(true);
    // 选中块滑到当前页签下面（客户端分段控件的滑块）；窄屏页签单行滑动，当前页签滚到中间
    const place = (animate) => {
        const el = bar.current?.querySelector('.tabs__item.is-active');
        if (!el || !thumb.current) {
            return;
        }
        thumb.current.classList.toggle('no-anim', !animate);
        thumb.current.style.width = el.offsetWidth + 'px';
        thumb.current.style.transform = `translateX(${el.offsetLeft}px)`;
        if (bar.current.scrollWidth > bar.current.clientWidth) {
            bar.current.scrollTo({ left: el.offsetLeft - (bar.current.clientWidth - el.offsetWidth) / 2, behavior: animate ? 'smooth' : 'auto' });
        }
    };
    useLayoutEffect(() => {
        place(!first.current);
        first.current = false;
    }, [current]);
    useEffect(() => {
        const onResize = () => place(false);
        window.addEventListener('resize', onResize);
        document.fonts?.ready.then(onResize);
        return () => window.removeEventListener('resize', onResize);
    }, []);
    return html`<nav class="tabs" role="tablist" ref=${bar}>
        <span class="tabs__thumb" ref=${thumb} aria-hidden="true"></span>
        ${pages.map((p) => html`<button type="button" role="tab" aria-selected=${p.id == current}
            class=${'tabs__item' + (p.id == current ? ' is-active' : '')} onClick=${() => onPick(p.id)}>${p.title}</button>`)}
    </nav>`;
}

// 透明代理试用期：醒目的倒计时 +「保留设置」（到点没点，控制服务自动关掉透明代理）
function TrialBanner({ trial }) {
    const [now, setNow] = useState(Date.now());
    useEffect(() => {
        if (!trial?.pending) {
            return;
        }
        const t = setInterval(() => setNow(Date.now()), 1000);
        return () => clearInterval(t);
    }, [trial?.pending]);
    if (!trial?.pending) {
        return null;
    }
    const left = Math.max(0, Math.round(trial.deadline - now / 1000));
    const mmss = `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}`;
    const keep = async () => {
        await post('/proxy/keep');
        update({ trial: { pending: false } });
        toast('已保留透明代理设置');
    };
    return html`<div class="banner is-warn" role="alert">
        <span class="banner__text">${left > 0
            ? html`透明代理刚打开或改过：确认 NAS 与局域网都能正常上网后点「保留设置」，<span class="banner__count">${mmss}</span> 后没点会自动关掉透明代理。`
            : '到时间了，正在自动关掉透明代理…'}</span>
        ${left > 0 && html`<${Button} kind="primary" onClick=${keep}>保留设置<//>`}
    </div>`;
}

function LoginScreen() {
    const [password, setPassword] = useState('');
    const [error, setError] = useState('');
    // 回车在表单里会「点」提交按钮，只由按钮的 onClick 登录一次
    const submit = async () => {
        setError('');
        try {
            await login(password);
            await refreshStatus();
        } catch (err) {
            setError(err.message);
        }
    };
    return html`<div class="login">
        <div class="card">
            <${Logo} />
            <div class="hero__title">MeowX</div>
            <p class="hint">管理页面设了访问密码，请先输入。</p>
            <form class="inline" onSubmit=${(e) => e.preventDefault()}>
                <input class="input" type="password" placeholder="访问密码" autocomplete="current-password" autofocus
                    value=${password} onInput=${(e) => setPassword(e.currentTarget.value)} />
                <${Button} kind="primary" type="submit" onClick=${submit}>登录<//>
            </form>
            ${error && html`<p class="error-text">${error}</p>`}
        </div>
    </div>`;
}

function App() {
    const store = useStore();
    const [current, setCurrent] = useState(pageFromHash());
    const [visited, setVisited] = useState(() => new Set([pageFromHash()]));
    const [anim, setAnim] = useState('');

    const pick = (id, push = true) => {
        if (id == current) {
            return;
        }
        const order = (x) => pages.findIndex((p) => p.id == x);
        setAnim(order(id) > order(current) ? 'pane--from-right' : 'pane--from-left');
        setCurrent(id);
        setVisited((v) => new Set(v).add(id));
        window.scrollTo({ top: 0 });
        if (push) {
            history.pushState(null, '', '#' + id);
        }
    };

    useEffect(() => {
        const onHash = () => pick(pageFromHash(), false);
        window.addEventListener('hashchange', onHash);
        window.addEventListener('popstate', onHash);
        return () => {
            window.removeEventListener('hashchange', onHash);
            window.removeEventListener('popstate', onHash);
        };
    });

    // 运行状态每 3 秒拿一次（页面在后台不拿，回到前台立刻拿）
    useEffect(() => {
        setUnauthorizedHandler(() => update({ needLogin: true }));
        refreshStatus();
        const t = setInterval(() => !document.hidden && refreshStatus(), 3000);
        const onVisible = () => !document.hidden && refreshStatus();
        document.addEventListener('visibilitychange', onVisible);
        // 有没保存的修改时关页提醒
        const onLeave = (e) => {
            if (store.dirty.size > 0) {
                e.preventDefault();
                e.returnValue = '';
            }
        };
        window.addEventListener('beforeunload', onLeave);
        return () => {
            clearInterval(t);
            document.removeEventListener('visibilitychange', onVisible);
            window.removeEventListener('beforeunload', onLeave);
        };
    }, []);

    // 登录后（或不需要登录）载入订阅与主控账户，几页共用
    const ready = store.status && !store.needLogin;
    useEffect(() => {
        if (ready) {
            refreshProfiles().catch(() => { });
            refreshPanel();
            refreshRelease();
        }
    }, [ready]);

    // 一键更新后应用中心会重启 MeowX：控制服务的版本号变成新版就刷新页面，换上新版的网页
    const version = store.status?.version;
    useEffect(() => {
        if (store.upgrading && version == store.upgrading.version) {
            location.reload();
        }
    }, [version, store.upgrading]);

    if (store.needLogin) {
        return html`<${LoginScreen} /><${Toasts} />`;
    }
    if (!store.status) {
        return html`<div class="boot">${store.offline ? '连不上 MeowX 控制服务，正在重试…' : 'MeowX 正在加载…'}</div>`;
    }

    return html`<div class="app">
        <header class="top">
            <${Logo} />
            <div>
                <div class="top__title">MeowX</div>
                <div class="top__sub">飞牛 NAS 版${store.status.version ? ' · ' + store.status.version : ''}${store.release?.has_update
            && html` · <a href="#settings">有新版本 ${store.release.latest}</a>`}</div>
            </div>
            <div class="top__status"><${StatusPill} status=${store.status} /></div>
        </header>
        <div class="tabs-bar">
            <${Tabs} current=${current} onPick=${pick} />
            ${store.offline && html`<div class="banner is-error" style="margin:10px 0 0">连不上 MeowX 控制服务（容器可能在重启），正在重试…</div>`}
            ${store.trial?.pending && html`<div style="margin-top:10px"><${TrialBanner} trial=${store.trial} /></div>`}
        </div>
        ${pages.filter((p) => visited.has(p.id)).map((p) => html`<main key=${p.id} hidden=${p.id != current}
            class=${p.id == current ? anim : ''} onAnimationEnd=${(e) => e.target == e.currentTarget && setAnim('')}>
            <${p.Page} active=${p.id == current} go=${pick} />
        </main>`)}
    </div>
    <${Toasts} />`;
}

render(html`<${App} />`, document.getElementById('root'));
