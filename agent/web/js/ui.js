// 公共元件：卡片、表单行、开关、分段、按钮（自带转圈）、列表输入、弹窗、提示
import { html, render, useState, useEffect, useRef } from './lib.js';
import { useStore, dismissToast, toast, setDirty, update } from './store.js';
import { get, put } from './api.js';
import { clone, same, getIn, setIn, merge3, formatBytes, relativeTime } from './util.js';

// 品牌图：浅 / 深两张，跟随系统深浅色
export function Logo() {
    return html`<span class="logo"><picture>
        <source srcset="img/logo-dark.jpg" media="(prefers-color-scheme: dark)" />
        <img src="img/logo-light.jpg" alt="" />
    </picture></span>`;
}

export function Card({ title, desc, actions, children, class: cls = '', i }) {
    return html`<section class=${'card enter ' + cls} style=${i != null ? `--i:${i}` : ''}>
        ${(title || actions) && html`<div class="card__head">
            <div class="card__titles">
                ${title && html`<h2 class="card__title">${title}</h2>`}
                ${desc && html`<p class="card__desc">${desc}</p>`}
            </div>
            ${actions && html`<div class="card__actions">${actions}</div>`}
        </div>`}
        ${children}
    </section>`;
}

// 表单行：左边标题 + 说明，右边控件。wide = 输入框类（窄屏换到下一行占满）
export function Field({ label, desc, wide, top, children }) {
    return html`<div class=${'field' + (top ? ' is-top' : '')}>
        <div class="field__label">
            <div class="field__title">${label}</div>
            ${desc && html`<div class="field__desc">${desc}</div>`}
        </div>
        <div class=${'field__control' + (wide ? ' is-wide' : '')}>${children}</div>
    </div>`;
}

export function Switch({ checked, onChange, disabled, label }) {
    return html`<input type="checkbox" class="switch" role="switch" aria-label=${label}
        checked=${!!checked} disabled=${disabled} onChange=${(e) => onChange(e.currentTarget.checked)} />`;
}

export function TextInput({ value, onChange, placeholder, type = 'text', mono, ...rest }) {
    return html`<input class=${'input' + (mono ? ' mono' : '')} type=${type} value=${value ?? ''} placeholder=${placeholder}
        spellcheck="false" autocomplete="off" onInput=${(e) => onChange(e.currentTarget.value)} ...${rest} />`;
}

// 数字：空 = 0（设置里 0 表示不改 / 不开）
export function NumberInput({ value, onChange, placeholder, min = 0, max }) {
    return html`<input class="input" type="number" inputmode="numeric" min=${min} max=${max}
        value=${value ? value : ''} placeholder=${placeholder}
        onInput=${(e) => { const n = parseInt(e.currentTarget.value, 10); onChange(isFinite(n) ? n : 0); }} />`;
}

export function Select({ value, onChange, options }) {
    return html`<select class="select" value=${value ?? ''} onChange=${(e) => onChange(e.currentTarget.value)}>
        ${options.map(([v, label]) => html`<option value=${v}>${label}</option>`)}
    </select>`;
}

// 列表：一行一项。正在输入的文本自己留着（不然敲回车新起一行就被整理掉了），外面的值变了（还原、重新载入）才换
export function ListInput({ value, onChange, placeholder, rows = 3 }) {
    const list = value ?? [];
    const [text, setText] = useState(list.join('\n'));
    const parse = (t) => t.split('\n').map((s) => s.trim()).filter(Boolean);
    useEffect(() => {
        if (!same(parse(text), list)) {
            setText(list.join('\n'));
        }
    }, [JSON.stringify(list)]);
    return html`<textarea class="textarea" rows=${Math.max(rows, Math.min(12, list.length + 1))} placeholder=${placeholder}
        spellcheck="false" value=${text}
        onInput=${(e) => { const t = e.currentTarget.value; setText(t); onChange(parse(t)); }}></textarea>`;
}

// 分段控件。options：[[值, 标题], …]
export function Segmented({ value, onChange, options, full, disabled }) {
    return html`<div class=${'seg' + (full ? ' is-full' : '')} role="radiogroup">
        ${options.map(([v, label]) => html`<button type="button" role="radio" aria-checked=${v == value}
            class=${'seg__item' + (v == value ? ' is-on' : '')} disabled=${disabled}
            onClick=${() => v != value && onChange(v)}>${label}</button>`)}
    </div>`;
}

// 按钮：onClick 返回 Promise 时转圈、禁止重复点；出错弹提示（错误已经提示过的抛 null）
export function Button({ onClick, kind = '', small, disabled, children, title, type = 'button' }) {
    const [busy, setBusy] = useState(false);
    const alive = useRef(true);
    useEffect(() => () => { alive.current = false; }, []);
    const click = async (e) => {
        if (busy || !onClick) {
            return;
        }
        const r = onClick(e);
        if (!r || typeof r.then != 'function') {
            return;
        }
        setBusy(true);
        try {
            await r;
        } catch (err) {
            if (err) {
                toast(err.message || String(err), 'warn');
            }
        } finally {
            if (alive.current) {
                setBusy(false);
            }
        }
    };
    return html`<button type=${type} title=${title} class=${['btn', kind, small && 'small', busy && 'is-busy'].filter(Boolean).join(' ')}
        disabled=${disabled} onClick=${click}>${children}</button>`;
}

// 渲染到 <body> 下单独的节点：弹窗不受所在卡片的变换 / 裁切影响
function Portal({ children }) {
    const host = useRef(null);
    if (!host.current) {
        host.current = document.createElement('div');
        document.body.appendChild(host.current);
    }
    const focused = useRef(false);
    useEffect(() => {
        render(children, host.current);
        // 动态插入的元素 autofocus 不一定生效：第一次画出来时手动聚焦
        if (!focused.current) {
            focused.current = true;
            host.current.querySelector('[autofocus]')?.focus();
        }
    });
    useEffect(() => () => {
        render(null, host.current);
        host.current.remove();
    }, []);
    return null;
}

export function Dialog({ title, onClose, children, actions }) {
    useEffect(() => {
        const key = (e) => e.key == 'Escape' && onClose();
        document.addEventListener('keydown', key);
        return () => document.removeEventListener('keydown', key);
    }, [onClose]);
    return html`<${Portal}><div class="overlay" onClick=${(e) => e.target == e.currentTarget && onClose()}>
        <div class="dialog" role="dialog" aria-modal="true">
            <h2 class="dialog__title">${title}</h2>
            ${children}
            <div class="dialog__actions">${actions}</div>
        </div>
    </div><//>`;
}

export function Toasts() {
    const { toasts } = useStore();
    return html`<div class="toasts" aria-live="polite">
        ${toasts.map((t) => html`<div key=${t.id} class=${'toast' + (t.kind == 'warn' ? ' is-warn' : '')}>
            <span class="toast__text">${t.text}</span>
            <button type="button" class="close-x" aria-label="关闭" onClick=${() => dismissToast(t.id)}>×</button>
        </div>`)}
    </div>`;
}

// 状态胶囊：运行中 = 绿点 + 涟漪
export function StatusPill({ status }) {
    const s = describeStatus(status);
    return html`<span class=${'status ' + s.cls}><span class="status__dot"></span><span>${s.text}</span></span>`;
}

export function describeStatus(status) {
    if (!status) {
        return { text: '连接中…', cls: '' };
    }
    if (status.core?.running) {
        return { text: '运行中', cls: 'is-running' };
    }
    if (status.enabled) {
        return { text: status.error || status.core?.error ? '没在运行' : '正在启动', cls: status.error || status.core?.error ? 'is-error' : '' };
    }
    return { text: '已停止', cls: '' };
}

// 设置表单（混入 / 透明代理 / 设置页共用）：pick 取出这页管的那部分，place 放回整份设置。
// 表单载入时记一份 base，保存时与刚取的最新整份三方合并：别的页签同时改的（如概览里切了出站模式）不会被这页的旧值盖回去
export function useSettingsForm(page, pick, place, active) {
    const [base, setBase] = useState(null);
    const [draft, setDraft] = useState(null);
    const [error, setError] = useState('');
    const dirty = base != null && !same(base, draft);

    const load = async () => {
        try {
            const s = await get('/settings');
            const part = pick(s);
            setBase(clone(part));
            setDraft(clone(part));
            setError('');
        } catch (err) {
            setError(err.message);
        }
    };
    // 切回这一页时：没改过就重新载入（别处可能改了设置）
    useEffect(() => {
        if (active && !dirty) {
            load();
        }
    }, [active]);
    useEffect(() => setDirty(page, dirty), [dirty]);
    useEffect(() => () => setDirty(page, false), []);

    return {
        draft, dirty, error,
        value: (path) => getIn(draft, path),
        set: (path, v) => setDraft((d) => setIn(d, path, v)),
        bind: (path) => ({ value: getIn(draft, path), onChange: (v) => setDraft((d) => setIn(d, path, v)) }),
        reset: () => setDraft(clone(base)),
        reload: load,
        // 保存；apply = false 只存不重启内核。返回 PUT 的回包
        save: async (apply = true) => {
            const latest = await get('/settings');
            const merged = merge3(pick(latest), base, draft);
            place(latest, merged);
            const res = await put('/settings' + (apply ? '' : '?apply=0'), latest);
            const part = pick(res.settings);
            setBase(clone(part));
            setDraft(clone(part));
            if (res.trial) {
                update({ trial: res.trial });
            }
            return res;
        },
    };
}

// 底部浮起的保存条：有改动才出现
export function SaveBar({ form, onSave, label = '保存并应用' }) {
    if (!form.dirty) {
        return null;
    }
    return html`<div class="savebar">
        <span class="savebar__text">有没保存的修改</span>
        <${Button} onClick=${form.reset}>还原<//>
        <${Button} kind="primary" onClick=${onSave}>${label}<//>
    </div>`;
}

// 用量条 + 剩余 / 到期 + 「更新于 …」（同客户端首页订阅条）。onRefresh 为空 = 本地文件，不能刷新
export function Usage({ profile: p, onRefresh }) {
    const [busy, setBusy] = useState(false);
    const used = +p.used || 0, total = +p.total || 0;
    const ratio = total > 0 ? Math.min(1, Math.max(0, used / total)) : 0;
    const parts = [];
    if (total > 0) {
        parts.push(`剩余 ${formatBytes(Math.max(0, total - used))}`);
    } else if (p.url && p.updated_at) {
        parts.push('无限制');
    }
    if (+p.expire > 0) {
        const days = Math.floor((+p.expire - Date.now() / 1000) / 86400);
        parts.push(days < 0 ? '已过期' : `${days} 天后到期`);
    }
    const failed = !!p.error;
    let updatedText = p.updated_at ? `更新于 ${relativeTime(p.updated_at)}` : '未更新';
    if (failed) {
        updatedText = '更新失败';
    }
    if (!p.url) {
        updatedText = p.updated_at ? `上传于 ${relativeTime(p.updated_at)}` : '本地文件';
    }
    const refresh = async () => {
        if (!onRefresh || busy) {
            return;
        }
        setBusy(true);
        try {
            await onRefresh();
        } catch (err) {
            toast(err.message, 'warn');
        } finally {
            setBusy(false);
        }
    };
    return html`<div class="usage">
        ${total > 0 && html`<div class="usage__bar"><span style=${`transform: scaleX(${ratio})`}></span></div>`}
        <div class="usage__line">
            ${(total > 0 || used > 0) && html`<span class="usage__amount">${total > 0 ? `${formatBytes(used)} / ${formatBytes(total)}` : formatBytes(used)}</span>`}
            ${parts.length > 0 && html`<span class="usage__meta">${parts.join(' · ')}</span>`}
            <button type="button" class=${['updated', failed && 'is-failed', busy && 'is-busy', !onRefresh && 'is-static'].filter(Boolean).join(' ')}
                title=${failed ? p.error : onRefresh ? '更新' : ''} disabled=${!onRefresh} onClick=${refresh}>${updatedText}</button>
        </div>
    </div>`;
}
