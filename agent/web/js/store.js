// 几个页签共用的状态：运行状态、订阅列表、主控账户、提示条。各页自己的表单状态留在页里
import { useState, useEffect } from './lib.js';
import { get, put, post } from './api.js';

export const state = {
    status: null,      // GET /api/status
    offline: false,    // 连不上控制服务
    needLogin: false,  // 设了网页密码、还没登录
    profiles: null,    // { profiles: [], active }
    panel: null,       // 主控账户状态（panel status 原样）
    toasts: [],
    trial: null,       // 透明代理试用：{ pending, deadline(秒) }，到点没点「保留设置」自动关
    dirty: new Set(),  // 有没保存修改的页签
    release: null,     // GET /api/update：有没有新版本
    upgrading: null,   // 一键更新已提交：{ version, at(毫秒) }，控制服务的版本号变成它就刷新页面
};

const listeners = new Set();

export function update(patch) {
    Object.assign(state, patch);
    listeners.forEach((fn) => fn());
}

export function useStore() {
    const [, force] = useState(0);
    useEffect(() => {
        const fn = () => force((n) => n + 1);
        listeners.add(fn);
        return () => listeners.delete(fn);
    }, []);
    return state;
}

export async function refreshStatus() {
    try {
        const status = await get('/status');
        const patch = { status, offline: false, needLogin: !!status.need_login };
        if (!status.need_login) {
            patch.trial = await get('/proxy').catch(() => state.trial);
        }
        update(patch);
        return status;
    } catch (err) {
        if (err.status === 0) {
            update({ offline: true });
        }
        return null;
    }
}

export async function refreshProfiles() {
    const res = await get('/profiles');
    update({ profiles: { profiles: res?.profiles ?? [], active: res?.active ?? '' } });
    return state.profiles;
}

// 检查新版本（控制服务缓存一小时，force = 重新取）
export async function refreshRelease(force = false) {
    try {
        update({ release: await get('/update' + (force ? '?refresh=1' : '')) });
    } catch (err) {
        update({ release: { error: err.message } });
    }
    return state.release;
}

export async function refreshPanel() {
    try {
        update({ panel: await get('/panel') });
    } catch (err) {
        update({ panel: { logged_in: false, error: err.message } });
    }
    return state.panel;
}

// ---------- 提示 ----------

let toastSeq = 0;

// kind：'ok'（缺省）/ 'warn'。warn 留得久一点、可手动关
export function toast(text, kind = 'ok') {
    const id = ++toastSeq;
    update({ toasts: [...state.toasts, { id, text, kind }] });
    setTimeout(() => dismissToast(id), kind == 'warn' ? 9000 : 3500);
}

export function dismissToast(id) {
    if (state.toasts.some((t) => t.id == id)) {
        update({ toasts: state.toasts.filter((t) => t.id != id) });
    }
}

// ---------- 设置 ----------

// 改一处设置：先取最新的整份、改完整份写回（PUT 是整份替换）。apply = false 只存不重启内核
export async function saveSettings(mutate, apply = true) {
    const settings = await get('/settings');
    mutate(settings);
    return put('/settings' + (apply ? '' : '?apply=0'), settings);
}

export function setDirty(page, dirty) {
    const has = state.dirty.has(page);
    if (dirty == has) {
        return;
    }
    const next = new Set(state.dirty);
    dirty ? next.add(page) : next.delete(page);
    update({ dirty: next });
}

// 网页密码：登录拿会话 Cookie
export function login(password) {
    return post('/login', { password });
}
