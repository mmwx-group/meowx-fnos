// 公共小函数（同 OpenWrt 版 tools/meowx.js 里的格式化与订阅判断）

export function formatBytes(n) {
    n = +n;
    if (!isFinite(n) || n < 0) {
        return '';
    }
    const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    let i = 0;
    while (n >= 1024 && i < units.length - 1) {
        n /= 1024;
        i++;
    }
    return (i == 0 || n >= 100 ? Math.round(n) : n.toFixed(1)) + ' ' + units[i];
}

export function formatSpeed(n) {
    return formatBytes(n) + '/s';
}

// ts：秒
export function relativeTime(ts) {
    const diff = Math.max(0, Date.now() / 1000 - (+ts));
    if (diff < 60) {
        return '刚刚';
    }
    if (diff < 3600) {
        return `${Math.floor(diff / 60)} 分钟前`;
    }
    if (diff < 86400) {
        return `${Math.floor(diff / 3600)} 小时前`;
    }
    return `${Math.floor(diff / 86400)} 天前`;
}

// 时长（秒）→「3 天 4 小时」「12 分钟」
export function formatDuration(sec) {
    sec = Math.max(0, Math.floor(sec));
    const d = Math.floor(sec / 86400), h = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60);
    if (d > 0) {
        return h > 0 ? `${d} 天 ${h} 小时` : `${d} 天`;
    }
    if (h > 0) {
        return m > 0 ? `${h} 小时 ${m} 分钟` : `${h} 小时`;
    }
    if (m > 0) {
        return `${m} 分钟`;
    }
    return `${sec} 秒`;
}

export function clockTime(ms) {
    const d = new Date(ms);
    const p = (n) => String(n).padStart(2, '0');
    return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

// 后端给的原因句末常带句号，套进句子前去掉，免得出现「…。。」
export function stripPeriod(text) {
    return String(text ?? '').replace(/[.。]\s*$/, '');
}

export const clone = (v) => (v === undefined ? undefined : JSON.parse(JSON.stringify(v)));
export const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

export function getIn(obj, path) {
    return path.split('.').reduce((o, k) => (o == null ? undefined : o[k]), obj);
}

// 不可变地改一处：返回新对象，沿路复制
export function setIn(obj, path, value) {
    const keys = path.split('.');
    const root = Array.isArray(obj) ? obj.slice() : { ...obj };
    let cur = root;
    for (let i = 0; i < keys.length - 1; i++) {
        const next = cur[keys[i]];
        cur[keys[i]] = Array.isArray(next) ? next.slice() : { ...(next ?? {}) };
        cur = cur[keys[i]];
    }
    cur[keys[keys.length - 1]] = value;
    return root;
}

// 三方合并：base 是表单载入时的那份，mine 是表单里改过的，server 是保存前刚取的最新一份。
// 表单里没动过的地方用 server 的（别的页签同时改过的不会被这页的旧值盖回去），动过的用 mine 的
export function merge3(server, base, mine) {
    if (same(mine, base)) {
        return clone(server);
    }
    const isObj = (v) => v && typeof v === 'object' && !Array.isArray(v);
    if (isObj(server) && isObj(base) && isObj(mine)) {
        const out = {};
        for (const k of new Set([...Object.keys(server), ...Object.keys(mine)])) {
            out[k] = merge3(server[k], base[k], mine[k]);
        }
        return out;
    }
    return clone(mine);
}

// 订阅显示名：没名字时用链接的主机名（同客户端 SubProfile 的缺省名）
export function profileName(p) {
    if (p?.name) {
        return p.name;
    }
    const m = String(p?.url || '').match(/^[a-z][a-z0-9+.-]*:\/\/([^/:?#]+)/i);
    return m ? m[1] : '订阅';
}

// 账户下还没下载到本机的订阅（同客户端 SubAccountStore.notDownloaded / OpenWrt 版 notDownloaded）：
// 短链路径（/x/<code>）相同就算已下载、不比主机名；接口式地址按「主机 + 路径 + 除 token / t 以外的参数」认；
// 只有 filename 的旧式地址按同主机 + filename 认
export function isDownloaded(sub, localUrls) {
    const parse = (u) => {
        const m = String(u || '').match(/^[a-z][a-z0-9+.-]*:\/\/([^/:?#]+)(?::\d+)?([^?#]*)(?:\?([^#]*))?/i);
        if (!m) {
            return null;
        }
        const query = new URLSearchParams(m[3] || '');
        const ident = [...query.entries()].filter((e) => e[0] != 'token' && e[0] != 't').map((e) => e[0] + '=' + e[1]).sort().join('&');
        const path = (m[2] || '/').replace(/\/+$/, '') || '/';
        return { host: m[1].toLowerCase(), path, filename: query.get('filename') || '', api: m[1].toLowerCase() + path + '?' + ident };
    };
    const r = parse(sub.url);
    if (!r) {
        return true;
    }
    const locals = localUrls.map(parse).filter(Boolean);
    if (sub.path && r.path.startsWith('/x/')) {
        return locals.some((l) => l.path == r.path);
    }
    if (sub.path) {
        return locals.some((l) => l.api == r.api);
    }
    return locals.some((l) => l.host == r.host && l.filename && l.filename == r.filename);
}

// 延迟的颜色档：同客户端（< 200 快、< 500 中、其余慢，0 = 超时 / 失败）
export function delayClass(ms) {
    if (ms == null) {
        return '';
    }
    if (ms <= 0) {
        return 'is-fail';
    }
    return ms < 200 ? 'is-fast' : ms < 500 ? 'is-mid' : 'is-slow';
}

// 复制：http 访问 NAS 不是安全上下文，navigator.clipboard 不一定有，退回 execCommand
export async function copyText(text) {
    try {
        if (navigator.clipboard && window.isSecureContext) {
            await navigator.clipboard.writeText(text);
            return true;
        }
    } catch { }
    const area = document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', '');
    area.style.position = 'fixed';
    area.style.opacity = '0';
    document.body.appendChild(area);
    area.select();
    let ok = false;
    try {
        ok = document.execCommand('copy');
    } catch { }
    area.remove();
    return ok;
}
