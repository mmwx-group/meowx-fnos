// 控制服务 API（docs/api.md）。出错一律抛 ApiError，message 是给人看的中文原因

let onUnauthorized = () => { };

// 任意请求回 401（设了网页密码、会话失效）时调：界面切到登录框
export function setUnauthorizedHandler(fn) {
    onUnauthorized = fn;
}

export class ApiError extends Error {
    constructor(message, status, data) {
        super(message);
        this.status = status;
        this.data = data;
    }
}

function messageOf(data) {
    if (data && typeof data === 'object') {
        return data.error || data.message || '';
    }
    return typeof data === 'string' ? data.trim().slice(0, 300) : '';
}

// body 为字符串且 raw = true 时原样发（上传配置文件）
export async function request(method, path, body, { raw = false } = {}) {
    const init = { method, credentials: 'same-origin', headers: {} };
    if (body !== undefined) {
        if (raw) {
            init.body = body;
            init.headers['Content-Type'] = 'text/yaml; charset=utf-8';
        } else {
            init.body = JSON.stringify(body);
            init.headers['Content-Type'] = 'application/json';
        }
    }
    let res;
    try {
        res = await fetch('/api' + path, init);
    } catch (err) {
        throw new ApiError('连不上控制服务', 0);
    }
    const text = await res.text();
    let data = null;
    if (text) {
        try {
            data = JSON.parse(text);
        } catch {
            data = text;
        }
    }
    if (res.status === 401 && path !== '/login') {
        onUnauthorized();
    }
    if (!res.ok) {
        throw new ApiError(messageOf(data) || `请求失败（HTTP ${res.status}）`, res.status, data);
    }
    return data;
}

export const get = (path) => request('GET', path);
export const post = (path, body) => request('POST', path, body === undefined ? {} : body);
export const put = (path, body) => request('PUT', path, body);
export const patch = (path, body) => request('PATCH', path, body);
export const del = (path) => request('DELETE', path);

// 路径里的一段（订阅 ID、代理组 / 节点名）
export const seg = (s) => encodeURIComponent(s);
